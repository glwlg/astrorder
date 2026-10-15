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

func TestNativeHistoryFailureNeverReturnsLegacyAppMessages(t *testing.T) {
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
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO messages(id,session_id,agent_id,role,kind,text,attachments,created_at) VALUES('msg-1',?,?,'user','message','Hello from historical session projection','[]','2026-10-09T00:00:00Z')`, sid, aid); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages?agent_id="+aid, nil)
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected explicit native error, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "Hello from historical session projection") {
		t.Fatalf("legacy App history leaked: %s", body)
	}
}
