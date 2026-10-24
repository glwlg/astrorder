package grpcapi_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/grpcapi"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testSecret = "grpc-test-secret"

type grpcFixture struct {
	t      *testing.T
	daemon *protocol.Daemon
	server *grpc.Server
	conn   *grpc.ClientConn
	ctx    context.Context
}

func startFixture(t *testing.T, options ...grpcapi.Option) *grpcFixture {
	t.Helper()
	daemon, err := protocol.Open("legacy-ws-secret", filepath.Join(t.TempDir(), "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := grpcapi.NewServer(grpcapi.Config{Credential: testSecret}, daemon, options...)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(grpcapi.AuthorizationMetadataKey, "Bearer "+testSecret))
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(
		dialCtx,
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(grpcapi.MaxWireBytes),
			grpc.MaxCallSendMsgSize(grpcapi.MaxWireBytes),
		),
	)
	if err != nil {
		server.Stop()
		listener.Close()
		daemon.Close()
		t.Fatal(err)
	}
	fixture := &grpcFixture{t: t, daemon: daemon, server: server, conn: conn, ctx: ctx}
	t.Cleanup(func() {
		conn.Close()
		server.Stop()
		listener.Close()
		daemon.Close()
	})
	return fixture
}

func (f *grpcFixture) append(sessionID, event, status string, payload string) *sessiondv1.AppendEventResponse {
	f.t.Helper()
	response, err := sessiondv1.NewEventStreamClient(f.conn).AppendEvent(f.ctx, &sessiondv1.AppendEventRequest{
		Session:   &sessiondv1.SessionRef{SessionId: sessionID},
		EventType: event,
		Status:    statusValue(status),
		Payload:   &sessiondv1.NativeJson{Schema: "test.event.v1", Utf8Json: []byte(payload)},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return response
}

func statusValue(value string) sessiondv1.SessionStatus {
	switch value {
	case "idle":
		return sessiondv1.SessionStatus_SESSION_STATUS_IDLE
	case "running":
		return sessiondv1.SessionStatus_SESSION_STATUS_RUNNING
	case "waiting_approval":
		return sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL
	case "error":
		return sessiondv1.SessionStatus_SESSION_STATUS_ERROR
	default:
		return sessiondv1.SessionStatus_SESSION_STATUS_UNSPECIFIED
	}
}

func TestAuthenticationCapabilitiesAndRegisteredSurface(t *testing.T) {
	f := startFixture(t)
	client := sessiondv1.NewDaemonControlClient(f.conn)
	for name, ctx := range map[string]context.Context{
		"missing": context.Background(),
		"wrong":   metadata.NewOutgoingContext(context.Background(), metadata.Pairs(grpcapi.AuthorizationMetadataKey, "Bearer wrong")),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.GetCapabilities(ctx, &sessiondv1.GetCapabilitiesRequest{})
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("authentication code = %v, want Unauthenticated: %v", status.Code(err), err)
			}
		})
	}

	capabilities, err := client.GetCapabilities(f.ctx, &sessiondv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	wantServices := []string{"astrorder.sessiond.v1.ConnectorIngress", "astrorder.sessiond.v1.DaemonControl", "astrorder.sessiond.v1.EventStream", "astrorder.sessiond.v1.ModelConfig", "astrorder.sessiond.v1.OperationQuery", "astrorder.sessiond.v1.RuntimeQuery", "astrorder.sessiond.v1.SessionControl", "astrorder.sessiond.v1.TerminalStream"}
	if capabilities.ProtocolMajor != 1 || capabilities.ProtocolMinor != 2 || capabilities.DaemonId == "" || fmt.Sprint(capabilities.Services) != fmt.Sprint(wantServices) {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	if capabilities.MaxBusinessBytes != sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES || capabilities.MaxWireBytes != sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES || capabilities.EventStreamVersion != sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1 || !capabilities.OperationLookup {
		t.Fatalf("inaccurate capabilities = %+v", capabilities)
	}
	if got := f.server.GetServiceInfo(); len(got) != len(wantServices) || got[wantServices[0]].Methods == nil || got[wantServices[1]].Methods == nil || got[wantServices[2]].Methods == nil {
		t.Fatalf("registered services = %+v", got)
	}
	if got := f.server.GetServiceInfo(); got[wantServices[3]].Methods == nil {
		t.Fatal("RuntimeQuery registration has no methods")
	}

	stream, err := sessiondv1.NewEventStreamClient(f.conn).Subscribe(context.Background(), &sessiondv1.SubscribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("stream authentication code = %v, want Unauthenticated: %v", status.Code(err), err)
	}
}

func TestStatusAndShutdownUseSharedAuthority(t *testing.T) {
	f := startFixture(t)
	control := sessiondv1.NewDaemonControlClient(f.conn)
	f.append("approval-session", "approval.requested", "waiting_approval", `{"approval_id":"a-1"}`)

	statusResponse, err := control.GetStatus(f.ctx, &sessiondv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if statusResponse.DaemonId == "" || statusResponse.PendingOperations == nil || *statusResponse.PendingOperations != 0 || statusResponse.StorageError != nil {
		t.Fatalf("status authority incomplete: %+v", statusResponse)
	}
	if len(statusResponse.Sessions) != 1 || statusResponse.Sessions[0].Session.SessionId != "approval-session" || statusResponse.Sessions[0].Status != sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL {
		t.Fatalf("status sessions = %+v", statusResponse.Sessions)
	}

	wrongID := "wrong-daemon"
	_, err = control.Shutdown(f.ctx, &sessiondv1.ShutdownRequest{ConfirmActive: true, ExpectedDaemonId: &wrongID})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("wrong daemon shutdown = %v", err)
	}
	select {
	case <-f.daemon.ShutdownRequested():
		t.Fatal("identity mismatch signaled shutdown")
	default:
	}

	_, err = control.Shutdown(f.ctx, &sessiondv1.ShutdownRequest{})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("active shutdown = %v", err)
	}
	select {
	case <-f.daemon.ShutdownRequested():
		t.Fatal("refused active shutdown signaled shutdown")
	default:
	}

	response, err := control.Shutdown(f.ctx, &sessiondv1.ShutdownRequest{ConfirmActive: true, ExpectedDaemonId: &statusResponse.DaemonId})
	if err != nil || !response.Accepted || !response.GracefulDrainStarted || response.DaemonId != statusResponse.DaemonId {
		t.Fatalf("confirmed shutdown = %+v, %v", response, err)
	}
	select {
	case <-f.daemon.ShutdownRequested():
	case <-time.After(time.Second):
		t.Fatal("accepted gRPC shutdown was not signaled")
	}
}

func TestStatusFailsClosedWhenDurableStorageIsIncomplete(t *testing.T) {
	f := startFixture(t)
	f.append("known", "token", "idle", `{"n":1}`)
	if err := f.daemon.Close(); err != nil {
		t.Fatal(err)
	}
	response, err := sessiondv1.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &sessiondv1.GetStatusRequest{})
	if status.Code(err) != codes.Unavailable || response != nil {
		t.Fatalf("closed storage status = %+v, %v", response, err)
	}
}

