package sessions

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// refreshDaemonServer is an independent gRPC fixture for the session refresh
// routes. It mirrors the capabilities contract of the shared fixture but
// exposes a configurable GetStatus so tests exercise the real client/server
// ownership boundary instead of fabricated idle state.
type refreshDaemonServer struct {
	sessiondv1.UnimplementedDaemonControlServer
	sessiondv1.UnimplementedSessionControlServer
	sessiondv1.UnimplementedRuntimeQueryServer
	statusCb func() (*sessiondv1.GetStatusResponse, error)
}

func (m *refreshDaemonServer) GetCapabilities(context.Context, *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor: 1,
		ProtocolMinor: 0,
		DaemonId:      "test-daemon",
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
		CanonicalizationVersions: []sessiondv1.CanonicalizationVersion{sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1},
		MaxBusinessBytes:         sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		MaxWireBytes:             sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		EventStreamVersion:       sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:          true,
	}, nil
}

func (m *refreshDaemonServer) GetStatus(context.Context, *sessiondv1.GetStatusRequest) (*sessiondv1.GetStatusResponse, error) {
	if m.statusCb != nil {
		return m.statusCb()
	}
	return &sessiondv1.GetStatusResponse{DaemonId: "test-daemon"}, nil
}

func setupRefreshServer(t *testing.T, srv *refreshDaemonServer) (*Handler, *store.Store, *mockPublisher, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(grpcSrv, srv)
	sessiondv1.RegisterSessionControlServer(grpcSrv, srv)
	sessiondv1.RegisterRuntimeQueryServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()

	client, err := daemonclient.Dial(context.Background(), daemonclient.Config{
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
		client.Close()
		grpcSrv.Stop()
		t.Fatalf("store.Open: %v", err)
	}
	authorizer := auth.New(config.Config{BrowserSecret: "browser-sec"}, auth.OriginLookupFunc(func(ctx context.Context) (auth.OriginPreferences, error) {
		return auth.OriginPreferences{AllowedOrigins: []string{"http://localhost"}}, nil
	}))
	pub := &mockPublisher{}
	handler, err := NewHandler(Options{Store: st, Client: client, Authorizer: authorizer, Publisher: pub})
	if err != nil {
		st.Close()
		client.Close()
		grpcSrv.Stop()
		t.Fatalf("NewHandler: %v", err)
	}
	cleanup := func() {
		client.Close()
		grpcSrv.Stop()
		_ = lis.Close()
		st.Close()
	}
	return handler.(*Handler), st, pub, cleanup
}

func refreshCall(h *Handler, method, path string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(""))
	if authenticated {
		request.Header.Set("Authorization", "Bearer browser-sec")
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	return recorder
}

func refreshStringPtr(value string) *string { return &value }

func refreshOwnedSnapshot(sessionID, agentID string, connectionID *string, kind sessiondv1.RuntimeKind, state sessiondv1.SessionStatus) *sessiondv1.SessionSnapshot {
	owned := true
	ref := &sessiondv1.SessionRef{SessionId: sessionID, AgentId: &agentID}
	if connectionID != nil {
		ref.ConnectionId = connectionID
	}
	return &sessiondv1.SessionSnapshot{Session: ref, Status: state, RuntimeOwned: &owned, Runtime: &kind}
}

func seedRefreshAgent(t *testing.T, st *store.Store, id, kind, connectionID string) {
	t.Helper()
	payload := map[string]any{"id": id, "kind": kind, "name": id, "status": "ready"}
	if connectionID != "" {
		payload["connection_id"] = connectionID
	}
	if _, err := st.UpsertAgent(context.Background(), payload); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

func seedRefreshSession(t *testing.T, st *store.Store, agentID, sessionID string, fields map[string]any) {
	t.Helper()
	payload := map[string]any{
		"id": sessionID, "agent_id": agentID, "title": sessionID, "status": "idle",
		"control_state": "owned", "updated_at": "2026-10-09T00:00:00Z",
	}
	for key, value := range fields {
		payload[key] = value
	}
	if _, err := st.UpsertSession(context.Background(), payload); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
}

func seedRefreshMessage(t *testing.T, st *store.Store, agentID, sessionID, role, createdAt string) {
	t.Helper()
	if _, err := st.UpsertMessage(context.Background(), map[string]any{
		"id":       "m-" + agentID + "-" + sessionID + "-" + role + "-" + createdAt,
		"agent_id": agentID, "session_id": sessionID, "role": role, "kind": "message",
		"text": "fixture", "attachments": []any{}, "created_at": createdAt,
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}
}

type refreshRef struct {
	AgentID string `json:"agent_id"`
	ID      string `json:"id"`
}

type refreshItem struct {
	AgentID    string `json:"agent_id"`
	ID         string `json:"id"`
	LastUserAt string `json:"last_user_at"`
}

type refreshPresenceBody struct {
	Items []refreshItem `json:"items"`
	Open  []refreshRef  `json:"open"`
	Live  []refreshRef  `json:"live"`
}

type refreshActivityBody struct {
	Items []refreshItem `json:"items"`
	Live  []refreshRef  `json:"live"`
}

func refreshRefKeys(refs []refreshRef) []string {
	keys := make([]string, 0, len(refs))
	for _, ref := range refs {
		keys = append(keys, ref.AgentID+"::"+ref.ID)
	}
	sort.Strings(keys)
	return keys
}

func TestPresenceAndUserActivityProjectMessageMetadata(t *testing.T) {
	srv := &refreshDaemonServer{}
	h, st, _, cleanup := setupRefreshServer(t, srv)
	defer cleanup()

	seedRefreshAgent(t, st, "ag-1", "hermes", "")
	seedRefreshSession(t, st, "ag-1", "s-1", nil)
	seedRefreshSession(t, st, "ag-1", "s-2", map[string]any{"control_state": "readonly", "history_state": "available"})
	seedRefreshMessage(t, st, "ag-1", "s-1", "user", "2026-10-09T00:00:00Z")
	seedRefreshMessage(t, st, "ag-1", "s-1", "user", "2026-10-09T05:30:00Z")
	seedRefreshMessage(t, st, "ag-1", "s-1", "assistant", "2026-10-09T06:00:00Z")
	seedRefreshMessage(t, st, "ag-1", "s-2", "user", "2026-10-08T00:00:00Z")
	// Deleted/unknown session and a different-agent composite must never be reported.
	seedRefreshMessage(t, st, "ag-1", "gone", "user", "2026-10-09T07:00:00Z")
	seedRefreshMessage(t, st, "ag-other", "s-1", "user", "2026-10-09T08:00:00Z")

	srv.statusCb = func() (*sessiondv1.GetStatusResponse, error) {
		return &sessiondv1.GetStatusResponse{
			DaemonId: "test-daemon",
			Sessions: []*sessiondv1.SessionSnapshot{
				refreshOwnedSnapshot("s-1", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_IDLE),
			},
		}, nil
	}

	rec := refreshCall(h, http.MethodGet, "/api/v1/presence", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("presence status = %d: %s", rec.Code, rec.Body.String())
	}
	var presence refreshPresenceBody
	if err := json.Unmarshal(rec.Body.Bytes(), &presence); err != nil {
		t.Fatalf("presence body: %v", err)
	}
	wantItems := []refreshItem{
		{AgentID: "ag-1", ID: "s-1", LastUserAt: "2026-10-09T05:30:00.000Z"},
		{AgentID: "ag-1", ID: "s-2", LastUserAt: "2026-10-08T00:00:00.000Z"},
	}
	if !reflect.DeepEqual(presence.Items, wantItems) {
		t.Fatalf("items = %+v", presence.Items)
	}
	if !reflect.DeepEqual(presence.Open, []refreshRef{{AgentID: "ag-1", ID: "s-1"}}) {
		t.Fatalf("open = %+v", presence.Open)
	}
	if len(presence.Live) != 0 {
		t.Fatalf("live = %+v", presence.Live)
	}

	rec = refreshCall(h, http.MethodGet, "/api/v1/user-activity", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("user-activity status = %d: %s", rec.Code, rec.Body.String())
	}
	var activity refreshActivityBody
	if err := json.Unmarshal(rec.Body.Bytes(), &activity); err != nil {
		t.Fatalf("user-activity body: %v", err)
	}
	if !reflect.DeepEqual(activity.Items, wantItems) || len(activity.Live) != 0 {
		t.Fatalf("user-activity = %+v", activity)
	}
}

func TestPresenceLiveRequiresExplicitOwnershipAndExactRef(t *testing.T) {
	srv := &refreshDaemonServer{}
	h, st, _, cleanup := setupRefreshServer(t, srv)
	defer cleanup()

	seedRefreshAgent(t, st, "ag-1", "hermes", "")
	seedRefreshAgent(t, st, "ag-2", "hermes", "")
	for _, sid := range []string{"s-1", "s-open-idle", "s-journal", "s-released", "s-foreign", "s-other-agent", "s-wrongkind", "s-unknown", "probe-hermes-local"} {
		seedRefreshSession(t, st, "ag-1", sid, nil)
	}
	// Two connections share one native id; only the matching connection may win.
	seedRefreshSession(t, st, "ag-1", "local-1", map[string]any{"source_session_id": "native-shared"})
	seedRefreshSession(t, st, "ag-1", "remote-1", map[string]any{"source_session_id": "native-shared", "connection_id": "saved"})

	savedConn := "saved"
	agTwo := "ag-2"
	ownedFalse := false
	srv.statusCb = func() (*sessiondv1.GetStatusResponse, error) {
		return &sessiondv1.GetStatusResponse{
			DaemonId: "test-daemon",
			Sessions: []*sessiondv1.SessionSnapshot{
				refreshOwnedSnapshot("s-1", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_RUNNING),
				refreshOwnedSnapshot("s-open-idle", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_IDLE),
				{Session: &sessiondv1.SessionRef{SessionId: "s-journal", AgentId: refreshStringPtr("ag-1")}, Status: sessiondv1.SessionStatus_SESSION_STATUS_RUNNING},
				{Session: &sessiondv1.SessionRef{SessionId: "s-released", AgentId: refreshStringPtr("ag-1")}, Status: sessiondv1.SessionStatus_SESSION_STATUS_RUNNING, RuntimeOwned: &ownedFalse},
				refreshOwnedSnapshot("s-foreign", "ag-1", &savedConn, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_RUNNING),
				refreshOwnedSnapshot("s-other-agent", agTwo, nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_RUNNING),
				refreshOwnedSnapshot("s-wrongkind", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX, sessiondv1.SessionStatus_SESSION_STATUS_RUNNING),
				refreshOwnedSnapshot("s-unknown", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_UNSPECIFIED),
				refreshOwnedSnapshot("probe-hermes-local", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_RUNNING),
				refreshOwnedSnapshot("native-shared", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_RUNNING),
			},
		}, nil
	}

	rec := refreshCall(h, http.MethodGet, "/api/v1/presence", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("presence status = %d: %s", rec.Code, rec.Body.String())
	}
	var presence refreshPresenceBody
	if err := json.Unmarshal(rec.Body.Bytes(), &presence); err != nil {
		t.Fatal(err)
	}
	wantOpen := []string{"ag-1::local-1", "ag-1::s-1", "ag-1::s-open-idle"}
	wantLive := []string{"ag-1::local-1", "ag-1::s-1"}
	if got := refreshRefKeys(presence.Open); !reflect.DeepEqual(got, wantOpen) {
		t.Fatalf("open = %v, want %v", got, wantOpen)
	}
	if got := refreshRefKeys(presence.Live); !reflect.DeepEqual(got, wantLive) {
		t.Fatalf("live = %v, want %v", got, wantLive)
	}
}

func TestSyncKeepsDaemonStatusWithoutCommandHeuristics(t *testing.T) {
	srv := &refreshDaemonServer{}
	h, st, pub, cleanup := setupRefreshServer(t, srv)
	defer cleanup()

	seedRefreshAgent(t, st, "ag-1", "hermes", "")
	seedRefreshSession(t, st, "ag-1", "s-1", map[string]any{"status": "running", "title": "Fix build", "project_id": "p1", "project_name": "Proj", "workspace": "C:/work"})
	seedRefreshSession(t, st, "ag-1", "s-2", map[string]any{"status": "waiting_approval"})
	srv.statusCb = func() (*sessiondv1.GetStatusResponse, error) {
		return &sessiondv1.GetStatusResponse{
			DaemonId: "test-daemon",
			Sessions: []*sessiondv1.SessionSnapshot{
				refreshOwnedSnapshot("s-1", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_RUNNING),
				refreshOwnedSnapshot("s-2", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL),
			},
		}, nil
	}

	// No commands exist for s-1; a running session must stay running anyway.
	rec := refreshCall(h, http.MethodPost, "/api/v1/sessions/s-1/sync?agent_id=ag-1", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync s-1 status = %d: %s", rec.Code, rec.Body.String())
	}
	var row map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row["status"] != "running" || row["title"] != "Fix build" {
		t.Fatalf("sync s-1 row = %+v", row)
	}

	rec = refreshCall(h, http.MethodPost, "/api/v1/sessions/s-2/sync?agent_id=ag-1", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync s-2 status = %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row["status"] != "waiting_approval" {
		t.Fatalf("sync s-2 row = %+v", row)
	}

	stored, err := st.GetSession(context.Background(), "ag-1", "s-1")
	if err != nil || stored["status"] != "running" || stored["updated_at"] != "2026-10-09T00:00:00.000Z" {
		t.Fatalf("stored s-1 = %+v err=%v", stored, err)
	}
	stored, err = st.GetSession(context.Background(), "ag-1", "s-2")
	if err != nil || stored["status"] != "waiting_approval" {
		t.Fatalf("stored s-2 = %+v err=%v", stored, err)
	}
	if len(pub.events) != 0 {
		t.Fatalf("unchanged sync published events: %+v", pub.events)
	}
}

func TestSyncAppliesDaemonStatusTransitionAndPublishesReadBack(t *testing.T) {
	srv := &refreshDaemonServer{}
	h, st, pub, cleanup := setupRefreshServer(t, srv)
	defer cleanup()

	seedRefreshAgent(t, st, "ag-1", "hermes", "")
	seedRefreshSession(t, st, "ag-1", "s-1", map[string]any{"status": "running", "title": "Fix build", "project_id": "p1", "project_name": "Proj", "workspace": "C:/work"})
	srv.statusCb = func() (*sessiondv1.GetStatusResponse, error) {
		return &sessiondv1.GetStatusResponse{
			DaemonId: "test-daemon",
			Sessions: []*sessiondv1.SessionSnapshot{
				refreshOwnedSnapshot("s-1", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_IDLE),
			},
		}, nil
	}
	rec := refreshCall(h, http.MethodPost, "/api/v1/sessions/s-1/sync?agent_id=ag-1", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d: %s", rec.Code, rec.Body.String())
	}
	var row map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row["status"] != "idle" || row["title"] != "Fix build" || row["project_id"] != "p1" || row["workspace"] != "C:/work" {
		t.Fatalf("synced row = %+v", row)
	}
	stored, err := st.GetSession(context.Background(), "ag-1", "s-1")
	if err != nil || stored["status"] != "idle" || stored["title"] != "Fix build" || stored["project_id"] != "p1" {
		t.Fatalf("stored row = %+v err=%v", stored, err)
	}
	if len(pub.events) != 1 || pub.events[0]["type"] != "session.upsert" {
		t.Fatalf("events = %+v", pub.events)
	}
	data, ok := pub.events[0]["data"].(map[string]any)
	if !ok || data["id"] != "s-1" || data["status"] != "idle" {
		t.Fatalf("event data = %+v", pub.events[0]["data"])
	}
}

func TestSyncRejectsUnconfirmedAuthorityWithoutMutation(t *testing.T) {
	savedConn := "saved"
	ownedFalse := false
	cases := []struct {
		name     string
		statusCb func() (*sessiondv1.GetStatusResponse, error)
	}{
		{"epoch mismatch", func() (*sessiondv1.GetStatusResponse, error) {
			return &sessiondv1.GetStatusResponse{DaemonId: "other-daemon", Sessions: []*sessiondv1.SessionSnapshot{
				refreshOwnedSnapshot("s-1", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_IDLE),
			}}, nil
		}},
		{"storage unhealthy", func() (*sessiondv1.GetStatusResponse, error) {
			return &sessiondv1.GetStatusResponse{DaemonId: "test-daemon", StorageError: &sessiondv1.BusinessError{Kind: sessiondv1.ErrorKind_ERROR_KIND_UNAVAILABLE, Message: "storage down", Retryable: true}}, nil
		}},
		{"daemon read failure", func() (*sessiondv1.GetStatusResponse, error) {
			return nil, status.Error(codes.Unavailable, "daemon down")
		}},
		{"foreign connection", func() (*sessiondv1.GetStatusResponse, error) {
			return &sessiondv1.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*sessiondv1.SessionSnapshot{
				refreshOwnedSnapshot("s-1", "ag-1", &savedConn, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_IDLE),
			}}, nil
		}},
		{"missing ownership field", func() (*sessiondv1.GetStatusResponse, error) {
			return &sessiondv1.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*sessiondv1.SessionSnapshot{
				{Session: &sessiondv1.SessionRef{SessionId: "s-1", AgentId: refreshStringPtr("ag-1")}, Status: sessiondv1.SessionStatus_SESSION_STATUS_IDLE},
			}}, nil
		}},
		{"released ownership", func() (*sessiondv1.GetStatusResponse, error) {
			return &sessiondv1.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*sessiondv1.SessionSnapshot{
				{Session: &sessiondv1.SessionRef{SessionId: "s-1", AgentId: refreshStringPtr("ag-1")}, Status: sessiondv1.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &ownedFalse},
			}}, nil
		}},
		{"unknown enum", func() (*sessiondv1.GetStatusResponse, error) {
			return &sessiondv1.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*sessiondv1.SessionSnapshot{
				refreshOwnedSnapshot("s-1", "ag-1", nil, sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, sessiondv1.SessionStatus_SESSION_STATUS_UNSPECIFIED),
			}}, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &refreshDaemonServer{statusCb: tc.statusCb}
			h, st, pub, cleanup := setupRefreshServer(t, srv)
			defer cleanup()
			seedRefreshAgent(t, st, "ag-1", "hermes", "")
			seedRefreshSession(t, st, "ag-1", "s-1", map[string]any{"status": "running"})
			rec := refreshCall(h, http.MethodPost, "/api/v1/sessions/s-1/sync?agent_id=ag-1", true)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			stored, err := st.GetSession(context.Background(), "ag-1", "s-1")
			if err != nil || stored["status"] != "running" || stored["updated_at"] != "2026-10-09T00:00:00.000Z" {
				t.Fatalf("mutated row = %+v err=%v", stored, err)
			}
			if len(pub.events) != 0 {
				t.Fatalf("unconfirmed sync published: %+v", pub.events)
			}
		})
	}
}

