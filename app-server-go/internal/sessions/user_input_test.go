package sessions

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserInputHTTPPreservesAnswerAndScope(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	seedProjectSession(t, h, "a", "s", "one", "p")
	if _, err := st.AppendEvent(ctx, store.Event{ID: "question", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "codex:91", "method": "item/tool/requestUserInput", "state": "pending", "questions": []any{map[string]any{"id": "name", "question": "名字？"}}}}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"id":"reply","agent_id":"a","request_id":"codex:91","answers":{"wrong":{"answers":["x"]}}}`, 422},
		{`{"id":"reply","agent_id":"a","request_id":"foreign","answers":{}}`, 409},
		{`{"id":"reply","agent_id":"a","request_id":"codex:91","answers":{"name":{"answers":["张三"]}}}`, 200},
	} {
		capturedApprovalRequest = nil
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/sessions/s/user-input", strings.NewReader(test.body))
		r.Header.Set("Authorization", "Bearer browser-sec")
		h.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if test.status == 200 {
			var response map[string]any
			if capturedApprovalRequest == nil {
				t.Fatal("missing gRPC answer")
			}
			_ = json.Unmarshal(capturedApprovalRequest.UserInput.Utf8Json, &response)
			if response["answers"].(map[string]any)["name"].(map[string]any)["answers"].([]any)[0] != "张三" {
				t.Fatal(response)
			}
		} else if capturedApprovalRequest != nil {
			t.Fatal("invalid answer dispatched")
		}
	}
	// Native resolution can arrive before the browser receives its HTTP receipt.
	if _, err := st.AppendEvent(ctx, store.Event{ID: "resolved", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "codex:91", "state": "approved"}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/sessions/s/user-input", strings.NewReader(`{"id":"reply","agent_id":"a","request_id":"codex:91","answers":{"name":{"answers":["张三"]}}}`))
	r.Header.Set("Authorization", "Bearer browser-sec")
	h.ServeHTTP(w, r)
	if w.Code != 200 || capturedApprovalRequest.Meta.OperationId != "reply" {
		t.Fatalf("receipt retry blocked: %d %s", w.Code, w.Body.String())
	}
	rows, err := st.QueryRows(ctx, "SELECT * FROM commands")
	if err != nil || len(rows) != 0 {
		t.Fatalf("answer persisted as chat: %v %v", rows, err)
	}
}
