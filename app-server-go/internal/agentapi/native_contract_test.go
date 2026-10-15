package agentapi

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type testPublisher struct {
	events []string
}

func (p *testPublisher) Publish(ctx context.Context, eventType string, data map[string]any) error {
	p.events = append(p.events, eventType)
	return nil
}

type mockSessionControlServer struct {
	sessiondv1.UnimplementedSessionControlServer
	createResp    *sessiondv1.CommandReceipt
	createErr     error
	sendResp      *sessiondv1.CommandReceipt
	sendErr       error
	sendCalls     int
	lastSendReq   *sessiondv1.SendRequest
	deleteResp    *sessiondv1.CommandReceipt
	deleteErr     error
	deleteCalls   int
	interruptResp *sessiondv1.CommandReceipt
	interruptErr  error
}

func (m *mockSessionControlServer) CreateSession(ctx context.Context, req *sessiondv1.CreateSessionRequest) (*sessiondv1.CommandReceipt, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.createResp, nil
}

func (m *mockSessionControlServer) Send(ctx context.Context, req *sessiondv1.SendRequest) (*sessiondv1.CommandReceipt, error) {
	m.sendCalls++
	m.lastSendReq = req
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	return m.sendResp, nil
}

func (m *mockSessionControlServer) DeleteSession(ctx context.Context, req *sessiondv1.DeleteSessionRequest) (*sessiondv1.CommandReceipt, error) {
	m.deleteCalls++
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return m.deleteResp, nil
}

func (m *mockSessionControlServer) Interrupt(ctx context.Context, req *sessiondv1.InterruptRequest) (*sessiondv1.CommandReceipt, error) {
	if m.interruptErr != nil {
		return nil, m.interruptErr
	}
	return m.interruptResp, nil
}

func setupMockGRPC(t *testing.T, srv *mockSessionControlServer) (*daemonclient.Client, func()) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	sessiondv1.RegisterSessionControlServer(s, srv)
	sessiondv1.RegisterRuntimeQueryServer(s, nativeHistoryFixture{})
	go s.Serve(l)

	conn, err := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		s.Stop()
		l.Close()
		t.Fatal(err)
	}

	cli := &daemonclient.Client{
		Session: sessiondv1.NewSessionControlClient(conn),
		Runtime: sessiondv1.NewRuntimeQueryClient(conn),
	}

	cleanup := func() {
		conn.Close()
		s.Stop()
		l.Close()
	}
	return cli, cleanup
}

func TestNativeDeleteNilClientNeverDeletesAppStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agent-1", "kind": "codex", "name": "Agent 1"})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":         "sess-1",
		"agent_id":   "agent-1",
		"title":      "Test",
		"workspace":  "P:/ws",
		"status":     "idle",
		"updated_at": "2026-01-01T00:00:00Z",
	})

	h := &Handler{
		options: Options{
			Store:  st,
			Client: nil, // nil client
		},
	}

	res, err := h.invokeSessionsDelete(ctx, map[string]any{
		"agent_id":   "agent-1",
		"session_id": "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if ok, _ := m["ok"].(bool); ok {
		t.Fatalf("expected deletion to fail when client is nil")
	}

	// Verify session is STILL in app store
	sess, err := st.GetSession(ctx, "agent-1", "sess-1")
	if err != nil || sess == nil {
		t.Fatalf("session was deleted from app store despite nil daemon client: %v", sess)
	}
}

func TestNativeCreateForeignAgentReceiptFails(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agent-local", "kind": "codex", "name": "Local Agent"})

	foreignAgentID := "foreign-agent"
	mockSrv := &mockSessionControlServer{
		createResp: &sessiondv1.CommandReceipt{
			Accepted:       true,
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Status:         sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
			Session: &sessiondv1.SessionRef{
				SessionId: "sess-created",
				AgentId:   &foreignAgentID, // Foreign agent returned!
			},
		},
	}
	client, cleanup := setupMockGRPC(t, mockSrv)
	defer cleanup()

	h := &Handler{
		options: Options{
			Store:            st,
			Client:           client,
			DefaultWorkspace: "P:/ws",
		},
	}

	_, err = h.invokeSessionsCreate(ctx, map[string]any{
		"agent_id": "agent-local",
	})
	if err == nil {
		t.Fatalf("expected foreign agent receipt to fail invokeSessionsCreate")
	}
}