func TestSyncReturnsHistoricalRowWithoutMutationWhenNotOwned(t *testing.T) {
	srv := &refreshDaemonServer{}
	h, st, pub, cleanup := setupRefreshServer(t, srv)
	defer cleanup()

	seedRefreshAgent(t, st, "ag-1", "hermes", "")
	seedRefreshSession(t, st, "ag-1", "s-9", map[string]any{"control_state": "readonly", "history_state": "available", "status": "idle"})
	// The daemon reports no live owner for this historical projection.
	rec := refreshCall(h, http.MethodPost, "/api/v1/sessions/s-9/sync?agent_id=ag-1", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var row map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row["id"] != "s-9" || row["control_state"] != "readonly" {
		t.Fatalf("row = %+v", row)
	}
	stored, err := st.GetSession(context.Background(), "ag-1", "s-9")
	if err != nil || stored["updated_at"] != "2026-10-09T00:00:00.000Z" {
		t.Fatalf("mutated row = %+v err=%v", stored, err)
	}
	if len(pub.events) != 0 {
		t.Fatalf("historical sync published: %+v", pub.events)
	}
}

func TestSyncRouteAuthAndMissingIdentities(t *testing.T) {
	srv := &refreshDaemonServer{}
	h, st, _, cleanup := setupRefreshServer(t, srv)
	defer cleanup()

	seedRefreshAgent(t, st, "ag-1", "hermes", "")
	seedRefreshSession(t, st, "ag-1", "s-1", nil)

	rec := refreshCall(h, http.MethodPost, "/api/v1/sessions/s-1/sync?agent_id=ag-1", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated sync = %d", rec.Code)
	}
	rec = refreshCall(h, http.MethodPost, "/api/v1/sessions/s-1/sync", true)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing agent_id sync = %d: %s", rec.Code, rec.Body.String())
	}
	rec = refreshCall(h, http.MethodPost, "/api/v1/sessions/missing/sync?agent_id=ag-1", true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing session sync = %d: %s", rec.Code, rec.Body.String())
	}
	rec = refreshCall(h, http.MethodGet, "/api/v1/presence", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated presence = %d", rec.Code)
	}
	rec = refreshCall(h, http.MethodGet, "/api/v1/user-activity", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated user-activity = %d", rec.Code)
	}
}

