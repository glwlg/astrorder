package projection_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

// resyncFrame builds the exact frame shape the daemon emits when it rejects a
// subscription cursor.
func resyncFrame(sessionID string, after, minimum, through int64, reason pb.ResyncReason, current *string) *pb.EventStreamFrame {
	return &pb.EventStreamFrame{Frame: &pb.EventStreamFrame_ResyncRequired{ResyncRequired: &pb.ResyncRequired{
		Session:               &pb.SessionRef{SessionId: sessionID},
		RequestedAfterSeqId:   after,
		MinRetainedSeqId:      minimum,
		CommittedThroughSeqId: through,
		Reason:                reason,
		CurrentDaemonId:       current,
	}}}
}

// successFrames returns the snapshot and live-ready pair for one healthy stream.
func successFrames(epoch, sessionID string, through int64) []*pb.EventStreamFrame {
	return []*pb.EventStreamFrame{
		{Frame: &pb.EventStreamFrame_Snapshot{Snapshot: &pb.StateSnapshot{DaemonId: epoch, Sessions: []*pb.SessionSnapshot{
			{Session: &pb.SessionRef{SessionId: sessionID}, ReplayThroughSeqId: through},
		}}}},
		{Frame: &pb.EventStreamFrame_LiveReady{LiveReady: &pb.LiveReady{DaemonId: epoch, Watermarks: []*pb.SessionWatermark{
			{Session: &pb.SessionRef{SessionId: sessionID}, ReplayThroughSeqId: through},
		}}}},
	}
}

type retiredHarness struct {
	ctx      context.Context
	cancel   context.CancelFunc
	store    *store.Store
	ready    chan struct{}
	done     chan error
	attempts atomic.Int32
}

// startRetiredHarness wires a synthetic store and fake gRPC daemon exactly like
// the startup path: the projection subscribes with the durable checkpoints.
func startRetiredHarness(t *testing.T, epoch string, seed map[string]int64) *retiredHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	st, err := store.Open(filepath.Join(t.TempDir(), "retired-boundary.db"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	for stream, seq := range seed {
		if _, err := st.SetCheckpoint(ctx, epoch, stream, seq); err != nil {
			cancel()
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cancel()
		st.Close()
	})
	return &retiredHarness{ctx: ctx, cancel: cancel, store: st, ready: make(chan struct{}, 1), done: make(chan error, 1)}
}

func (h *retiredHarness) run(t *testing.T, fake *fakeEventsServer) {
	t.Helper()
	address, stop := startFakeEventsServer(t, fake)
	t.Cleanup(stop)
	client, err := daemonclient.Dial(h.ctx, daemonclient.Config{Target: address, Credential: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	proj, err := projection.New(client, h.store, &testPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	go func() { h.done <- proj.RunReady(h.ctx, h.ready) }()
}

func (h *retiredHarness) waitSettled(t *testing.T, wantReady bool) error {
	t.Helper()
	select {
	case <-h.ready:
		if !wantReady {
			t.Fatal("retired-stream resync was accepted when it should have failed")
		}
		return nil
	case err := <-h.done:
		if wantReady {
			t.Fatalf("retired-stream resync blocked startup: %v", err)
		}
		if err == nil || !projection.IsFatalProjectionError(err) {
			t.Fatalf("expected fatal resync error, got: %v", err)
		}
		return err
	case <-time.After(4 * time.Second):
		t.Fatal("projection did not settle")
		return nil
	}
}

func TestRetiredStreamResyncRequiresExactCheckpointAfter(t *testing.T) {
	epoch := "epoch-exact-after"
	h := startRetiredHarness(t, epoch, map[string]int64{"retired-control": 29, "current-control": 7})
	fake := &fakeEventsServer{daemonID: epoch}
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		h.attempts.Add(1)
		for _, cursor := range req.Cursors {
			if cursor.Session.GetSessionId() == "retired-control" {
				// requested_after disagrees with the durable checkpoint: this is
				// not the confirmed no-history signal and must stay fatal.
				return stream.Send(resyncFrame("retired-control", 0, 0, 0, pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, nil))
			}
		}
		for _, frame := range successFrames(epoch, "current-control", 7) {
			if err := stream.Send(frame); err != nil {
				return err
			}
		}
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	h.run(t, fake)
	err := h.waitSettled(t, false)
	if !strings.Contains(err.Error(), "stream=retired-control") {
		t.Fatalf("resync error lost the stream identity: %v", err)
	}
	if h.attempts.Load() != 1 {
		t.Fatalf("mismatched resync must fail on the first subscription, attempts=%d", h.attempts.Load())
	}
	checkpoints, cpErr := h.store.ListCheckpoints(context.Background(), epoch)
	if cpErr != nil || checkpoints["retired-control"] != 29 || checkpoints["current-control"] != 7 {
		t.Fatalf("durable checkpoints changed: %v %v", checkpoints, cpErr)
	}
}

func TestRetiredStreamResyncRejectsRetainedHistorySignals(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		minimum int64
		through int64
	}{
		{"min retained", 30, 0},
		{"committed through", 0, 10},
		{"both bounds", 30, 40},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			epoch := "epoch-bounds-" + strings.ReplaceAll(testCase.name, " ", "-")
			h := startRetiredHarness(t, epoch, map[string]int64{"retired-control": 29})
			fake := &fakeEventsServer{daemonID: epoch}
			fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
				h.attempts.Add(1)
				return stream.Send(resyncFrame("retired-control", 29, testCase.minimum, testCase.through, pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, nil))
			}
			h.run(t, fake)
			h.waitSettled(t, false)
			if h.attempts.Load() != 1 {
				t.Fatalf("bounded resync must fail on the first subscription, attempts=%d", h.attempts.Load())
			}
		})
	}
}

