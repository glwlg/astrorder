package agentapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type unownedHistoryDaemonServer struct {
	sessiondv1.UnimplementedRuntimeQueryServer
	sessiondv1.UnimplementedSessionControlServer

	mu         sync.Mutex
	attached   map[string]bool
	failAttach map[string]error

	attachCount      int
	queryNativeCount int

	hermesItems   []*sessiondv1.NativeJson
	codexItems    []map[string]any
	hermesPayload map[string]any
	lastTarget    *sessiondv1.RuntimeTarget
}

func (s *unownedHistoryDaemonServer) ReadModel(_ context.Context, r *sessiondv1.ReadModelRequest) (*sessiondv1.ModelState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sid := r.GetSession().GetSessionId()
	if !s.attached[sid] {
		return nil, status.Error(codes.NotFound, "session is not daemon-owned")
	}
	model := "group/luozhi"
	provider := "custom"
	return &sessiondv1.ModelState{
		Session:  r.Session,
		Model:    &model,
		Provider: &provider,
	}, nil
}

func (s *unownedHistoryDaemonServer) AttachSession(_ context.Context, r *sessiondv1.AttachSessionRequest) (*sessiondv1.SessionReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attachCount++
	sid := r.GetSession().GetSessionId()
	if err, ok := s.failAttach[sid]; ok && err != nil {
		return nil, err
	}
	s.attached[sid] = true
	return &sessiondv1.SessionReceipt{
		Session:  r.Session,
		Status:   sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
		Attached: true,
	}, nil
}

func (s *unownedHistoryDaemonServer) GetHistoryPage(_ context.Context, r *sessiondv1.HistoryPageRequest) (*sessiondv1.HistoryPageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sid := r.GetSession().GetSessionId()
	if !s.attached[sid] {
		return nil, status.Error(codes.NotFound, "session is not daemon-owned")
	}
	return &sessiondv1.HistoryPageResponse{
		Session:    r.Session,
		Items:      s.hermesItems,
		NextCursor: &sessiondv1.NullableString{State: &sessiondv1.NullableString_Value{Value: "hermes-rpc:anchor-page-2"}},
	}, nil
}

func (s *unownedHistoryDaemonServer) QueryNative(_ context.Context, r *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queryNativeCount++
	s.lastTarget = r.Target

	if query := r.GetCodexThreadItemsList(); query != nil {
		payload := map[string]any{
			"data":       s.codexItems,
			"nextCursor": "page-2-token",
		}
		raw, _ := json.Marshal(payload)
		return &sessiondv1.NativeQueryResponse{
			Result: &sessiondv1.NativeJson{
				Schema:   "codex.thread-items.v1",
				Utf8Json: raw,
			},
		}, nil
	}
	if query := r.GetHermesSessionHistory(); query != nil {
		messages := make([]map[string]any, len(s.hermesItems))
		for i, item := range s.hermesItems {
			var m map[string]any
			_ = json.Unmarshal(item.Utf8Json, &m)
			messages[i] = m
		}
		payload := map[string]any{
			"result": map[string]any{
				"session_id": query.SessionId,
				"messages":   messages,
				"info":       map[string]any{"profile_name": "default"},
			},
		}
		if s.hermesPayload != nil {
			payload = s.hermesPayload
		}
		raw, _ := json.Marshal(payload)
		return &sessiondv1.NativeQueryResponse{
			Result: &sessiondv1.NativeJson{
				Schema:   "hermes.snapshot.v1",
				Utf8Json: raw,
			},
		}, nil
	}
	return nil, status.Error(codes.Unimplemented, "unhandled native query")
}

func setupUnownedServer(t *testing.T, srv *unownedHistoryDaemonServer) (*Handler, *store.Store, func()) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	sessiondv1.RegisterRuntimeQueryServer(grpcServer, srv)
	sessiondv1.RegisterSessionControlServer(grpcServer, srv)
	go grpcServer.Serve(listener)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		grpcServer.Stop()
		st.Close()
		t.Fatal(err)
	}

	client := &daemonclient.Client{
		Runtime: sessiondv1.NewRuntimeQueryClient(conn),
		Session: sessiondv1.NewSessionControlClient(conn),
	}
	h := &Handler{
		options: Options{
			Store:  st,
			Client: client,
		},
	}
	cleanup := func() {
		conn.Close()
		grpcServer.Stop()
		st.Close()
	}
	return h, st, cleanup
}

