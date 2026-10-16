package modelgateway_test

import (
	"encoding/json"
	"net/http"
	"testing"
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
