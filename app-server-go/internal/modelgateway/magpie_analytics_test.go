package modelgateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/modelgateway"
)

func magpieAnalyticsHandler(t *testing.T, serve http.HandlerFunc) http.Handler {
	t.Helper()
	st, dc, authz, cleanup := setupTestEnvironment(t, nil)
	t.Cleanup(cleanup)
	upstream := httptest.NewServer(serve)
	t.Cleanup(upstream.Close)
	_, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{
		"gateway_type": "magpie", "management_url": "http://unused.invalid", "inference_url": upstream.URL + "/v1", "api_key": "fixture-gateway-key", "management_key": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := modelgateway.NewHandler(modelgateway.Options{Store: st, Client: dc, Authorizer: authz, Publisher: &mockPublisher{}, HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func analyticsRequest(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer browser-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestMagpieQuotaRouteIsDispatched(t *testing.T) {
	if !modelgateway.Matches(http.MethodGet, "/api/v1/analytics/quotas") || modelgateway.Matches(http.MethodPost, "/api/v1/analytics/quotas") {
		t.Fatal("quota route is missing from application dispatcher")
	}
}

func TestMagpieNativeQuota(t *testing.T) {
	h := magpieAnalyticsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/magpie/quotas" || r.Header.Get("Authorization") != "Bearer fixture-gateway-key" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"provider":"codex","name":"Codex","user":"fixture-account","kind":"subscription","plan":"plus","windows":[{"name":"5h","used":95,"remaining":5,"resetsAt":"2026-10-16T01:00:00Z"},{"name":"7d","used":20,"remaining":80}]},{"provider":"other","name":"Other","kind":"balance","balance":"$12","windows":[]}]}`))
	})
	w := analyticsRequest(t, h, "/api/v1/analytics/model-quota?model=codex/gpt-test")
	if w.Code != 200 {
		t.Fatalf("quota: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Providers []string         `json:"providers"`
		Reports   []map[string]any `json:"reports"`
		Accounts  []struct {
			Quota struct {
				Short  float64 `json:"shortPercent"`
				Weekly float64 `json:"weeklyPercent"`
			} `json:"quota"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 1 || got.Providers[0] != "codex" || len(got.Accounts) != 1 || got.Accounts[0].Quota.Short != 95 || got.Accounts[0].Quota.Weekly != 20 {
		t.Fatalf("missing quota: %s", w.Body.String())
	}
	for _, route := range []string{"codex/gpt-test", "magpie/codex/gpt-test", "codex/codex/gpt-test"} {
		w = analyticsRequest(t, h, "/api/v1/analytics/model-quota?model="+route)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"shortPercent":95`) {
			t.Fatalf("wrapped model quota missing: %s %s", route, w.Body.String())
		}
	}
	w = analyticsRequest(t, h, "/api/v1/analytics/quotas")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"balance":"$12"`) {
		t.Fatalf("quota panel: %d %s", w.Code, w.Body.String())
	}
}

func TestMagpieFilteredUsagePaginatesAndHonorsExactTimes(t *testing.T) {
	calls := 0
	h := magpieAnalyticsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/magpie/usage/requests" || r.URL.Query().Get("agent") != "hermes" || r.URL.Query().Get("period") != "all" {
			t.Errorf("wrong filter: %s", r.URL.String())
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("offset") == "0" {
			_, _ = w.Write([]byte(`{"rows":[{"t":"2026-10-15T10:30:00Z","provider":"codex","model":"gpt-test","providerAccount":"fixture-account","in":10,"out":2,"cache_read":8,"cost":0.1,"priced":true},{"t":"2026-10-15T11:00:00Z","provider":"codex","model":"gpt-test","in":100,"out":200}],"offset":0,"total":3}`))
		} else {
			_, _ = w.Write([]byte(`{"rows":[{"t":"2026-10-15T10:00:00Z","provider":"codex","model":"gpt-test","providerAccount":"fixture-account","in":20,"out":3,"cache_read":7,"cost":0.2,"priced":true}],"offset":2,"total":3}`))
		}
	})
	start := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC).UnixMilli()
	w := analyticsRequest(t, h, fmt.Sprintf("/api/v1/analytics/usage?range=all&surface=hermes&since=%d&until=%d", start, end))
	if w.Code != 200 {
		t.Fatalf("filtered usage: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Summary  map[string]float64 `json:"summary"`
		Accounts []map[string]any   `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || got.Summary["requests"] != 2 || got.Summary["totalTokens"] != 50 || len(got.Accounts) != 1 {
		t.Fatalf("wrong filtered total: calls=%d %s", calls, w.Body.String())
	}
}

func TestMagpieUpstreamFailuresAreNotEmptySuccess(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, `{"error":{"message":"denied"}}`, `{}`, `{"calls":"wrong"}`} {
		t.Run(body, func(t *testing.T) {
			h := magpieAnalyticsHandler(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			w := analyticsRequest(t, h, "/api/v1/analytics/usage?range=7d")
			if w.Code != http.StatusBadGateway {
				t.Fatalf("accepted invalid response: %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, code := range []int{401, 403, 404, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			h := magpieAnalyticsHandler(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"calls":0}`))
			})
			w := analyticsRequest(t, h, "/api/v1/analytics/usage?range=7d")
			if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), strconv.Itoa(code)) {
				t.Fatalf("hidden failure: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

// Cache traffic dominates long-context usage and must be included everywhere.
func TestMagpieCacheHeavyTotals(t *testing.T) {
	for _, surface := range []string{"all", "hermes"} {
		t.Run(surface, func(t *testing.T) {
			h := magpieAnalyticsHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/requests") {
					_, _ = w.Write([]byte(`{"rows":[{"t":"2026-10-15T12:00:00+08:00","provider":"codex","model":"cached-model","providerAccount":"fixture-account","in":20000000,"out":2000000,"cache_read":280000000,"cache_write":1000000,"reasoning":1000000,"priced":true}],"offset":0,"total":1}`))
				} else {
					_, _ = w.Write([]byte(`{"calls":1,"input":20000000,"output":2000000,"cache_read":280000000,"cache_write":1000000,"reasoning":1000000,"series":[{"time":"2026-10-15T00:00:00+08:00","calls":1,"input":20000000,"output":2000000,"cache_read":280000000,"cache_write":1000000}],"models":[{"provider":"codex","model":"cached-model","calls":1,"input":20000000,"output":2000000,"cache_read":280000000,"cache_write":1000000}],"accounts":[{"provider":"codex","account":"fixture-account","calls":1,"input":20000000,"output":2000000,"cache_read":280000000,"cache_write":1000000}]}`))
				}
			})
			w := analyticsRequest(t, h, "/api/v1/analytics/usage?range=7d&surface="+surface)
			if w.Code != 200 {
				t.Fatalf("status %d", w.Code)
			}
			var got struct {
				Summary   map[string]float64 `json:"summary"`
				Days      []map[string]any   `json:"days"`
				Models    []map[string]any   `json:"models"`
				Providers []map[string]any   `json:"providers"`
				Accounts  []map[string]any   `json:"accounts"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Summary["totalTokens"] != 303000000 || got.Summary["inputTokens"] != 20000000 || got.Summary["requests"] != 1 {
				t.Fatalf("cache-heavy summary: %v", got.Summary)
			}
			for name, rows := range map[string][]map[string]any{"days": got.Days, "models": got.Models, "providers": got.Providers, "accounts": got.Accounts} {
				if len(rows) != 1 || rows[0]["totalTokens"] != float64(303000000) {
					t.Errorf("%s: %v", name, rows)
				}
			}
			if got.Models[0]["shareRatio"] != float64(1) || got.Providers[0]["shareRatio"] != float64(1) {
				t.Fatal("cache-inclusive shares must sum to one")
			}
		})
	}
}

func TestMagpieNativeUsageSummary(t *testing.T) {
	h := magpieAnalyticsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/magpie/usage" || r.URL.Query().Get("period") != "7d" || r.Header.Get("Authorization") != "Bearer fixture-gateway-key" || r.URL.Query().Has("k") {
			t.Errorf("unexpected upstream request: path=%s query=%s", r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"period":"7d","since":"2026-10-09T00:00:00+08:00","calls":3,"errors":1,"input":100,"output":20,"cache_read":80,"cache_write":10,"reasoning":5,"cost":0.12,"unpriced":1,"series":[{"time":"2026-10-15T00:00:00+08:00","calls":3,"input":100,"output":20,"cache_read":80,"cost":0.12}],"models":[{"id":"codex/gpt-test","provider":"codex","model":"gpt-test","calls":3,"input":100,"output":20,"cache_read":80,"cache_write":10,"cost":0.12,"unpriced":1}],"accounts":[{"id":"codex/test","provider":"codex","account":"fixture-account","calls":3,"input":100,"output":20,"cache_read":80,"cost":0.12}]}`))
	})
	w := analyticsRequest(t, h, "/api/v1/analytics/usage?range=7d&surface=all")
	if w.Code != 200 {
		t.Fatalf("usage: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		GatewayType string             `json:"gatewayType"`
		Summary     map[string]float64 `json:"summary"`
		Days        []struct {
			Date  string `json:"date"`
			Total int    `json:"totalTokens"`
		} `json:"days"`
		Models    []map[string]any `json:"models"`
		Providers []map[string]any `json:"providers"`
		Accounts  []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.GatewayType != "magpie" || got.Summary["requests"] != 3 || got.Summary["totalTokens"] != 210 || got.Summary["cacheHitRate"] != 80.0/190.0 || got.Summary["errors"] != 1 {
		t.Fatalf("wrong summary: %+v", got)
	}
	if len(got.Days) != 1 || got.Days[0].Date != "2026-10-15" || got.Days[0].Total != 200 || len(got.Models) != 1 || len(got.Providers) != 1 || len(got.Accounts) != 1 {
		t.Fatalf("missing breakdowns: %+v", got)
	}
	// Native summaries cannot establish per-request metering coverage.
	if _, ok := got.Summary["measuredRequests"]; ok {
		t.Fatal("invented measurement count")
	}
}
