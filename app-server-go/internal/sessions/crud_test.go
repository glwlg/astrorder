package sessions

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type mockDaemonServer struct {
	sessiondv1.UnimplementedDaemonControlServer
	sessiondv1.UnimplementedSessionControlServer
	sessiondv1.UnimplementedRuntimeQueryServer
	sessiondv1.UnimplementedEventStreamServer
	eventsCb         func(*sessiondv1.SubscribeRequest, grpc.ServerStreamingServer[sessiondv1.EventStreamFrame]) error
	backgroundCb     func(*sessiondv1.ListBackgroundProcessesRequest) (*sessiondv1.ListBackgroundProcessesResponse, error)
	createCb         func(*sessiondv1.CreateSessionRequest) (*sessiondv1.CommandReceipt, error)
	renameCb         func(*sessiondv1.RenameSessionRequest) (*sessiondv1.CommandReceipt, error)
	deleteCb         func(*sessiondv1.DeleteSessionRequest) (*sessiondv1.CommandReceipt, error)
	readModelCb      func(*sessiondv1.ReadModelRequest) (*sessiondv1.ModelState, error)
	attachCb         func(*sessiondv1.AttachSessionRequest) (*sessiondv1.SessionReceipt, error)
	getHistoryPageCb func(*sessiondv1.HistoryPageRequest) (*sessiondv1.HistoryPageResponse, error)
	sendCb           func(*sessiondv1.SendRequest) (*sessiondv1.CommandReceipt, error)
	statusCb         func() (*sessiondv1.GetStatusResponse, error)
	protocolMinor    uint32
	closeCb          func(*sessiondv1.CloseSessionRequest) (*sessiondv1.CommandReceipt, error)
	interruptCb      func(*sessiondv1.InterruptRequest) (*sessiondv1.CommandReceipt, error)
	queryCb          func(*sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error)
}

func (m *mockDaemonServer) QueryNative(_ context.Context, r *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
	if m.queryCb != nil {
		return m.queryCb(r)
	}
	return nil, fmt.Errorf("native query fixture is not configured")
}

func (m *mockDaemonServer) CloseSession(ctx context.Context, r *sessiondv1.CloseSessionRequest) (*sessiondv1.CommandReceipt, error) {
	if m.closeCb != nil {
		return m.closeCb(r)
	}
	return nil, fmt.Errorf("close fixture is not configured")
}
func (m *mockDaemonServer) Interrupt(ctx context.Context, r *sessiondv1.InterruptRequest) (*sessiondv1.CommandReceipt, error) {
	if m.interruptCb != nil {
		return m.interruptCb(r)
	}
	return nil, fmt.Errorf("interrupt fixture is not configured")
}

func (m *mockDaemonServer) GetStatus(context.Context, *sessiondv1.GetStatusRequest) (*sessiondv1.GetStatusResponse, error) {
	if m.statusCb != nil {
		return m.statusCb()
	}
	return nil, fmt.Errorf("status fixture is not configured")
}

func (m *mockDaemonServer) Subscribe(r *sessiondv1.SubscribeRequest, stream grpc.ServerStreamingServer[sessiondv1.EventStreamFrame]) error {
	if m.eventsCb != nil {
		return m.eventsCb(r, stream)
	}
	return fmt.Errorf("event fixture is not configured")
}

func (m *mockDaemonServer) ReadModel(ctx context.Context, req *sessiondv1.ReadModelRequest) (*sessiondv1.ModelState, error) {
	if m.readModelCb != nil {
		return m.readModelCb(req)
	}
	return &sessiondv1.ModelState{}, nil
}

func (m *mockDaemonServer) AttachSession(ctx context.Context, req *sessiondv1.AttachSessionRequest) (*sessiondv1.SessionReceipt, error) {
	if m.attachCb != nil {
		return m.attachCb(req)
	}
	return &sessiondv1.SessionReceipt{Attached: true, Session: req.Session, Status: sessiondv1.SessionStatus_SESSION_STATUS_IDLE}, nil
}

func (m *mockDaemonServer) GetCapabilities(ctx context.Context, req *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor:            1,
		ProtocolMinor:            m.protocolMinor,
		DaemonId:                 "test-daemon",
		Services:                 []string{"astrorder.sessiond.v1.DaemonControl", "astrorder.sessiond.v1.SessionControl", "astrorder.sessiond.v1.RuntimeQuery", "astrorder.sessiond.v1.OperationQuery", "astrorder.sessiond.v1.ModelConfig", "astrorder.sessiond.v1.EventStream", "astrorder.sessiond.v1.TerminalStream", "astrorder.sessiond.v1.ConnectorIngress"},
		CanonicalizationVersions: []sessiondv1.CanonicalizationVersion{sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1},
		MaxBusinessBytes:         sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		MaxWireBytes:             sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		EventStreamVersion:       sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:          true,
	}, nil
}