func TestNativeSendSameCommandIDTwiceInvokesDaemonOnlyOnce(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agent-1", "kind": "codex", "name": "Local Agent"})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":         "sess-1",
		"agent_id":   "agent-1",
		"title":      "Test",
		"workspace":  "P:/ws",
		"status":     "idle",
		"updated_at": "2026-01-01T00:00:00Z",
	})

	mockSrv := &mockSessionControlServer{
		sendResp: &sessiondv1.CommandReceipt{
			Accepted:       true,
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Status:         sessiondv1.SessionStatus_SESSION_STATUS_RUNNING,
		},
	}
	client, cleanup := setupMockGRPC(t, mockSrv)
	defer cleanup()

	pub := &testPublisher{}
	h := &Handler{
		options: Options{
			Store:     st,
			Client:    client,
			Publisher: pub,
		},
	}

	fixedCmdID := "fixed-cmd-123"
	// First call
	res1, err := h.invokeSessionsSend(ctx, map[string]any{
		"agent_id":   "agent-1",
		"session_id": "sess-1",
		"command_id": fixedCmdID,
		"text":       "hello 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	m1 := res1.(map[string]any)
	if m1["state"] != "running" {
		t.Fatalf("expected state running, got %v", m1["state"])
	}
	if mockSrv.sendCalls != 1 {
		t.Fatalf("expected 1 send call, got %d", mockSrv.sendCalls)
	}

	// Second call with same command_id
	res2, err := h.invokeSessionsSend(ctx, map[string]any{
		"agent_id":   "agent-1",
		"session_id": "sess-1",
		"command_id": fixedCmdID,
		"text":       "hello 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	m2 := res2.(map[string]any)
	if m2["state"] != "running" {
		t.Fatalf("expected existing state running, got %v", m2["state"])
	}
	// Daemon MUST still have been called only once
	if mockSrv.sendCalls != 1 {
		t.Fatalf("expected daemon called only once, but got %d", mockSrv.sendCalls)
	}
}

func TestNativeSendCompletedOperationStateMeansAcceptedNotTurnCompleted(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agent-1", "kind": "codex", "name": "Local Agent"})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":         "sess-1",
		"agent_id":   "agent-1",
		"title":      "Test",
		"workspace":  "P:/ws",
		"status":     "idle",
		"updated_at": "2026-01-01T00:00:00Z",
	})

	// Daemon sends COMPLETED receipt, but session is not yet running (e.g. idle or starting)
	mockSrv := &mockSessionControlServer{
		sendResp: &sessiondv1.CommandReceipt{
			Accepted:       true,
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Status:         sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
		},
	}
	client, cleanup := setupMockGRPC(t, mockSrv)
	defer cleanup()

	pub := &testPublisher{}
	h := &Handler{
		options: Options{
			Store:     st,
			Client:    client,
			Publisher: pub,
		},
	}

	res, err := h.invokeSessionsSend(ctx, map[string]any{
		"agent_id":   "agent-1",
		"session_id": "sess-1",
		"text":       "start task",
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	// Must be accepted (operation submission accepted), NOT completed turn!
	if m["state"] != "accepted" {
		t.Fatalf("expected state accepted (not completed), got %v", m["state"])
	}
}

func TestNativeSharedCommandSubmitCallback(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agent-1", "kind": "codex", "name": "Local Agent"})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":         "sess-1",
		"agent_id":   "agent-1",
		"title":      "Test",
		"workspace":  "P:/ws",
		"status":     "idle",
		"updated_at": "2026-01-01T00:00:00Z",
	})

	var submittedActions []string
	submitFn := func(ctx context.Context, p map[string]any) (map[string]any, error) {
		action, _ := p["action"].(string)
		submittedActions = append(submittedActions, action)
		return map[string]any{
			"id":    p["id"],
			"state": "running",
		}, nil
	}

	h := &Handler{
		options: Options{
			Store:         st,
			CommandSubmit: submitFn,
		},
	}

	resSend, err := h.invokeSessionsSend(ctx, map[string]any{
		"agent_id":   "agent-1",
		"session_id": "sess-1",
		"command_id": "custom-cmd-1",
		"text":       "run command",
	})
	if err != nil {
		t.Fatal(err)
	}
	mSend := resSend.(map[string]any)
	if mSend["command_id"] != "custom-cmd-1" || mSend["state"] != "running" {
		t.Fatalf("expected custom-cmd-1 with running state, got: %v", mSend)
	}

	resStop, err := h.invokeSessionsStop(ctx, map[string]any{
		"agent_id":   "agent-1",
		"session_id": "sess-1",
		"command_id": "custom-cmd-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	mStop := resStop.(map[string]any)
	if mStop["command_id"] != "custom-cmd-2" || mStop["state"] != "running" {
		t.Fatalf("expected custom-cmd-2 with running state, got: %v", mStop)
	}

	if len(submittedActions) != 2 || submittedActions[0] != "send" || submittedActions[1] != "stop" {
		t.Fatalf("expected actions [send, stop], got: %v", submittedActions)
	}
}

func TestNativeStopCodexUsesActiveTurnFromStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agent-1", "kind": "codex", "name": "Local Agent"})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":         "sess-1",
		"agent_id":   "agent-1",
		"title":      "Test",
		"workspace":  "P:/ws",
		"status":     "running",
		"updated_at": "2026-01-01T00:00:00Z",
	})

	_ = st.SetActiveTurn(ctx, "agent-1", "sess-1", "turn-authoritative-99")

	var receivedTurnID string
	mockSrv := &mockSessionControlServer{
		interruptResp: &sessiondv1.CommandReceipt{
			Accepted:       true,
			OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		},
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	sessiondv1.RegisterSessionControlServer(s, &interceptServer{
		mock: mockSrv,
		onInterrupt: func(r *sessiondv1.InterruptRequest) {
			if r.TurnId != nil {
				receivedTurnID = *r.TurnId
			}
		},
	})
	go s.Serve(l)
	defer func() {
		s.Stop()
		l.Close()
	}()

	conn, err := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	cli := &daemonclient.Client{Session: sessiondv1.NewSessionControlClient(conn)}
	h := &Handler{
		options: Options{
			Store:  st,
			Client: cli,
		},
	}

	res, err := h.invokeSessionsStop(ctx, map[string]any{
		"agent_id":   "agent-1",
		"session_id": "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["state"] != "completed" {
		t.Fatalf("expected state completed, got %v", m["state"])
	}
	if receivedTurnID != "turn-authoritative-99" {
		t.Fatalf("expected turn-authoritative-99, got %s", receivedTurnID)
	}
}

type interceptServer struct {
	sessiondv1.UnimplementedSessionControlServer
	mock        *mockSessionControlServer
	onInterrupt func(r *sessiondv1.InterruptRequest)
}

func (i *interceptServer) Interrupt(ctx context.Context, req *sessiondv1.InterruptRequest) (*sessiondv1.CommandReceipt, error) {
	if i.onInterrupt != nil {
		i.onInterrupt(req)
	}
	return i.mock.Interrupt(ctx, req)
}

func TestAgentApiResolveRuntimeTargetRemoteDefaultsAndOverrides(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.SaveSSHConnection(ctx, map[string]any{
		"id":   "remote-1",
		"host": "192.168.1.100",
		"port": json.Number("22"),
		"user": "luwei",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{options: Options{Store: st}}

	// default codex
	codexTarget, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "c1", "kind": "codex", "connection_id": "remote-1"})
	if err != nil {
		t.Fatal(err)
	}
	if codexTarget.Ssh.GetCodexExecutable() != "/home/luwei/.vite-plus/bin/codex" {
		t.Fatalf("unexpected codex default: %v", codexTarget.Ssh.GetCodexExecutable())
	}

	// default grok
	grokTarget, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "g1", "kind": "grok", "connection_id": "remote-1"})
	if err != nil {
		t.Fatal(err)
	}
	if grokTarget.Ssh.GetGrokExecutable() != "/home/luwei/.local/bin/grok" {
		t.Fatalf("unexpected grok default: %v", grokTarget.Ssh.GetGrokExecutable())
	}

	// default hermes
	hermesTarget, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "h1", "kind": "hermes", "connection_id": "remote-1"})
	if err != nil {
		t.Fatal(err)
	}
	if hermesTarget.Ssh.GetHermesPath() != "/home/luwei/.local/bin/hermes" {
		t.Fatalf("unexpected hermes default: %v", hermesTarget.Ssh.GetHermesPath())
	}

	// with custom hermes override
	_, err = st.SaveSSHConnection(ctx, map[string]any{
		"id":          "remote-custom",
		"host":        "192.168.1.100",
		"port":        json.Number("22"),
		"user":        "luwei",
		"hermes_path": "/custom/bin/hermes",
	})
	if err != nil {
		t.Fatal(err)
	}
	hTarget, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "h2", "kind": "hermes", "connection_id": "remote-custom"})
	if err != nil {
		t.Fatal(err)
	}
	if hTarget.Ssh.GetHermesPath() != "/custom/bin/hermes" {
		t.Fatalf("unexpected custom hermes: %v", hTarget.Ssh.GetHermesPath())
	}
}
