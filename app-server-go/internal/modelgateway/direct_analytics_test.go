package modelgateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type stubDirectAnalyticsDaemon struct {
	stubRuntimeQueryServer
	sessiondv1.UnimplementedModelConfigServer
	plan func(context.Context, *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error)
}

func (s *stubDirectAnalyticsDaemon) Plan(ctx context.Context, req *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
	if s.plan != nil {
		return s.plan(ctx, req)
	}
	return &sessiondv1.ModelConfigPlanResponse{}, nil
}

func TestDirectUsageTabAndMagpieMergeWithoutDoubleCounting(t *testing.T) {
	nowSlot := time.Now().Unix() / 900
	daemon := &stubDirectAnalyticsDaemon{
		stubRuntimeQueryServer: stubRuntimeQueryServer{
			queryNative: func(ctx context.Context, req *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
				if req.GetAntigravityUsageSummary() == nil {
					t.Fatalf("expected AntigravityUsageSummary query, got %T", req.Query)
				}
				raw, _ := json.Marshal(map[string]any{
					"provider":        "antigravity",
					"active_sessions": 2,
					"slots": []map[string]any{
						{
							"slot_15m":    nowSlot,
							"model":       "gemini-4-carbon-high",
							"calls":       5,
							"input":       10000,
							"output":      2000,
							"cache_read":  88000,
							"cache_write": 0,
							"reasoning":   800,
						},
					},
					"sessions": []map[string]any{
						{
							"session_id":  "52794208-5f2e-4120-a6b1-99e5ee29c7f1",
							"title":       "ACP 改造与统计",
							"workspace":   "/home/luwei/workspace/glwlg/astrorder",
							"model":       "gemini-4-carbon-high",
							"updated_at":  time.Now().UTC().Format(time.RFC3339),
							"calls":       5,
							"input":       10000,
							"output":      2000,
							"cache_read":  88000,
							"cache_write": 0,
							"reasoning":   800,
							"total":       100000,
						},
					},
				})
				return &sessiondv1.NativeQueryResponse{
					Target: req.Target,
					Result: &sessiondv1.NativeJson{Schema: "antigravity.usage.summary", Utf8Json: raw},
				}, nil
			},
		},
		plan: func(ctx context.Context, req *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
			return &sessiondv1.ModelConfigPlanResponse{
				CurrentFiles: []*sessiondv1.ModelConfigFileState{
					{
						// Codex IS routed through the Magpie gateway -> must NOT be counted as direct!
						File:    sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG,
						Content: []byte(`model_provider = "magpie"` + "\n" + `[model_providers.magpie]` + "\n" + `base_url = "http://127.0.0.1:19999/v1"`),
					},
					{
						// Grok is NOT pointing to gateway -> direct login!
						File:    sessiondv1.ManagedFile_MANAGED_FILE_GROK_CONFIG,
						Content: []byte(`{"baseURL":"https://api.x.ai/v1"}`),
					},
				},
			}, nil
		},
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(srv, daemon)
	sessiondv1.RegisterRuntimeQueryServer(srv, daemon)
	sessiondv1.RegisterModelConfigServer(srv, daemon)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	dc, err := daemonclient.Dial(context.Background(), daemonclient.Config{
		Target:         lis.Addr().String(),
		Credential:     "test-cred",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "direct.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	magpieSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"period":  "today",
			"bucket":  "hour",
			"since":   time.Now().UTC().Format(time.RFC3339),
			"calls":   10,
			"input":   20000,
			"output":  5000,
			"models":  []map[string]any{{"provider": "codex", "model": "gpt-5.6-sol", "calls": 10, "input": 20000, "output": 5000}},
			"series":  []map[string]any{{"time": time.Now().UTC().Format(time.RFC3339), "calls": 10, "input": 20000, "output": 5000}},
			"accounts": []map[string]any{},
		})
	}))
	defer magpieSrv.Close()

	// Point Codex config check at magpieSrv's URL so Codex is recognized as gateway-routed.
	daemon.plan = func(ctx context.Context, req *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
		return &sessiondv1.ModelConfigPlanResponse{
			CurrentFiles: []*sessiondv1.ModelConfigFileState{
				{
					File:    sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG,
					Content: []byte(`base_url = "` + magpieSrv.URL + `/v1"`),
				},
				{
					File:    sessiondv1.ManagedFile_MANAGED_FILE_GROK_CONFIG,
					Content: []byte(`{"baseURL":"https://api.x.ai/v1"}`),
				},
			},
		}, nil
	}

	ctx := context.Background()
	if _, err := SaveGatewayConfig(ctx, st, map[string]any{
		"gateway_type":  "magpie",
		"inference_url": magpieSrv.URL + "/v1",
	}); err != nil {
		t.Fatal(err)
	}

	for _, ag := range []map[string]any{
		{"id": "local-antigravity", "kind": "antigravity", "name": "Antigravity", "status": "ready"},
		{"id": "local-codex", "kind": "codex", "name": "Codex", "status": "ready"},
		{"id": "local-grok", "kind": "grok", "name": "Grok", "status": "ready"},
	} {
		if _, err := st.UpsertAgent(ctx, ag); err != nil {
			t.Fatal(err)
		}
	}

	todayStr := time.Now().In(time.Local).Format("2006-01-02")
	// Codex has 999,999 tokens in local token_metrics, but MUST be excluded because Codex routes via Magpie!
	if _, err := st.UpsertTokenMetric(ctx, map[string]any{
		"agent_id":     "local-codex",
		"session_id":   "codex-s1",
		"model":        "native",
		"date":         todayStr,
		"input_tokens": int64(900000),
		"output_tokens": int64(99999),
		"total_tokens": int64(999999),
	}); err != nil {
		t.Fatal(err)
	}
	// Grok is direct (not pointing at Magpie) -> 5,000 tokens MUST be included!
	if _, err := st.UpsertTokenMetric(ctx, map[string]any{
		"agent_id":     "local-grok",
		"session_id":   "grok-s1",
		"model":        "grok-4.6",
		"provider":     "grok",
		"date":         todayStr,
		"input_tokens": int64(4000),
		"output_tokens": int64(1000),
		"total_tokens": int64(5000),
	}); err != nil {
		t.Fatal(err)
	}

	rawHandler, err := NewHandler(Options{Store: st, Client: dc, HTTPClient: magpieSrv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	h := rawHandler.(*Handler)

	// 1. Direct-only endpoint (?source=direct) includes Antigravity (100k) + direct Grok (5k) = 105k, excluding gateway Codex!
	wDirect := httptest.NewRecorder()
	h.handleGetAnalyticsUsage(wDirect, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/usage?source=direct&range=today&surface=all", nil))
	if wDirect.Code != http.StatusOK {
		t.Fatalf("direct status=%d body=%s", wDirect.Code, wDirect.Body.String())
	}
	var dirResp map[string]any
	if err := json.Unmarshal(wDirect.Body.Bytes(), &dirResp); err != nil {
		t.Fatal(err)
	}
	sum := dirResp["summary"].(map[string]any)
	if sum["totalTokens"] != float64(105000) || sum["requests"] != float64(6) {
		t.Fatalf("expected 105000 direct tokens (100k agy + 5k grok, 0 codex), got: %s", wDirect.Body.String())
	}
	if len(dirResp["directAgents"].([]any)) != 2 || len(dirResp["directSessions"].([]any)) != 2 {
		t.Fatalf("expected 2 direct agents and 2 direct sessions, got: %s", wDirect.Body.String())
	}

	// 2. Combined endpoint (used by Sidebar TodayUsagePreview) merges Magpie (25k) + Direct (105k) = 130k!
	wMerged := httptest.NewRecorder()
	h.handleGetAnalyticsUsage(wMerged, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/usage?range=today&surface=all", nil))
	if wMerged.Code != http.StatusOK {
		t.Fatalf("merged status=%d body=%s", wMerged.Code, wMerged.Body.String())
	}
	var mergedResp map[string]any
	_ = json.Unmarshal(wMerged.Body.Bytes(), &mergedResp)
	mergedSum := mergedResp["summary"].(map[string]any)
	if mergedSum["totalTokens"] != float64(130000) || mergedSum["requests"] != float64(16) {
		t.Fatalf("expected 130000 merged tokens (25k magpie + 105k direct), got: %s", wMerged.Body.String())
	}
}
