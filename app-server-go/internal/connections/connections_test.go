package connections_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type dummyPublisher struct {
	events []string
}

func (d *dummyPublisher) Publish(_ context.Context, eventType string, _ map[string]any) error {
	d.events = append(d.events, eventType)
	return nil
}

type fakeDaemonService struct {
	sessiondv1.UnimplementedDaemonControlServer
	sessiondv1.UnimplementedSessionControlServer
	sessiondv1.UnimplementedRuntimeQueryServer
	sessiondv1.UnimplementedOperationQueryServer
	sessiondv1.UnimplementedModelConfigServer
	sessiondv1.UnimplementedEventStreamServer
	sessiondv1.UnimplementedTerminalStreamServer
	sessiondv1.UnimplementedConnectorIngressServer

	token              string
	disconnectHit      atomic.Int32
	attachHit          atomic.Int32
	attachRequest      atomic.Pointer[sessiondv1.AttachSessionRequest]
	statusResponse     atomic.Pointer[sessiondv1.GetStatusResponse]
	disconnectRequest  atomic.Pointer[sessiondv1.DisconnectRuntimeRequest]
	disconnectResponse *sessiondv1.DisconnectRuntimeResponse
	disconnectError    error
	attachResponse     *sessiondv1.SessionReceipt
	attachNilReceipt   bool
	attachError        error
	attachRequestsMu   sync.Mutex
	attachRequests     []*sessiondv1.AttachSessionRequest
}

func (s *fakeDaemonService) checkAuth(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "metadata missing")
	}
	vals := md.Get(daemonclient.AuthorizationMetadataKey)
	if len(vals) == 0 || vals[0] != "Bearer "+s.token {
		return status.Error(codes.Unauthenticated, "unauthorized")
	}
	return nil
}

func (s *fakeDaemonService) GetCapabilities(ctx context.Context, _ *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor: 1,
		ProtocolMinor: 0,
		DaemonId:      "daemon-test-123",
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
			sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_ASTRORDER_OPERATION_V1,
		},
		MaxWireBytes:       sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		MaxBusinessBytes:   sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		EventStreamVersion: sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:    true,
	}, nil
}

func (s *fakeDaemonService) GetStatus(ctx context.Context, _ *sessiondv1.GetStatusRequest) (*sessiondv1.GetStatusResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if response := s.statusResponse.Load(); response != nil {
		return response, nil
	}
	return &sessiondv1.GetStatusResponse{
		DaemonId: "daemon-test-123",
		Runtimes: []*sessiondv1.RuntimeStatus{
			{
				Runtime:    sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX,
				Registered: true,
			},
		},
	}, nil
}

func (s *fakeDaemonService) DisconnectRuntime(ctx context.Context, r *sessiondv1.DisconnectRuntimeRequest) (*sessiondv1.DisconnectRuntimeResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if r.Meta == nil || r.Meta.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "durable operation identity is required")
	}
	s.disconnectHit.Add(1)
	s.disconnectRequest.Store(r)
	if s.disconnectError != nil {
		return nil, s.disconnectError
	}
	if s.disconnectResponse != nil {
		return s.disconnectResponse, nil
	}
	return &sessiondv1.DisconnectRuntimeResponse{
		DaemonId:       "daemon-test-123",
		Disconnected:   true,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
	}, nil
}

func (s *fakeDaemonService) AttachSession(ctx context.Context, r *sessiondv1.AttachSessionRequest) (*sessiondv1.SessionReceipt, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	s.attachHit.Add(1)
	s.attachRequest.Store(r)
	s.attachRequestsMu.Lock()
	s.attachRequests = append(s.attachRequests, r)
	s.attachRequestsMu.Unlock()
	if s.attachError != nil {
		return nil, s.attachError
	}
	if s.attachNilReceipt {
		return nil, nil
	}
	if s.attachResponse != nil {
		return s.attachResponse, nil
	}
	// The real daemon always confirms a concrete session status on success;
	// an unconfirmed (uncertain) outcome carries no status.
	return &sessiondv1.SessionReceipt{
		DaemonId: "daemon-test-123",
		Session:  r.Session,
		Status:   sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
		Attached: true,
	}, nil
}

