package modelgateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
)

func TestAnalyticsUsageStaleWhileRevalidateAndEpochGuard(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "swr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var calls atomic.Int32
	bgStarted := make(chan struct{}, 2)
	bgRelease := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 {
			select {
			case bgStarted <- struct{}{}:
			default:
			}
			<-bgRelease
		}
		_, _ = w.Write([]byte(`{"period":"today","calls":` + string(rune('0'+n)) + `,"input":10,"output":5,"cache_read":0,"models":[],"series":[]}`))
	}))
	defer upstream.Close()

	if _, err := SaveGatewayConfig(context.Background(), st, map[string]any{
		"gateway_type":   "magpie",
		"management_url": "http://unused.invalid",
		"inference_url":  upstream.URL + "/v1",
		"api_key":        "fixture-key",
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := NewHandler(Options{Store: st, HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	h := raw.(*Handler)
	defer func() { _ = h.Close(context.Background()) }()

	// 1) Initial cold call populates cache (calls=1).
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/usage?range=today&surface=all", nil))
	if w1.Code != http.StatusOK || !strings.Contains(w1.Body.String(), `"requests":1`) {
		t.Fatalf("cold call failed: %d %s", w1.Code, w1.Body.String())
	}

	// 2) Age freshUntil into the past while keeping staleUntil valid (simulates 3-min Sidebar poll).
	const key = "today|all||||"
	h.analyticsFlightMu.Lock()
	entry := h.analyticsCache[key]
	entry.freshUntil = time.Now().Add(-time.Second)
	entry.staleUntil = time.Now().Add(5 * time.Minute)
	h.analyticsCache[key] = entry
	h.analyticsFlightMu.Unlock()

	// Stale hit returns immediately with v1 while kicking off ONE background refresh (calls=2).
	started := time.Now()
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/usage?range=today&surface=all", nil))
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("stale-while-revalidate blocked for %v", elapsed)
	}
	if w2.Code != http.StatusOK || !strings.Contains(w2.Body.String(), `"requests":1`) {
		t.Fatalf("expected immediate stale v1 response: %d %s", w2.Code, w2.Body.String())
	}

	select {
	case <-bgStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background refresh never started")
	}

	// Second stale hit while background refresh is in-flight does not spawn a 3rd upstream request.
	w2b := httptest.NewRecorder()
	h.ServeHTTP(w2b, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/usage?range=today&surface=all", nil))
	if w2b.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("expected coalesced single background refresh: code=%d calls=%d", w2b.Code, calls.Load())
	}

	close(bgRelease)

	// Wait until background refresh populates fresh v2 cache entry.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w3 := httptest.NewRecorder()
		h.ServeHTTP(w3, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/usage?range=today&surface=all", nil))
		if strings.Contains(w3.Body.String(), `"requests":2`) {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/usage?range=today&surface=all", nil))
	if w3.Code != http.StatusOK || !strings.Contains(w3.Body.String(), `"requests":2`) {
		t.Fatalf("background refresh did not update cache to v2: %d %s", w3.Code, w3.Body.String())
	}

	// 3) Config update invalidates cache and advances epoch so stale data is never returned.
	cfgReq := httptest.NewRequest(http.MethodPut, "/api/v1/analytics/config", bytes.NewReader([]byte(`{"gateway_type":"magpie"}`)))
	wCfg := httptest.NewRecorder()
	h.ServeHTTP(wCfg, cfgReq)
	if wCfg.Code != http.StatusOK {
		t.Fatalf("config update failed: %d %s", wCfg.Code, wCfg.Body.String())
	}
	w4 := httptest.NewRecorder()
	h.ServeHTTP(w4, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/usage?range=today&surface=all", nil))
	if w4.Code != http.StatusOK || !strings.Contains(w4.Body.String(), `"requests":3`) {
		t.Fatalf("expected fresh v3 after config invalidation: %d %s", w4.Code, w4.Body.String())
	}
}
