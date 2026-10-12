package projection_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

func TestUnknownRetiredStreamCheckpointCannotBlockAppStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, err := store.Open(filepath.Join(t.TempDir(), "resync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	epoch := "same-persisted-daemon-epoch"
	if _, err := st.SetCheckpoint(ctx, epoch, "retired-control", 29); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetCheckpoint(ctx, epoch, "current-control", 7); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	fake := &fakeEventsServer{daemonID: epoch}
	fake.subscribe = func(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		attempts.Add(1)
		currentFound := false
		for _, cursor := range req.Cursors {
			if cursor.Session.GetSessionId() == "retired-control" {
				return stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_ResyncRequired{ResyncRequired: &pb.ResyncRequired{Session: &pb.SessionRef{SessionId: "retired-control"}, RequestedAfterSeqId: 29, Reason: pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION}}})
			}
			if cursor.Session.GetSessionId() == "current-control" && cursor.AfterSeqId == 7 {
				currentFound = true
			}
		}
		if !currentFound {
			t.Error("resync discarded current stream checkpoint")
		}
		for _, frame := range []*pb.EventStreamFrame{
			{Frame: &pb.EventStreamFrame_Snapshot{Snapshot: &pb.StateSnapshot{DaemonId: epoch, Sessions: []*pb.SessionSnapshot{{Session: &pb.SessionRef{SessionId: "current-control"}, ReplayThroughSeqId: 7}}}}},
			{Frame: &pb.EventStreamFrame_LiveReady{LiveReady: &pb.LiveReady{DaemonId: epoch, Watermarks: []*pb.SessionWatermark{{Session: &pb.SessionRef{SessionId: "current-control"}, ReplayThroughSeqId: 7}}}}},
		} {
			if err := stream.Send(frame); err != nil {
				return err
			}
		}
		<-stream.Context().Done()
		return stream.Context().Err()
	}
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
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- proj.RunReady(ctx, ready) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal("retired stream prevented startup", err)
	case <-ctx.Done():
		t.Fatal("resync never reached live-ready")
	}
	cancel()
	<-done
	if attempts.Load() != 2 {
		t.Fatal("resync retried unexpectedly", attempts.Load())
	}
	checkpoints, err := st.ListCheckpoints(context.Background(), epoch)
	if err != nil || checkpoints["current-control"] != 7 || checkpoints["retired-control"] != 29 {
		t.Fatal("resync rewrote durable checkpoint history", checkpoints, err)
	}
}
