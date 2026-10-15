package sessions

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestBootstrapIncludesDurableFailedAndUnknownQueue(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	for _, state := range []string{"queued", "failed", "unknown", "cancelled"} {
		if _, _, err := st.CreateCommand(context.Background(), map[string]any{"id": state, "agent_id": "a", "session_id": "s", "action": "enqueue", "text": state}, state); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest("GET", "/api/v1/bootstrap", nil)
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var body struct {
		Commands []map[string]any `json:"commands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(body.Commands) != 3 {
		t.Fatalf("lost pending queue: %d %s", w.Code, w.Body.String())
	}
}