func TestRetiredStreamResyncRejectsForeignEpoch(t *testing.T) {
	epoch := "epoch-current"
	h := startRetiredHarness(t, epoch, map[string]int64{"retired-control": 29})
	foreign := "epoch-other"
	fake := &fakeEventsServer{daemonID: epoch}
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		h.attempts.Add(1)
		return stream.Send(resyncFrame("retired-control", 29, 0, 0, pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, &foreign))
	}
	h.run(t, fake)
	h.waitSettled(t, false)
	if h.attempts.Load() != 1 {
		t.Fatalf("foreign-epoch resync must fail on the first subscription, attempts=%d", h.attempts.Load())
	}
}

func TestRetiredStreamCursorExpiredStaysFatal(t *testing.T) {
	epoch := "epoch-cursor-expired"
	h := startRetiredHarness(t, epoch, map[string]int64{"retired-control": 29, "current-control": 7})
	fake := &fakeEventsServer{daemonID: epoch}
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		h.attempts.Add(1)
		return stream.Send(resyncFrame("retired-control", 29, 30, 40, pb.ResyncReason_RESYNC_REASON_CURSOR_EXPIRED, nil))
	}
	h.run(t, fake)
	err := h.waitSettled(t, false)
	if !strings.Contains(err.Error(), "reason=RESYNC_REASON_CURSOR_EXPIRED") || !strings.Contains(err.Error(), "stream=retired-control") {
		t.Fatalf("real history gap error lost its diagnosis: %v", err)
	}
	checkpoints, cpErr := h.store.ListCheckpoints(context.Background(), epoch)
	if cpErr != nil || checkpoints["retired-control"] != 29 {
		t.Fatalf("durable checkpoint changed after fatal gap: %v %v", checkpoints, cpErr)
	}
}

func TestRetiredStreamResyncAfterSnapshotIsFatal(t *testing.T) {
	epoch := "epoch-after-snapshot"
	h := startRetiredHarness(t, epoch, map[string]int64{"retired-control": 29, "current-control": 7})
	fake := &fakeEventsServer{daemonID: epoch}
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		h.attempts.Add(1)
		snapshot := &pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Snapshot{Snapshot: &pb.StateSnapshot{DaemonId: epoch, Sessions: []*pb.SessionSnapshot{
			{Session: &pb.SessionRef{SessionId: "current-control"}, ReplayThroughSeqId: 7},
		}}}}
		if err := stream.Send(snapshot); err != nil {
			return err
		}
		return stream.Send(resyncFrame("retired-control", 29, 0, 0, pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, nil))
	}
	h.run(t, fake)
	err := h.waitSettled(t, false)
	if !strings.Contains(err.Error(), "reason=RESYNC_REASON_UNKNOWN_SESSION") || !strings.Contains(err.Error(), "stream=retired-control") {
		t.Fatalf("post-snapshot resync error lost its diagnosis: %v", err)
	}
	if h.attempts.Load() != 1 {
		t.Fatalf("post-snapshot resync must fail immediately, attempts=%d", h.attempts.Load())
	}
	checkpoints, cpErr := h.store.ListCheckpoints(context.Background(), epoch)
	if cpErr != nil || checkpoints["retired-control"] != 29 {
		t.Fatalf("durable checkpoint changed after fatal resync: %v %v", checkpoints, cpErr)
	}
}

