package modelgateway

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAntigravityUsageDoesNotInferContextFromCumulativeInput(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertTokenMetric(context.Background(), map[string]any{"agent_id": "agy", "session_id": "s", "model": "probe", "provider": "antigravity", "date": "2026-10-18", "input_tokens": int64(22), "output_tokens": int64(6), "total_tokens": int64(28)})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{store: st}
	w := httptest.NewRecorder()
	h.handleGetSessionUsage(w, httptest.NewRequest("GET", "/api/v1/sessions/s/usage?agent_id=agy", nil))
	var data map[string]any
	json.Unmarshal(w.Body.Bytes(), &data)
	if data["context_available"] != false || data["total_tokens"] != float64(28) {
		t.Fatalf("unknown context became measured zero: %s", w.Body.String())
	}
}
