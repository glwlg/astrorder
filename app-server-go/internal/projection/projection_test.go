package projection_test

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type testPublisher struct {
	mu     sync.Mutex
	events []store.Event
}

func (p *testPublisher) PublishCommitted(events []store.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, events...)
}

func (p *testPublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events)
}

type fakeEventsServer struct {
	sessiondv1.UnimplementedDaemonControlServer
	sessiondv1.UnimplementedSessionControlServer
	sessiondv1.UnimplementedRuntimeQueryServer
	sessiondv1.UnimplementedOperationQueryServer
	sessiondv1.UnimplementedModelConfigServer
	sessiondv1.UnimplementedEventStreamServer
	sessiondv1.UnimplementedTerminalStreamServer
	sessiondv1.UnimplementedConnectorIngressServer

	daemonID  string
	frames    []*sessiondv1.EventStreamFrame
	subscribe func(*sessiondv1.SubscribeRequest, grpc.ServerStreamingServer[sessiondv1.EventStreamFrame]) error
}

func (s *fakeEventsServer) GetCapabilities(ctx context.Context, req *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor: 1,
		ProtocolMinor: 0,
		DaemonId:      s.daemonID,
		Services: []string{
			"astrorder.sessiond.v1.ConnectorIngress",
			"astrorder.sessiond.v1.DaemonControl",
			"astrorder.sessiond.v1.EventStream",
			"astrorder.sessiond.v1.ModelConfig",
			"astrorder.sessiond.v1.OperationQuery",
			"astrorder.sessiond.v1.RuntimeQuery",
			"astrorder.sessiond.v1.SessionControl",
			"astrorder.sessiond.v1.TerminalStream",
		},
		CanonicalizationVersions: []sessiondv1.CanonicalizationVersion{
			sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
		},
		MaxWireBytes:       sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		MaxBusinessBytes:   sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		EventStreamVersion: sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:    true,
	}, nil
}