func (m *mockDaemonServer) CreateSession(ctx context.Context, req *sessiondv1.CreateSessionRequest) (*sessiondv1.CommandReceipt, error) {
	if m.createCb != nil {
		return m.createCb(req)
	}
	return &sessiondv1.CommandReceipt{
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Session:        &sessiondv1.SessionRef{SessionId: "s-created", AgentId: req.Target.AgentId},
		Status:         sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
	}, nil
}

func (m *mockDaemonServer) RenameSession(ctx context.Context, req *sessiondv1.RenameSessionRequest) (*sessiondv1.CommandReceipt, error) {
	if m.renameCb != nil {
		return m.renameCb(req)
	}
	return &sessiondv1.CommandReceipt{
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
	}, nil
}

func (m *mockDaemonServer) DeleteSession(ctx context.Context, req *sessiondv1.DeleteSessionRequest) (*sessiondv1.CommandReceipt, error) {
	if m.deleteCb != nil {
		return m.deleteCb(req)
	}
	return &sessiondv1.CommandReceipt{
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
	}, nil
}

type mockPublisher struct {
	events []map[string]any
}

func (p *mockPublisher) Publish(ctx context.Context, eventType string, data map[string]any) error {
	p.events = append(p.events, map[string]any{"type": eventType, "data": data})
	return nil
}

func setupTestServer(t *testing.T, srv *mockDaemonServer) (*Handler, *store.Store, *daemonclient.Client, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(grpcSrv, srv)
	sessiondv1.RegisterSessionControlServer(grpcSrv, srv)
	sessiondv1.RegisterRuntimeQueryServer(grpcSrv, srv)
	sessiondv1.RegisterEventStreamServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()

	ctx := context.Background()
	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         lis.Addr().String(),
		Credential:     "test-secret",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		grpcSrv.Stop()
		t.Fatalf("dial: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		grpcSrv.Stop()
		t.Fatalf("store.Open: %v", err)
	}

	authorizer := auth.New(config.Config{BrowserSecret: "browser-sec"}, auth.OriginLookupFunc(func(ctx context.Context) (auth.OriginPreferences, error) {
		return auth.OriginPreferences{AllowedOrigins: []string{"http://localhost"}}, nil
	}))

	pub := &mockPublisher{}
	h, err := NewHandler(Options{
		Store:            st,
		Client:           client,
		Authorizer:       authorizer,
		Publisher:        pub,
		DefaultWorkspace: "C:/work",
	})
	if err != nil {
		st.Close()
		grpcSrv.Stop()
		t.Fatalf("NewHandler: %v", err)
	}

	cleanup := func() {
		client.Close()
		grpcSrv.Stop()
		_ = lis.Close()
		st.Close()
	}
	return h.(*Handler), st, client, cleanup
}

func TestPresenceStatusRejectedInUpdateSession(t *testing.T) {
	srv := &mockDaemonServer{}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()

	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "ag-1", "kind": "codex", "name": "Codex", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id": "s-1", "agent_id": "ag-1", "title": "Old", "status": "idle", "updated_at": "2026-10-09T00:00:00Z",
	})

	// Direct browser attempt to set status must be rejected with 422
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/s-1", strings.NewReader(`{"agent_id":"ag-1","status":"running"}`))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for status modification, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestNilClientReturns503OnRenameAndDelete(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "ag-1", "kind": "codex", "name": "Codex", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id": "s-1", "agent_id": "ag-1", "title": "Old", "status": "idle", "updated_at": "2026-10-09T00:00:00Z",
	})

	authorizer := auth.New(config.Config{BrowserSecret: "browser-sec"}, nil)
	h, _ := NewHandler(Options{
		Store:      st,
		Client:     nil, // Nil client
		Authorizer: authorizer,
		Publisher:  &mockPublisher{},
	})

	// Rename with nil client must be 503
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/s-1", strings.NewReader(`{"agent_id":"ag-1","title":"New"}`))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on rename with nil client, got %d: %s", rec.Code, rec.Body.String())
	}

	// Delete with nil client must be 503
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/s-1?agent_id=ag-1", nil)
	reqDel.Header.Set("Authorization", "Bearer browser-sec")
	recDel := httptest.NewRecorder()
	h.ServeHTTP(recDel, reqDel)
	if recDel.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on delete with nil client, got %d: %s", recDel.Code, recDel.Body.String())
	}
}