func TestRetiredStreamResyncForUnsubscribedStreamIsFatal(t *testing.T) {
	epoch := "epoch-ghost"
	h := startRetiredHarness(t, epoch, map[string]int64{"current-control": 7})
	fake := &fakeEventsServer{daemonID: epoch}
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		h.attempts.Add(1)
		return stream.Send(resyncFrame("ghost-control", 0, 0, 0, pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, nil))
	}
	h.run(t, fake)
	err := h.waitSettled(t, false)
	if !strings.Contains(err.Error(), "stream=ghost-control") {
		t.Fatalf("unsubscribed-stream resync error lost the stream identity: %v", err)
	}
	if h.attempts.Load() != 1 {
		t.Fatalf("unsubscribed-stream resync must fail immediately, attempts=%d", h.attempts.Load())
	}
}

func TestRetiredStreamIdentityReuseIsFatal(t *testing.T) {
	epoch := "epoch-identity-reuse"
	h := startRetiredHarness(t, epoch, map[string]int64{"retired-control": 29, "current-control": 7})
	fake := &fakeEventsServer{daemonID: epoch}
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		attempt := h.attempts.Add(1)
		if attempt == 1 {
			return stream.Send(resyncFrame("retired-control", 29, 0, 0, pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, nil))
		}
		// The retired identity reappeared in a later snapshot: the old
		// checkpoint must never be applied to it again.
		snapshot := &pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Snapshot{Snapshot: &pb.StateSnapshot{DaemonId: epoch, Sessions: []*pb.SessionSnapshot{
			{Session: &pb.SessionRef{SessionId: "retired-control"}, ReplayThroughSeqId: 1},
			{Session: &pb.SessionRef{SessionId: "current-control"}, ReplayThroughSeqId: 7},
		}}}}
		return stream.Send(snapshot)
	}
	h.run(t, fake)
	err := h.waitSettled(t, false)
	if !strings.Contains(err.Error(), "retired stream identity was reused") {
		t.Fatalf("identity reuse was not diagnosed: %v", err)
	}
	if h.attempts.Load() != 2 {
		t.Fatalf("identity reuse must fail on the second subscription, attempts=%d", h.attempts.Load())
	}
	checkpoints, cpErr := h.store.ListCheckpoints(context.Background(), epoch)
	if cpErr != nil || checkpoints["retired-control"] != 29 {
		t.Fatalf("durable checkpoint changed after identity reuse: %v %v", checkpoints, cpErr)
	}
}

func TestRetiredStreamsRetireIndividuallyWithoutResubscribing(t *testing.T) {
	epoch := "epoch-multi-retire"
	h := startRetiredHarness(t, epoch, map[string]int64{"retired-a": 5, "retired-b": 7, "current-control": 7})
	fake := &fakeEventsServer{daemonID: epoch}
	var firstRetired atomic.Value
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		attempt := h.attempts.Add(1)
		present := make(map[string]int64, len(req.Cursors))
		for _, cursor := range req.Cursors {
			present[cursor.Session.GetSessionId()] = cursor.AfterSeqId
		}
		if attempt >= 2 {
			if first, ok := firstRetired.Load().(string); ok && first != "" {
				if _, resubscribed := present[first]; resubscribed {
					t.Errorf("retired stream %s was resubscribed", first)
				}
			}
		}
		for _, streamID := range []string{"retired-a", "retired-b"} {
			if after, ok := present[streamID]; ok {
				if attempt == 1 {
					firstRetired.Store(streamID)
				}
				return stream.Send(resyncFrame(streamID, after, 0, 0, pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, nil))
			}
		}
		for _, streamID := range []string{"retired-a", "retired-b"} {
			if _, ok := present[streamID]; ok {
				t.Errorf("retired stream %s still subscribed after retirement", streamID)
			}
		}
		for _, frame := range successFrames(epoch, "current-control", 7) {
			if err := stream.Send(frame); err != nil {
				return err
			}
		}
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	h.run(t, fake)
	h.waitSettled(t, true)
	h.cancel()
	<-h.done
	if h.attempts.Load() != 3 {
		t.Fatalf("expected exactly one retirement per stream, attempts=%d", h.attempts.Load())
	}
	checkpoints, cpErr := h.store.ListCheckpoints(context.Background(), epoch)
	if cpErr != nil || checkpoints["retired-a"] != 5 || checkpoints["retired-b"] != 7 || checkpoints["current-control"] != 7 {
		t.Fatalf("durable checkpoints changed: %v %v", checkpoints, cpErr)
	}
}

