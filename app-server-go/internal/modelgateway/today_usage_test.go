package modelgateway_test

import (
	"encoding/json"
	"net/http"
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