func TestAppendRejectsInvalidJSONAndBusinessLimitBeforePersistence(t *testing.T) {
	f := startFixture(t)
	client := sessiondv1.NewEventStreamClient(f.conn)
	requests := []*sessiondv1.AppendEventRequest{
		{Session: &sessiondv1.SessionRef{SessionId: "invalid-json"}, EventType: "token", Status: sessiondv1.SessionStatus_SESSION_STATUS_RUNNING, Payload: &sessiondv1.NativeJson{Utf8Json: []byte(`{"unterminated"`)}},
		{Session: &sessiondv1.SessionRef{SessionId: "too-large"}, EventType: "token", Status: sessiondv1.SessionStatus_SESSION_STATUS_RUNNING, Payload: &sessiondv1.NativeJson{Utf8Json: []byte(`"` + strings.Repeat("x", grpcapi.MaxBusinessBytes) + `"`)}},
	}
	for _, request := range requests {
		_, err := client.AppendEvent(f.ctx, request)
		if status.Code(err) != codes.InvalidArgument && status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("invalid append code = %v: %v", status.Code(err), err)
		}
	}
	statusResponse, err := sessiondv1.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &sessiondv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(statusResponse.Sessions) != 0 {
		t.Fatalf("invalid events were persisted: %+v", statusResponse.Sessions)
	}
}