func TestRetiredStreamRepeatedUnknownStaysFatal(t *testing.T) {
	epoch := "epoch-repeat-unknown"
	h := startRetiredHarness(t, epoch, map[string]int64{"retired-control": 29, "current-control": 7})
	fake := &fakeEventsServer{daemonID: epoch}
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		h.attempts.Add(1)
		// The daemon keeps naming the stream even after it was retired.
		return stream.Send(resyncFrame("retired-control", 29, 0, 0, pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, nil))
	}
	h.run(t, fake)
	err := h.waitSettled(t, false)
	if !strings.Contains(err.Error(), "stream=retired-control") {
		t.Fatalf("repeated unknown-session resync lost the stream identity: %v", err)
	}
	if h.attempts.Load() != 2 {
		t.Fatalf("repeated unknown-session resync must fail after one retirement, attempts=%d", h.attempts.Load())
	}
}

func TestRetiredStreamIdentityReuseInLiveFrameIsFatal(t *testing.T) {
	epoch := "epoch-live-reuse"
	h := startRetiredHarness(t, epoch, map[string]int64{"retired-control": 29, "current-control": 7})
	fake := &fakeEventsServer{daemonID: epoch}
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		attempt := h.attempts.Add(1)
		if attempt == 1 {
			return stream.Send(resyncFrame("retired-control", 29, 0, 0, pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, nil))
		}
		// The retired identity reappears as a live envelope without ever being
		// named in a snapshot: the old checkpoint must not be silently applied.
		for _, frame := range successFrames(epoch, "current-control", 7) {
			if err := stream.Send(frame); err != nil {
				return err
			}
		}
		return stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Live{Live: &pb.EventEnvelope{
			DaemonId:      epoch,
			Session:       &pb.SessionRef{SessionId: "retired-control", AgentId: strPtr("agent-a")},
			SeqId:         30,
			EventType:     "ping",
			NativePayload: makeNativePayload(map[string]any{}),
		}}})
	}
	h.run(t, fake)
	// Live-ready is reached for the healthy stream; the fatal error follows
	// when the retired identity reappears in a live frame.
	select {
	case <-h.ready:
	case err := <-h.done:
		t.Fatalf("live identity reuse fired before live-ready: %v", err)
	case <-time.After(4 * time.Second):
		t.Fatal("projection did not reach live-ready")
	}
	select {
	case err := <-h.done:
		if err == nil || !projection.IsFatalProjectionError(err) || !strings.Contains(err.Error(), "retired stream identity was reused") {
			t.Fatalf("live identity reuse was not diagnosed: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("live identity reuse was not detected")
	}
	checkpoints, cpErr := h.store.ListCheckpoints(context.Background(), epoch)
	if cpErr != nil || checkpoints["retired-control"] != 29 {
		t.Fatalf("durable checkpoint changed after live identity reuse: %v %v", checkpoints, cpErr)
	}
}

func TestProjectionStreamOverflowStaysFatal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := store.Open(filepath.Join(t.TempDir(), "overflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	epoch := "epoch-overflow"
	fake := &fakeEventsServer{daemonID: epoch, frames: []*pb.EventStreamFrame{
		{Frame: &pb.EventStreamFrame_Snapshot{Snapshot: &pb.StateSnapshot{DaemonId: epoch}}},
		{Frame: &pb.EventStreamFrame_LiveReady{LiveReady: &pb.LiveReady{DaemonId: epoch}}},
		{Frame: &pb.EventStreamFrame_Overflow{Overflow: &pb.StreamOverflow{QueueCapacity: 16, Reason: "fixture overflow"}}},
	}}
	address, stop := startFakeEventsServer(t, fake)
	defer stop()
	client, err := daemonclient.Dial(ctx, daemonclient.Config{Target: address, Credential: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	proj, err := projection.New(client, st, &testPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	err = proj.Run(ctx)
	if err == nil || !projection.IsFatalProjectionError(err) || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("expected fatal overflow error, got: %v", err)
	}
}

func TestProjectionUnknownFrameStaysFatal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := store.Open(filepath.Join(t.TempDir(), "unknown-frame.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	epoch := "epoch-unknown-frame"
	fake := &fakeEventsServer{daemonID: epoch, frames: []*pb.EventStreamFrame{
		{Frame: &pb.EventStreamFrame_Snapshot{Snapshot: &pb.StateSnapshot{DaemonId: epoch}}},
		{Frame: &pb.EventStreamFrame_LiveReady{LiveReady: &pb.LiveReady{DaemonId: epoch}}},
		{}, // an unrecognized frame shape must never be treated as benign
	}}
	address, stop := startFakeEventsServer(t, fake)
	defer stop()
	client, err := daemonclient.Dial(ctx, daemonclient.Config{Target: address, Credential: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	proj, err := projection.New(client, st, &testPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	err = proj.Run(ctx)
	if err == nil || !projection.IsFatalProjectionError(err) || !strings.Contains(err.Error(), "unrecognized stream frame") {
		t.Fatalf("expected fatal corrupted-frame error, got: %v", err)
	}
}