func TestSessionsRead_ColdStartUnownedHermesSession(t *testing.T) {
	aid := "local-hermes-default"
	sid := "local-4471cc1577cc9ab28e8dbf7a99f5ec895c85de2f0dddc1b91e8bad9cf585e4dc"

	// 8 messages: page 1 (5 items), page 2 (3 items)
	hermesItems := make([]*sessiondv1.NativeJson, 8)
	for i := 0; i < 8; i++ {
		raw := fmt.Sprintf(`{"row_id":%d,"role":"user","content":"msg-%d","created_at":"2026-10-19T00:00:0%dZ"}`, i+1, i+1, i)
		hermesItems[i] = &sessiondv1.NativeJson{
			Schema:   "hermes.message.v1",
			Utf8Json: []byte(raw),
		}
	}

	srv := &unownedHistoryDaemonServer{
		attached:    make(map[string]bool),
		failAttach:  make(map[string]error),
		hermesItems: hermesItems,
	}

	h, st, cleanup := setupUnownedServer(t, srv)
	defer cleanup()

	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": aid, "kind": "hermes", "name": "Hermes", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{
		"agent_id":          aid,
		"id":                sid,
		"source_session_id": sid,
		"title":             "External parent session",
		"status":            "idle",
		"control_state":     "external",
		"history_state":     "local",
		"updated_at":        "2026-10-19T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	// Cold start invocation: external Hermes session must use QueryNative directly
	// and NEVER call AttachSession (attachCount must be 0)
	res, err := h.invokeSessionsRead(ctx, map[string]any{
		"key":   aid + "::" + sid,
		"limit": 5,
	})
	if err != nil {
		t.Fatalf("invokeSessionsRead failed on unowned cold-start session: %v", err)
	}

	if srv.attachCount != 0 {
		t.Fatalf("expected 0 AttachSession calls on external Hermes session, got %d", srv.attachCount)
	}
	if srv.queryNativeCount != 1 {
		t.Fatalf("expected 1 QueryNative call, got %d", srv.queryNativeCount)
	}

	resMap, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any response, got %T", res)
	}
	items, ok := resMap["items"].([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any items, got %T", resMap["items"])
	}
	if len(items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(items))
	}
	nextCursor, _ := resMap["next_cursor"].(string)
	if !strings.HasPrefix(nextCursor, "hermes-rpc:") {
		t.Fatalf("expected next_cursor starting with 'hermes-rpc:', got '%v'", nextCursor)
	}

	// Second page continuation with cursor
	resPage2, err := h.invokeSessionsRead(ctx, map[string]any{
		"key":    aid + "::" + sid,
		"limit":  5,
		"before": nextCursor,
	})
	if err != nil {
		t.Fatalf("invokeSessionsRead page 2 failed: %v", err)
	}
	if srv.attachCount != 0 {
		t.Fatalf("expected 0 AttachSession calls across pages, got %d", srv.attachCount)
	}
	items2 := resPage2.(map[string]any)["items"].([]map[string]any)
	if len(items2) != 3 {
		t.Fatalf("expected 3 items on page 2, got %d", len(items2))
	}
}

func TestSessionsRead_HermesForeignCursorExplicitError(t *testing.T) {
	aid := "local-hermes-default"
	sid := "local-4471cc1577cc9ab28e8dbf7a99f5ec895c85de2f0dddc1b91e8bad9cf585e4dc"

	srv := &unownedHistoryDaemonServer{
		attached:   make(map[string]bool),
		failAttach: make(map[string]error),
	}

	h, st, cleanup := setupUnownedServer(t, srv)
	defer cleanup()

	ctx := context.Background()
	st.UpsertAgent(ctx, map[string]any{"id": aid, "kind": "hermes", "name": "Hermes", "status": "ready"})
	st.UpsertSession(ctx, map[string]any{
		"agent_id":          aid,
		"id":                sid,
		"source_session_id": sid,
		"title":             "External session",
		"status":            "idle",
		"control_state":     "external",
		"history_state":     "local",
		"updated_at":        "2026-10-19T00:00:00Z",
	})

	// Override QueryNative to return a mismatched session ID
	// Must return an explicit error and NOT swallow it
	srv.mu.Lock()
	srv.hermesItems = []*sessiondv1.NativeJson{
		{
			Schema:   "hermes.message.v1",
			Utf8Json: []byte(`{"row_id":1,"role":"user","content":"test"}`),
		},
	}
	srv.mu.Unlock()

	// Modify payload in query server to return mismatched ID
	// Let's test foreign cursor as well
	_, err := h.invokeSessionsRead(ctx, map[string]any{
		"key":    aid + "::" + sid,
		"limit":  5,
		"before": "hermes-rpc:eyJ2ZXJzaW9uIjoxLCJzZXNzaW9uIjoiZm9yZWlnbi1zZXNzaW9uIiwiYW5jaG9yIjoiYWJjIn0",
	})
	if err == nil {
		t.Fatal("expected error on foreign gateway cursor, got nil")
	}
	if !strings.Contains(err.Error(), "foreign gateway history cursor") {
		t.Fatalf("expected foreign cursor error, got: %v", err)
	}
}