func TestSubscribeReplaysAllSessionsAcrossPagesThenStreamsLive(t *testing.T) {
	const sessionCount = 130
	f := startFixture(t)
	for i := 0; i < sessionCount; i++ {
		id := fmt.Sprintf("session-%03d", i)
		for seq := 1; seq <= 3; seq++ {
			f.append(id, "token", "running", fmt.Sprintf(`{"n":%d,"exact":9007199254740993}`, seq))
		}
	}

	stream, err := sessiondv1.NewEventStreamClient(f.conn).Subscribe(f.ctx, &sessiondv1.SubscribeRequest{ReplayPageSize: 2, IncludeSnapshot: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshotFrame, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotFrame.GetSnapshot()
	if snapshot == nil || len(snapshot.Sessions) != sessionCount {
		t.Fatalf("snapshot sessions = %d", len(snapshot.GetSessions()))
	}

	replayed := map[string][]int64{}
	for {
		frame, recvErr := stream.Recv()
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		if ready := frame.GetLiveReady(); ready != nil {
			if len(ready.Watermarks) != sessionCount {
				t.Fatalf("watermarks = %d", len(ready.Watermarks))
			}
			break
		}
		event := frame.GetReplay()
		if event == nil {
			t.Fatalf("frame before live-ready is not replay: %+v", frame)
		}
		replayed[event.Session.SessionId] = append(replayed[event.Session.SessionId], event.SeqId)
		if !strings.Contains(string(event.GetNative().Utf8Json), `9007199254740993`) {
			t.Fatalf("native integer changed: %s", event.GetNative().Utf8Json)
		}
	}
	if len(replayed) != sessionCount {
		t.Fatalf("replayed sessions = %d", len(replayed))
	}
	for id, seqs := range replayed {
		if fmt.Sprint(seqs) != "[1 2 3]" {
			t.Fatalf("%s replay = %v", id, seqs)
		}
	}

	appended := f.append("new-during-live", "created", "idle", `{"n":1}`)
	liveFrame, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if live := liveFrame.GetLive(); live == nil || live.Session.SessionId != "new-during-live" || live.SeqId != appended.SeqId {
		t.Fatalf("live event = %+v", liveFrame)
	}
}

func TestSubscribeCoversNewSessionAndLiveEventDuringReplay(t *testing.T) {
	replayStarted := make(chan struct{})
	releaseReplay := make(chan struct{})
	f := startFixture(t, grpcapi.WithReplayPageHook(func(sessionID string, page int) {
		if sessionID == "history" && page == 1 {
			close(replayStarted)
			<-releaseReplay
		}
	}))
	for i := 0; i < 4; i++ {
		f.append("history", "token", "running", fmt.Sprintf(`{"n":%d}`, i))
	}
	stream, err := sessiondv1.NewEventStreamClient(f.conn).Subscribe(f.ctx, &sessiondv1.SubscribeRequest{ReplayPageSize: 1, IncludeSnapshot: true})
	if err != nil {
		t.Fatal(err)
	}
	if first, err := stream.Recv(); err != nil || first.GetSnapshot() == nil {
		t.Fatalf("snapshot = %+v, %v", first, err)
	}
	select {
	case <-replayStarted:
	case <-time.After(time.Second):
		t.Fatal("replay did not reach page hook")
	}
	f.append("history", "token", "idle", `{"n":5}`)
	f.append("new-session", "created", "idle", `{"n":1}`)
	close(releaseReplay)

	var kinds []string
	var live []string
	for len(live) < 2 {
		frame, recvErr := stream.Recv()
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		switch {
		case frame.GetReplay() != nil:
			kinds = append(kinds, "replay")
		case frame.GetLiveReady() != nil:
			kinds = append(kinds, "ready")
		case frame.GetLive() != nil:
			kinds = append(kinds, "live")
			live = append(live, fmt.Sprintf("%s:%d", frame.GetLive().Session.SessionId, frame.GetLive().SeqId))
		default:
			t.Fatalf("unexpected frame: %+v", frame)
		}
	}
	firstReady := -1
	for i, kind := range kinds {
		if kind == "ready" {
			firstReady = i
			break
		}
	}
	if firstReady < 0 {
		t.Fatal("missing live-ready")
	}
	for _, kind := range kinds[:firstReady] {
		if kind == "live" {
			t.Fatalf("live preceded barrier: %v", kinds)
		}
	}
	sort.Strings(live)
	if fmt.Sprint(live) != "[history:5 new-session:1]" {
		t.Fatalf("replay gap events = %v", live)
	}
}

func TestSubscribeSignalsEpochCursorAndUnknownSessionResync(t *testing.T) {
	f := startFixture(t)
	f.append("known", "one", "idle", `{}`)
	wrong := "previous-daemon"
	cases := []struct {
		name    string
		request *sessiondv1.SubscribeRequest
		reason  sessiondv1.ResyncReason
	}{
		{name: "epoch", request: &sessiondv1.SubscribeRequest{ExpectedDaemonId: &wrong}, reason: sessiondv1.ResyncReason_RESYNC_REASON_DAEMON_EPOCH_CHANGED},
		{name: "unknown", request: &sessiondv1.SubscribeRequest{Cursors: []*sessiondv1.SessionCursor{{Session: &sessiondv1.SessionRef{SessionId: "missing"}}}}, reason: sessiondv1.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION},
		{name: "ahead", request: &sessiondv1.SubscribeRequest{Cursors: []*sessiondv1.SessionCursor{{Session: &sessiondv1.SessionRef{SessionId: "known"}, AfterSeqId: 2}}}, reason: sessiondv1.ResyncReason_RESYNC_REASON_CURSOR_EXPIRED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := sessiondv1.NewEventStreamClient(f.conn).Subscribe(f.ctx, tc.request)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if resync := frame.GetResyncRequired(); resync == nil || resync.Reason != tc.reason {
				t.Fatalf("resync = %+v", frame)
			}
			_, err = stream.Recv()
			if err != io.EOF {
				t.Fatalf("resync stream did not end cleanly: %v", err)
			}
		})
	}
}

func TestSubscribeReportsCursorExpiredByRetention(t *testing.T) {
	f := startFixture(t)
	if err := f.daemon.ConfigureRetention(2048, 600, 3600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		f.append("expired", "text", "idle", `{"text":"`+strings.Repeat("x", 200)+`"}`)
	}
	stream, err := sessiondv1.NewEventStreamClient(f.conn).Subscribe(f.ctx, &sessiondv1.SubscribeRequest{Cursors: []*sessiondv1.SessionCursor{{Session: &sessiondv1.SessionRef{SessionId: "expired"}, AfterSeqId: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	resync := frame.GetResyncRequired()
	if resync == nil || resync.Reason != sessiondv1.ResyncReason_RESYNC_REASON_CURSOR_EXPIRED || resync.MinRetainedSeqId <= 1 || resync.CommittedThroughSeqId != 6 {
		t.Fatalf("retention resync = %+v", frame)
	}
}

func TestSubscriptionOverflowTerminatesOnlyStreamAndLeavesDaemonUsable(t *testing.T) {
	replayStarted := make(chan struct{})
	releaseReplay := make(chan struct{})
	f := startFixture(t,
		grpcapi.WithSubscriptionQueueCapacity(1),
		grpcapi.WithReplayPageHook(func(sessionID string, page int) {
			if sessionID == "history" && page == 1 {
				close(replayStarted)
				<-releaseReplay
			}
		}),
	)
	f.append("history", "one", "running", `{}`)
	stream, err := sessiondv1.NewEventStreamClient(f.conn).Subscribe(f.ctx, &sessiondv1.SubscribeRequest{ReplayPageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-replayStarted:
	case <-time.After(time.Second):
		t.Fatal("replay did not block")
	}
	f.append("history", "two", "running", `{}`)
	f.append("history", "three", "idle", `{}`)
	close(releaseReplay)

	seenOverflow := false
	for {
		frame, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		if overflow := frame.GetOverflow(); overflow != nil {
			seenOverflow = true
			if overflow.QueueCapacity != 1 || len(overflow.LastEmitted) == 0 {
				t.Fatalf("overflow detail = %+v", overflow)
			}
		}
	}
	if !seenOverflow {
		t.Fatal("overflow was not reported")
	}
	if _, err := sessiondv1.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &sessiondv1.GetStatusRequest{}); err != nil {
		t.Fatalf("overflow stopped daemon control: %v", err)
	}
	select {
	case <-f.daemon.ShutdownRequested():
		t.Fatal("stream termination stopped daemon")
	default:
	}
}

func TestSendTimeoutReleasesSubscriptionWithoutStoppingDaemon(t *testing.T) {
	sendStarted := make(chan struct{})
	blockSend := make(chan struct{})
	f := startFixture(t,
		grpcapi.WithSendTimeout(50*time.Millisecond),
		grpcapi.WithSendHook(func(kind string) {
			if kind == "snapshot" {
				close(sendStarted)
				<-blockSend
			}
		}),
	)
	f.append("known", "one", "idle", `{}`)
	ctx, cancel := context.WithCancel(f.ctx)
	stream, err := sessiondv1.NewEventStreamClient(f.conn).Subscribe(ctx, &sessiondv1.SubscribeRequest{IncludeSnapshot: true})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sendStarted:
	case <-time.After(time.Second):
		t.Fatal("send hook did not start")
	}
	time.Sleep(80 * time.Millisecond)
	close(blockSend)
	_, err = stream.Recv()
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("send timeout = %v", err)
	}
	cancel()
	if _, err := sessiondv1.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &sessiondv1.GetStatusRequest{}); err != nil {
		t.Fatalf("send timeout stopped daemon: %v", err)
	}
	select {
	case <-f.daemon.ShutdownRequested():
		t.Fatal("send timeout stopped daemon")
	default:
	}
}