func startFakeDaemon(t *testing.T, srv *fakeDaemonService) (string, func()) {
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

func TestConnectionsHandlerMatches(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/api/v1/agents", true},
		{http.MethodPost, "/api/v1/agents", false},
		{http.MethodGet, "/api/v1/agents/upgrade/targets", true},
		{http.MethodGet, "/api/v1/agents/codex/observations", true},
		{http.MethodPost, "/api/v1/agents/codex/upgrade", true},
		{http.MethodGet, "/api/v1/connections", true},
		{http.MethodGet, "/api/v1/environments", true},
		{http.MethodPost, "/api/v1/environments/local/discover", true},
		{http.MethodPost, "/api/v1/environments/local/agents/codex/disconnect", true},
		{http.MethodPut, "/api/v1/connections/ssh", true},
		{http.MethodGet, "/api/v1/unrelated", false},
	}
	for _, tc := range cases {
		if got := connections.Matches(tc.method, tc.path); got != tc.want {
			t.Errorf("Matches(%s, %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestConnectionsCRUDAndActions(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "conn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	secret := "test-secret"
	daemonSrv := &fakeDaemonService{token: secret}
	addr, daemonCleanup := startFakeDaemon(t, daemonSrv)
	defer daemonCleanup()

	client, err := daemonclient.Dial(context.Background(), daemonclient.Config{
		Target:         addr,
		Credential:     secret,
		RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer client.Close()

	pub := &dummyPublisher{}
	handler, err := connections.NewHandler(connections.Options{
		Store:     st,
		Client:    client,
		Publisher: pub,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Save SSH connection
	saveBody := `{"id":"ssh-test-1","display_name":"Test Server","host":"10.0.0.1","port":22,"user":"ubuntu"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/connections/ssh", strings.NewReader(saveBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save SSH connection: %d %s", w.Code, w.Body.String())
	}

	// 2. List connections snapshot
	req = httptest.NewRequest(http.MethodGet, "/api/v1/connections", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get connections snapshot: %d %s", w.Code, w.Body.String())
	}
	var snap map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	ssh, _ := snap["ssh"].(map[string]any)
	sshList, _ := ssh["items"].([]any)
	if len(sshList) != 1 {
		t.Fatalf("expected 1 ssh connection in snapshot, got %#v", snap)
	}

	// 3. Connect SSH connection (calls daemon AttachSession)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/connections/ssh/ssh-test-1/connect", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("connect ssh: %d %s", w.Code, w.Body.String())
	}
	if daemonSrv.attachHit.Load() != 1 {
		t.Fatalf("expected attachHit 1, got %d", daemonSrv.attachHit.Load())
	}

	// 4. Disconnect SSH connection (calls daemon DisconnectRuntime)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/connections/ssh/ssh-test-1/disconnect", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disconnect ssh: %d %s", w.Code, w.Body.String())
	}
	if daemonSrv.disconnectHit.Load() != 1 {
		t.Fatalf("expected disconnectHit 1, got %d", daemonSrv.disconnectHit.Load())
	}

	// 5. Delete SSH connection (calls daemon DisconnectRuntime again)
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/connections/ssh/ssh-test-1", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete ssh: %d %s", w.Code, w.Body.String())
	}
	if daemonSrv.disconnectHit.Load() != 2 {
		t.Fatalf("expected disconnectHit 2, got %d", daemonSrv.disconnectHit.Load())
	}

	// 6. Delete again returns 404
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/connections/ssh/ssh-test-1", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for deleted ssh, got %d", w.Code)
	}
}

type fakeVersionExecutor struct {
	versions map[string]string
	upToDate map[string]bool
}

func (f *fakeVersionExecutor) GetVersion(_ context.Context, kind, connID string, _ map[string]any) (string, bool, error) {
	key := kind + ":" + connID
	if v, ok := f.versions[key]; ok {
		return v, f.upToDate[key], nil
	}
	return "0.1.0", false, nil
}

func TestNilClientReturnsUnavailable(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "nilclient.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	handler, err := connections.NewHandler(connections.Options{
		Store:  st,
		Client: nil, // explicitly nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Connect local -> 503
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/local/connect", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for connect local with nil client, got %d", w.Code)
	}

	// 2. Disconnect local -> 503
	req = httptest.NewRequest(http.MethodPost, "/api/v1/connections/local/disconnect", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for disconnect local with nil client, got %d", w.Code)
	}

	// 3. Environment agent connect/disconnect/restart -> 503
	for _, action := range []string{"connect", "disconnect", "restart"} {
		req = httptest.NewRequest(http.MethodPost, "/api/v1/environments/local/agents/codex/"+action, nil)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 for agent %s with nil client, got %d", action, w.Code)
		}
	}
}

func TestAgentUpgradeTargetsWithVersionExecutor(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Seed agent
	_, err = st.UpsertAgent(context.Background(), map[string]any{
		"id":            "agent-codex-1",
		"name":          "Codex Local",
		"kind":          "codex",
		"status":        "ready",
		"connection_id": "local",
		"capabilities":  []string{},
		"control_state": "ready",
	})
	if err != nil {
		t.Fatal(err)
	}

	exec := &fakeVersionExecutor{
		versions: map[string]string{"codex:local": "0.115.0"},
		upToDate: map[string]bool{"codex:local": true},
	}
	handler, err := connections.NewHandler(connections.Options{
		Store:           st,
		VersionExecutor: exec,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/upgrade/targets", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	items, _ := res["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %#v", items)
	}
	item := items[0].(map[string]any)
	if item["current_version"] != "0.115.0" || item["has_update"] != false {
		t.Fatalf("expected 0.115.0 up to date, got: %#v", item)
	}
}
