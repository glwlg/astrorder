package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOccupiedCodexModelReadsNativeMetadataWithoutBinding(t *testing.T) {
	h, st, srv, _ := newNativeFixture(t, map[string]any{"id": "occupied", "model": "native-model", "modelProvider": "native-provider"})
	_, err := st.UpsertSession(context.Background(), map[string]any{"id": "occupied", "agent_id": "codex-1", "status": "idle", "updated_at": "2026-10-12T00:00:00Z", "title": "test"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/occupied/model?agent_id=codex-1", nil)
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["model"] != "native-model" || body["read_only"] != true || srv.reads != 1 {
		t.Fatalf("%v reads=%d", body, srv.reads)
	}
}