func TestPresenceFailsClosedWhenDaemonStatusIsUnavailable(t *testing.T) {
	srv := &refreshDaemonServer{statusCb: func() (*sessiondv1.GetStatusResponse, error) {
		return nil, status.Error(codes.Unavailable, "daemon down")
	}}
	h, st, _, cleanup := setupRefreshServer(t, srv)
	defer cleanup()

	seedRefreshAgent(t, st, "ag-1", "hermes", "")
	seedRefreshSession(t, st, "ag-1", "s-1", nil)
	seedRefreshMessage(t, st, "ag-1", "s-1", "user", "2026-10-09T00:00:00Z")

	for _, path := range []string{"/api/v1/presence", "/api/v1/user-activity"} {
		rec := refreshCall(h, http.MethodGet, path, true)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	rec := refreshCall(h, http.MethodPost, "/api/v1/sessions/s-1/sync?agent_id=ag-1", true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sync = %d: %s", rec.Code, rec.Body.String())
	}

	srv.statusCb = func() (*sessiondv1.GetStatusResponse, error) {
		return &sessiondv1.GetStatusResponse{DaemonId: "other-daemon"}, nil
	}
	rec = refreshCall(h, http.MethodGet, "/api/v1/presence", true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("epoch mismatch presence = %d: %s", rec.Code, rec.Body.String())
	}
}