func TestSessionsRead_ColdStartCodexActiveWriterFallback(t *testing.T) {
	aid := "local-codex"
	sid := "codex-desktop-locked"

	codexItems := make([]map[string]any, 3)
	for i := 0; i < 3; i++ {
		codexItems[i] = map[string]any{
			"id":   fmt.Sprintf("item-%d", i+1),
			"type": "agentMessage",
			"text": fmt.Sprintf("codex reply %d", i+1),
		}
	}

	srv := &unownedHistoryDaemonServer{
		attached: make(map[string]bool),
		failAttach: map[string]error{
			sid: status.Error(codes.FailedPrecondition, "already has an active writer"),
		},
		codexItems: codexItems,
	}

	h, st, cleanup := setupUnownedServer(t, srv)
	defer cleanup()

	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": aid, "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{
		"agent_id":          aid,
		"id":                sid,
		"source_session_id": sid,
		"title":             "Desktop active session",
		"status":            "idle",
		"control_state":     "external",
		"history_state":     "local",
		"updated_at":        "2026-10-19T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := h.invokeSessionsRead(ctx, map[string]any{
		"key":   aid + "::" + sid,
		"limit": 5,
	})
	if err != nil {
		t.Fatalf("invokeSessionsRead failed for active writer codex session: %v", err)
	}

	resMap := res.(map[string]any)
	items := resMap["items"].([]map[string]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	// unbound codex cursor must have prefix items:
	if nextCursor, _ := resMap["next_cursor"].(string); !strings.HasPrefix(nextCursor, "items:") {
		t.Fatalf("expected next_cursor with 'items:' prefix, got '%v'", resMap["next_cursor"])
	}
}

func TestSessionsRead_UnboundCursorContinuation(t *testing.T) {
	aid := "local-codex"
	sid := "codex-desktop-locked"

	codexItems := []map[string]any{
		{
			"id":   "item-old",
			"type": "agentMessage",
			"text": "older reply",
		},
	}

	srv := &unownedHistoryDaemonServer{
		attached: make(map[string]bool),
		failAttach: map[string]error{
			sid: status.Error(codes.FailedPrecondition, "already has an active writer"),
		},
		codexItems: codexItems,
	}

	h, st, cleanup := setupUnownedServer(t, srv)
	defer cleanup()

	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": aid, "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{
		"agent_id":          aid,
		"id":                sid,
		"source_session_id": sid,
		"title":             "Desktop active session",
		"status":            "idle",
		"control_state":     "external",
		"history_state":     "local",
		"updated_at":        "2026-10-19T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := h.invokeSessionsRead(ctx, map[string]any{
		"key":    aid + "::" + sid,
		"limit":  5,
		"before": "items:page-2-token",
	})
	if err != nil {
		t.Fatalf("invokeSessionsRead failed for unbound cursor: %v", err)
	}

	resMap := res.(map[string]any)
	items := resMap["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
}

func TestSessionsRead_ColdStartHermesAttachFailedFallbackToQueryNative(t *testing.T) {
	aid := "local-hermes-default"
	sid := "local-hermes-unbound"

	hermesItems := make([]*sessiondv1.NativeJson, 3)
	for i := 0; i < 3; i++ {
		raw := fmt.Sprintf(`{"row_id":%d,"role":"user","content":"hermes-unbound-%d","created_at":"2026-10-19T00:00:0%dZ"}`, i+1, i+1, i)
		hermesItems[i] = &sessiondv1.NativeJson{
			Schema:   "hermes.message.v1",
			Utf8Json: []byte(raw),
		}
	}

	srv := &unownedHistoryDaemonServer{
		attached: make(map[string]bool),
		failAttach: map[string]error{
			sid: status.Error(codes.Unavailable, "gateway viewer attach refused"),
		},
		hermesItems: hermesItems,
	}

	h, st, cleanup := setupUnownedServer(t, srv)
	defer cleanup()

	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": aid, "kind": "hermes", "name": "Hermes", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{
		"agent_id":          aid,
		"id":                sid,
		"source_session_id": sid,
		"title":             "Hermes attach failed session",
		"status":            "idle",
		"control_state":     "external",
		"history_state":     "local",
		"updated_at":        "2026-10-19T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	// Attach fails, should safely fallback to QueryNative without failing
	res, err := h.invokeSessionsRead(ctx, map[string]any{
		"key":   aid + "::" + sid,
		"limit": 5,
	})
	if err != nil {
		t.Fatalf("invokeSessionsRead failed for attach-failed hermes session: %v", err)
	}

	resMap := res.(map[string]any)
	items := resMap["items"].([]map[string]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
}
