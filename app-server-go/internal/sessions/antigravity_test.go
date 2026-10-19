package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAntigravityModelIsFixedWithoutBlockingChat(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "local-antigravity", "kind": "antigravity", "name": "Antigravity", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "11111111-1111-4111-8111-111111111111", "agent_id": "local-antigravity", "status": "idle", "control_state": "owned", "updated_at": "2026-10-16T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/11111111-1111-4111-8111-111111111111/model?agent_id=local-antigravity", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("model: %d %s", rec.Code, rec.Body.String())
	}
	var binding map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	if binding["read_only"] == true {
		t.Fatal("fixed model must not block text chat")
	}
	if binding["model_read_only"] != true {
		t.Fatal("model switching must be disabled")
	}
}
