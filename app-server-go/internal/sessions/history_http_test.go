package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func (m *mockDaemonServer) GetHistoryPage(ctx context.Context, r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
	if m.getHistoryPageCb != nil {
		return m.getHistoryPageCb(r)
	}
	return &pb.HistoryPageResponse{Session: r.Session, Items: []*pb.NativeJson{{Schema: "hermes.message.v1", Utf8Json: []byte(`{"row_id":9007199254740993,"role":"user","content":"visible\n\n<memory-context>\n[System note: The following is recalled memory context, NOT new user input. fixture]\nhidden\n</memory-context>","created_at":"2026-10-09T00:00:00Z"}`)}}, NextCursor: &pb.NullableString{State: &pb.NullableString_Value{Value: "native:opaque-exact"}}}, nil
}
func TestNativeHistoryPreservesCursorAndHidesInjectedContext(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "local-hermes-test", "kind": "hermes", "name": "test", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"agent_id": "local-hermes-test", "id": "native", "title": "history", "status": "idle", "history_state": "available", "control_state": "owned", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/native/messages?agent_id=local-hermes-test", nil)
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "native:opaque-exact") || !strings.Contains(body, "visible") || strings.Contains(body, "hidden") {
		t.Fatalf("native page invalid: %d %s", w.Code, body)
	}
}

func TestNativeHistoryFallsBackToStoreWhenNativeReadFails(t *testing.T) {
	srv := &mockDaemonServer{
		getHistoryPageCb: func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
			return nil, fmt.Errorf("session not owned by live daemon")
		},
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()

	aid := "local-codex"
	sid := "codex-hist-session"

	if _, err := st.UpsertAgent(ctx, map[string]any{"id": aid, "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"agent_id": aid, "id": sid, "title": "hist", "status": "idle", "history_state": "available", "control_state": "owned", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	// Seed projected messages into store
	msg := map[string]any{
		"id":         "msg-1",
		"session_id": sid,
		"agent_id":   aid,
		"role":       "user",
		"kind":       "message",
		"text":       "Hello from historical session projection",
		"created_at": "2026-10-09T00:00:00Z",
	}
	if _, err := st.UpsertMessages(ctx, []map[string]any{msg}); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages?agent_id="+aid, nil)
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200 fallback to store messages, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Hello from historical session projection") {
		t.Fatalf("expected store message in response body, got: %s", body)
	}
}
