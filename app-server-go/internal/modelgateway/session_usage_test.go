package modelgateway

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestSessionUsageReadsExactAgentMetrics(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertTokenMetric(context.Background(), map[string]any{"agent_id": "a", "session_id": "s", "model": "m", "date": "2026-10-12", "input_tokens": int64(4000), "output_tokens": int64(200), "total_tokens": int64(4200), "last_input_tokens": int64(2000), "context_window": int64(10000)})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{store: st}
	w := httptest.NewRecorder()
	h.handleGetSessionUsage(w, httptest.NewRequest("GET", "/api/v1/sessions/s/usage?agent_id=a", nil))
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || data["last_input_tokens"] != float64(2000) || data["used_percentage"] != float64(20) || data["speed"] != nil {
		t.Fatalf("usage not read: %s", w.Body.String())
	}

	// Recording session_speed preference surfaces via /sessions/{id}/usage as speed.
	if err := st.SetPreference(context.Background(), "session_speed:a\x00s", map[string]any{"speed": 68.42}, false); err != nil {
		t.Fatal(err)
	}
	w2 := httptest.NewRecorder()
	h.handleGetSessionUsage(w2, httptest.NewRequest("GET", "/api/v1/sessions/s/usage?agent_id=a", nil))
	var data2 map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &data2); err != nil {
		t.Fatal(err)
	}
	if w2.Code != 200 || data2["speed"] != 68.4 {
		t.Fatalf("expected speed 68.4, got %s", w2.Body.String())
	}
}
