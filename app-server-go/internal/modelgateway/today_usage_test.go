package modelgateway_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMagpieTodayUsesOneSummaryRequest(t *testing.T) {
	calls := 0
	h := magpieAnalyticsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/magpie/usage" || r.URL.Query().Get("period") != "today" {
			t.Errorf("unexpected upstream request: %s", r.URL.RequestURI())
		}
		_, _ = w.Write([]byte(`{"period":"today","calls":2,"input":10,"output":5,"cache_read":100,"models":[{"provider":"codex","model":"gpt-test","calls":2,"input":10,"output":5,"cache_read":100}],"series":[]}`))
	})
	w := analyticsRequest(t, h, "/api/v1/analytics/usage?range=today&surface=all")
	if w.Code != 200 || calls != 1 {
		t.Fatalf("status=%d calls=%d", w.Code, calls)
	}
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["range"] != "today" || v["summary"].(map[string]any)["totalTokens"] != float64(115) || len(v["models"].([]any)) != 1 {
		t.Fatal("today summary lost totals or models")
	}
}

func TestMagpieTodayCoalescesConcurrentRequests(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	h := magpieAnalyticsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		_, _ = w.Write([]byte(`{"period":"today","calls":1,"input":7,"output":3,"cache_read":0,"models":[],"series":[]}`))
	})
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := analyticsRequest(t, h, "/api/v1/analytics/usage?range=today&surface=all")
			codes[idx] = w.Code
		}(i)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream usage call never started")
	}
	time.Sleep(30 * time.Millisecond)
	close(release)
	wg.Wait()
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("unexpected status codes: %v", codes)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 coalesced upstream call, got %d", got)
	}
}

func TestAnalyticsUsageShortTTLCacheAndConfigInvalidation(t *testing.T) {
	var calls atomic.Int32
	h := magpieAnalyticsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		_, _ = w.Write([]byte(`{"period":"today","calls":` + string(rune('0'+n)) + `,"input":10,"output":5,"cache_read":0,"models":[],"series":[]}`))
	})

	// 1) Repeated requests within the short TTL reuse cached response.
	for range 3 {
		w := analyticsRequest(t, h, "/api/v1/analytics/usage?range=today&surface=all")
		if w.Code != http.StatusOK {
			t.Fatalf("cached usage failed: %d %s", w.Code, w.Body.String())
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 upstream call due to short TTL cache, got %d", got)
	}

	// 2) Explicit refresh=1 bypasses the TTL cache.
	wRefresh := analyticsRequest(t, h, "/api/v1/analytics/usage?range=today&surface=all&refresh=1")
	if wRefresh.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("refresh=1 did not bypass cache: status=%d calls=%d", wRefresh.Code, calls.Load())
	}

	// 3) Updating gateway config invalidates the analytics usage cache.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/analytics/config", bytes.NewReader([]byte(`{"gateway_type":"magpie"}`)))
	req.Header.Set("Authorization", "Bearer browser-secret")
	wCfg := httptest.NewRecorder()
	h.ServeHTTP(wCfg, req)
	if wCfg.Code != http.StatusOK {
		t.Fatalf("config update failed: %d %s", wCfg.Code, wCfg.Body.String())
	}
	wAfter := analyticsRequest(t, h, "/api/v1/analytics/usage?range=today&surface=all")
	if wAfter.Code != http.StatusOK || calls.Load() != 3 {
		t.Fatalf("config update did not invalidate cache: status=%d calls=%d", wAfter.Code, calls.Load())
	}
}