func (s *fakeEventsServer) Subscribe(req *sessiondv1.SubscribeRequest, stream grpc.ServerStreamingServer[sessiondv1.EventStreamFrame]) error {
	if s.subscribe != nil {
		return s.subscribe(req, stream)
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	_ = md
	for _, f := range s.frames {
		if err := stream.Send(f); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func startFakeEventsServer(t *testing.T, srv *fakeEventsServer) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	g := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(g, srv)
	sessiondv1.RegisterSessionControlServer(g, srv)
	sessiondv1.RegisterRuntimeQueryServer(g, srv)
	sessiondv1.RegisterOperationQueryServer(g, srv)
	sessiondv1.RegisterModelConfigServer(g, srv)
	sessiondv1.RegisterEventStreamServer(g, srv)
	sessiondv1.RegisterTerminalStreamServer(g, srv)
	sessiondv1.RegisterConnectorIngressServer(g, srv)

	go func() {
		_ = g.Serve(lis)
	}()

	cleanup := func() {
		g.Stop()
		_ = lis.Close()
	}
	return lis.Addr().String(), cleanup
}

func strPtr(s string) *string {
	return &s
}

func makeNativePayload(v any) *sessiondv1.NativeJson {
	b, _ := json.Marshal(v)
	return &sessiondv1.NativeJson{
		Schema:   "astrorder.event.payload.v1",
		Utf8Json: b,
	}
}

func TestProjectionReplayLiveAndCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "proj_test.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	daemonID := "daemon-epoch-1"

	// Construct frames for connector.hello, session.upsert, runtime.owner, hermes completion, token metrics
	helloPayload := map[string]any{
		"id":           "agent-hermes",
		"kind":         "hermes",
		"name":         "Hermes Agent",
		"status":       "ready",
		"capabilities": []any{"chat"},
	}

	sessionUpsertPayload := map[string]any{
		"agent_id": "agent-hermes",
		"type":     "session.upsert",
		"data": map[string]any{
			"id":        "session-1",
			"title":     "Test Session",
			"workspace": "/tmp",
			"status":    "running",
		},
	}

	runtimeOwnerPayload := map[string]any{
		"owner_pid":  12345,
		"session_id": "session-1",
	}

	cmdCompletePayload := map[string]any{
		"agent_id":   "agent-hermes",
		"session_id": "session-1",
		"command_id": "cmd-1",
	}

	tokenPayload := map[string]any{
		"agent_id":          "agent-hermes",
		"session_id":        "session-1",
		"model":             "test-model",
		"date":              "2026-10-09",
		"input_tokens":      150,
		"output_tokens":     250,
		"total_tokens":      400,
		"context_window":    128000,
		"cached_tokens":     50,
		"reasoning_tokens":  0,
		"last_input_tokens": 150,
	}

	frames := []*sessiondv1.EventStreamFrame{
		{
			Frame: &sessiondv1.EventStreamFrame_Snapshot{
				Snapshot: &sessiondv1.StateSnapshot{
					DaemonId: daemonID,
					Sessions: []*sessiondv1.SessionSnapshot{
						{
							Session:            &sessiondv1.SessionRef{SessionId: "session-1"},
							ReplayThroughSeqId: 2,
						},
					},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_Replay{
				Replay: &sessiondv1.EventEnvelope{
					DaemonId:      daemonID,
					Session:       &sessiondv1.SessionRef{SessionId: "session-1", AgentId: strPtr("agent-hermes")},
					SeqId:         1,
					EventType:     "connector.hello",
					NativePayload: makeNativePayload(helloPayload),
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_Replay{
				Replay: &sessiondv1.EventEnvelope{
					DaemonId:      daemonID,
					Session:       &sessiondv1.SessionRef{SessionId: "session-1", AgentId: strPtr("agent-hermes")},
					SeqId:         2,
					EventType:     "connector.event",
					NativePayload: makeNativePayload(sessionUpsertPayload),
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_LiveReady{
				LiveReady: &sessiondv1.LiveReady{
					DaemonId: daemonID,
					Watermarks: []*sessiondv1.SessionWatermark{
						{
							Session:            &sessiondv1.SessionRef{SessionId: "session-1"},
							ReplayThroughSeqId: 2,
						},
					},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId:      daemonID,
					Session:       &sessiondv1.SessionRef{SessionId: "session-1", AgentId: strPtr("agent-hermes")},
					SeqId:         3,
					EventType:     "runtime.owner",
					NativePayload: makeNativePayload(runtimeOwnerPayload),
					Payload: &sessiondv1.EventEnvelope_RuntimeOwner{
						RuntimeOwner: &sessiondv1.RuntimeOwnerEvent{
							OwnerPid: func(v int64) *int64 { return &v }(12345),
						},
					},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId:      daemonID,
					Session:       &sessiondv1.SessionRef{SessionId: "session-1", AgentId: strPtr("agent-hermes")},
					SeqId:         4,
					EventType:     "hermes.command_complete",
					NativePayload: makeNativePayload(cmdCompletePayload),
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId:      daemonID,
					Session:       &sessiondv1.SessionRef{SessionId: "session-1", AgentId: strPtr("agent-hermes")},
					SeqId:         5,
					EventType:     "token.usage",
					NativePayload: makeNativePayload(tokenPayload),
				},
			},
		},
	}

	fake := &fakeEventsServer{
		daemonID: daemonID,
		frames:   frames,
	}
	addr, stopServer := startFakeEventsServer(t, fake)
	defer stopServer()

	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         addr,
		Credential:     "test-credential",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer client.Close()

	pub := &testPublisher{}
	proj, err := projection.New(client, st, pub)
	if err != nil {
		t.Fatalf("new projection: %v", err)
	}

	runCtx, runCancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- proj.Run(runCtx)
	}()

	// Wait for frames to be projected
	for i := 0; i < 50; i++ {
		cp, _ := st.GetCheckpoint(ctx, daemonID, "session-1")
		if cp == 5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cp, err := st.GetCheckpoint(ctx, daemonID, "session-1")
	if err != nil || cp != 5 {
		t.Fatalf("expected checkpoint 5, got %d (err: %v)", cp, err)
	}

	// Verify agent was upserted in store
	agent, err := st.GetAgent(ctx, "agent-hermes")
	if err != nil || agent == nil || agent["name"] != "Hermes Agent" {
		t.Fatalf("agent not upserted properly: %+v (err: %v)", agent, err)
	}

	// Verify session was upserted
	sess, err := st.GetSession(ctx, "agent-hermes", "session-1")
	if err != nil || sess == nil {
		t.Fatalf("session not upserted properly: %+v (err: %v)", sess, err)
	}
	if sess["status"] != "idle" { // hermes.command_complete updates status to idle
		t.Fatalf("expected session status idle, got %v", sess["status"])
	}

	// Verify token metrics
	metrics, err := st.ListTokenMetrics(ctx, "agent-hermes", "session-1")
	if err != nil || len(metrics) != 1 || metrics[0]["total_tokens"].(int64) != 400 {
		t.Fatalf("token metrics not projected properly: %+v (err: %v)", metrics, err)
	}

	// Verify publisher received committed events
	if pub.count() < 4 {
		t.Fatalf("expected published events, got %d", pub.count())
	}

	runCancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("run returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not exit on context cancel")
	}
}

func TestProjectionUnknownEventFailsAndDoesNotAdvanceCheckpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "unknown_test.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	daemonID := "daemon-epoch-2"

	frames := []*sessiondv1.EventStreamFrame{
		{
			Frame: &sessiondv1.EventStreamFrame_Snapshot{
				Snapshot: &sessiondv1.StateSnapshot{
					DaemonId: daemonID,
					Sessions: []*sessiondv1.SessionSnapshot{},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_LiveReady{
				LiveReady: &sessiondv1.LiveReady{
					DaemonId:   daemonID,
					Watermarks: []*sessiondv1.SessionWatermark{},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId:      daemonID,
					Session:       &sessiondv1.SessionRef{SessionId: "session-unknown", AgentId: strPtr("agent-a")},
					SeqId:         1,
					EventType:     "completely.unknown.event.type",
					NativePayload: makeNativePayload(map[string]any{"some": "data"}),
				},
			},
		},
	}

	fake := &fakeEventsServer{
		daemonID: daemonID,
		frames:   frames,
	}
	addr, stopServer := startFakeEventsServer(t, fake)
	defer stopServer()

	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         addr,
		Credential:     "cred",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	pub := &testPublisher{}
	proj, err := projection.New(client, st, pub)
	if err != nil {
		t.Fatalf("new projection: %v", err)
	}

	err = proj.Run(ctx)
	if err == nil || !projection.IsFatalProjectionError(err) {
		t.Fatalf("expected fatal unknown event error, got: %v", err)
	}

	cp, _ := st.GetCheckpoint(ctx, daemonID, "session-unknown")
	if cp != 0 {
		t.Fatalf("checkpoint must NOT advance on unknown event, got %d", cp)
	}
}

func TestProjectionNonContiguousSequenceFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "gap_test.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	daemonID := "daemon-epoch-gap"

	frames := []*sessiondv1.EventStreamFrame{
		{
			Frame: &sessiondv1.EventStreamFrame_Snapshot{
				Snapshot: &sessiondv1.StateSnapshot{
					DaemonId: daemonID,
					Sessions: []*sessiondv1.SessionSnapshot{},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_LiveReady{
				LiveReady: &sessiondv1.LiveReady{
					DaemonId:   daemonID,
					Watermarks: []*sessiondv1.SessionWatermark{},
				},
			},
		},
		{
			Frame: &sessiondv1.EventStreamFrame_Live{
				Live: &sessiondv1.EventEnvelope{
					DaemonId:      daemonID,
					Session:       &sessiondv1.SessionRef{SessionId: "session-gap", AgentId: strPtr("agent-a")},
					SeqId:         2, // Gap: expected 1!
					EventType:     "ping",
					NativePayload: makeNativePayload(map[string]any{}),
				},
			},
		},
	}

	fake := &fakeEventsServer{
		daemonID: daemonID,
		frames:   frames,
	}
	addr, stopServer := startFakeEventsServer(t, fake)
	defer stopServer()

	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         addr,
		Credential:     "cred",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	pub := &testPublisher{}
	proj, err := projection.New(client, st, pub)
	if err != nil {
		t.Fatalf("new projection: %v", err)
	}

	err = proj.Run(ctx)
	if err == nil || !projection.IsFatalProjectionError(err) {
		t.Fatalf("expected non-contiguous error, got: %v", err)
	}

	cp, _ := st.GetCheckpoint(ctx, daemonID, "session-gap")
	if cp != 0 {
		t.Fatalf("checkpoint must NOT advance on gap, got %d", cp)
	}
}
