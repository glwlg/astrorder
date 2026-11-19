package modelgateway

import (
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"encoding/json"
	"net/http"
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

func TestAntigravityACPUsagePreservesCumulativeAcrossModelsAndBackfillsNative(t *testing.T) {
	var usageCalls int
	rt := &stubRuntimeQueryServer{
		queryNative: func(ctx context.Context, req *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
			usageCalls++
			raw, _ := json.Marshal(map[string]any{
				"context_available": true,
				"provider":          "antigravity",
				"model":             "gemini-4-carbon",
				"last_input_tokens": int64(79000),
				"context_window":    int64(512000),
				"input_tokens":      int64(173519606),
				"cached_tokens":     int64(163943397),
				"output_tokens":     int64(674472),
				"reasoning_tokens":  int64(374415),
				"total_tokens":      int64(174194078),
			})
			return &sessiondv1.NativeQueryResponse{
				Target: req.Target,
				Result: &sessiondv1.NativeJson{Schema: "antigravity.session.usage", Utf8Json: raw},
			}, nil
		},
	}

	h, st, cleanup := setupModelGatewayWithRuntime(t, rt)
	defer cleanup()
	ctx := context.Background()

	if _, err := st.UpsertAgent(ctx, map[string]any{
		"id":     "local-antigravity",
		"kind":   "antigravity",
		"name":   "Antigravity",
		"status": "ready",
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate an existing wiped row from ACP usage_update (live 81208 / 1048576, total_tokens = 0).
	if _, err := st.UpsertTokenMetric(ctx, map[string]any{
		"agent_id":          "local-antigravity",
		"session_id":        "52794208-5f2e-4120-a6b1-99e5ee29c7f1",
		"model":             "gemini-4-carbon-high",
		"provider":          "antigravity",
		"date":              "2026-11-18",
		"context_window":    int64(1048576),
		"last_input_tokens": int64(81208),
		"total_tokens":      int64(0),
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	h.handleGetSessionUsage(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/52794208-5f2e-4120-a6b1-99e5ee29c7f1/usage?agent_id=local-antigravity", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if usageCalls != 1 {
		t.Fatalf("expected 1 native backfill query, got %d", usageCalls)
	}
	if got["context_window"] != float64(1048576) || got["last_input_tokens"] != float64(81208) || got["total_tokens"] != float64(174194078) {
		t.Fatalf("expected preserved 1M ACP window + live input + backfilled cumulative tokens, got: %s", w.Body.String())
	}

	// Even when runtime is nil/offline, multi-row session fallback recovers cumulative counters from prior model row.
	offlineHandler := &Handler{store: st}
	if _, err := st.UpsertTokenMetric(ctx, map[string]any{
		"agent_id":          "local-antigravity",
		"session_id":        "52794208-5f2e-4120-a6b1-99e5ee29c7f1",
		"model":             "gemini-4-carbon-low",
		"provider":          "antigravity",
		"date":              "2026-11-19",
		"context_window":    int64(1048576),
		"last_input_tokens": int64(92000),
		"total_tokens":      int64(0),
	}); err != nil {
		t.Fatal(err)
	}
	w2 := httptest.NewRecorder()
	offlineHandler.handleGetSessionUsage(w2, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/52794208-5f2e-4120-a6b1-99e5ee29c7f1/usage?agent_id=local-antigravity", nil))
	var got2 map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &got2)
	if got2["last_input_tokens"] != float64(92000) || got2["context_window"] != float64(1048576) || got2["total_tokens"] != float64(174194078) {
		t.Fatalf("multi-row session fallback failed: %s", w2.Body.String())
	}
}
