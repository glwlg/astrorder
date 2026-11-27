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

type stubRuntimeQueryServer struct {
	sessiondv1.UnimplementedDaemonControlServer
	sessiondv1.UnimplementedRuntimeQueryServer
	queryNative func(context.Context, *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error)
}

type fixtureRemoteExecutable struct{ path string }

func (f fixtureRemoteExecutable) Resolve(context.Context, string, map[string]any) (string, error) {
	return f.path, nil
}

func (s *stubRuntimeQueryServer) GetCapabilities(context.Context, *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor: 1,
		ProtocolMinor: 0,
		DaemonId:      "stub-daemon",
		Services: []string{
			"astrorder.sessiond.v1.ConnectorIngress",
			"astrorder.sessiond.v1.DaemonControl",
			"astrorder.sessiond.v1.EventStream",
			"astrorder.sessiond.v1.ModelConfig",
			"astrorder.sessiond.v1.OperationQuery",
			"astrorder.sessiond.v1.RuntimeQuery",
			"astrorder.sessiond.v1.SessionControl",
			"astrorder.sessiond.v1.TerminalStream",
		},
		CanonicalizationVersions: []sessiondv1.CanonicalizationVersion{
			sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
		},
		MaxWireBytes:       sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		MaxBusinessBytes:   sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		EventStreamVersion: sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:    true,
	}, nil
}

func (s *stubRuntimeQueryServer) GetStatus(context.Context, *sessiondv1.GetStatusRequest) (*sessiondv1.GetStatusResponse, error) {
	return &sessiondv1.GetStatusResponse{DaemonId: "stub-daemon"}, nil
}

func (s *stubRuntimeQueryServer) QueryNative(ctx context.Context, req *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
	return s.queryNative(ctx, req)
}

func setupModelGatewayWithRuntime(t *testing.T, rt *stubRuntimeQueryServer) (*Handler, *store.Store, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(srv, rt)
	sessiondv1.RegisterRuntimeQueryServer(srv, rt)
	go func() { _ = srv.Serve(lis) }()

	dc, err := daemonclient.Dial(context.Background(), daemonclient.Config{
		Target:         lis.Addr().String(),
		Credential:     "test-cred",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		srv.Stop()
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		dc.Close()
		srv.Stop()
		t.Fatal(err)
	}
	raw, err := NewHandler(Options{
		Store:      st,
		Client:     dc,
		HTTPClient: &http.Client{Timeout: time.Second},
	})
	if err != nil {
		st.Close()
		dc.Close()
		srv.Stop()
		t.Fatal(err)
	}
	cleanup := func() {
		st.Close()
		dc.Close()
		srv.Stop()
	}
	return raw.(*Handler), st, cleanup
}

func TestAntigravityNativeModelQuotaPausedByDefault(t *testing.T) {
	var seenQuotaQueries int
	rt := &stubRuntimeQueryServer{
		queryNative: func(ctx context.Context, req *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
			switch req.Query.(type) {
			case *sessiondv1.NativeQueryRequest_AntigravityQuotaRead:
				seenQuotaQueries++
				t.Fatalf("AntigravityQuotaRead should not be called while paused")
				return nil, nil
			case *sessiondv1.NativeQueryRequest_AntigravitySessionUsage:
				raw, _ := json.Marshal(map[string]any{
					"context_available": true,
					"provider":          "antigravity",
					"model":             "gemini-4-neon",
					"last_input_tokens": int64(10000),
					"context_window":    int64(1048576),
					"input_tokens":      int64(10000),
					"output_tokens":     int64(500),
					"total_tokens":      int64(10500),
				})
				return &sessiondv1.NativeQueryResponse{
					Target: req.Target,
					Result: &sessiondv1.NativeJson{Schema: "antigravity.session.usage", Utf8Json: raw},
				}, nil
			default:
				t.Fatalf("unexpected query: %T", req.Query)
				return nil, nil
			}
		},
	}

	h, st, cleanup := setupModelGatewayWithRuntime(t, rt)
	defer cleanup()

	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{
		"id":            "local-antigravity",
		"kind":          "antigravity",
		"name":          "Antigravity · 本机",
		"connection_id": "local",
		"status":        "ready",
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	h.handleGetModelQuota(w, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/model-quota?model=antigravity%2Fgemini-4-neon&agent_id=local-antigravity", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 paused payload, got %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["paused"] != true || len(resp["accounts"].([]any)) != 0 || seenQuotaQueries != 0 {
		t.Fatalf("expected paused=true with 0 accounts and 0 daemon calls, got resp=%+v calls=%d", resp, seenQuotaQueries)
	}

	// Local SQLite session context/token usage remains enabled.
	wUsage := httptest.NewRecorder()
	h.handleGetSessionUsage(wUsage, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/usage?agent_id=local-antigravity", nil))
	if wUsage.Code != http.StatusOK {
		t.Fatalf("expected session usage to remain available: %d %s", wUsage.Code, wUsage.Body.String())
	}
}

func TestMagpieDoublePrefixUnwrapAndAntigravityNativeFallback(t *testing.T) {
	prevPaused := antigravityNativeQuotaPaused
	antigravityNativeQuotaPaused = false
	t.Cleanup(func() { antigravityNativeQuotaPaused = prevPaused })
	var seenQuotaQueries int
	var seenSSHTarget bool
	rt := &stubRuntimeQueryServer{
		queryNative: func(ctx context.Context, req *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
			switch q := req.Query.(type) {
			case *sessiondv1.NativeQueryRequest_AntigravityQuotaRead:
				seenQuotaQueries++
				if req.Target.GetRuntime() == sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH && req.Target.GetSsh().GetAntigravityExecutable() == "/opt/fixture/agy" {
					seenSSHTarget = true
				}
				raw, _ := json.Marshal(map[string]any{
					"provider":  "antigravity",
					"plan":      "Google AI Ultra",
					"plan_name": "Pro",
					"as_of":     "2026-11-18T03:00:00Z",
					"models": []map[string]any{
						{
							"id":                 "gemini-4-carbon-high",
							"label":              "Gemini 4 Carbon (High)",
							"remaining_fraction": 0.8,
							"used_percent":       20.0,
							"reset_at":           int64(1794978363000),
						},
					},
				})
				return &sessiondv1.NativeQueryResponse{
					Target: req.Target,
					Result: &sessiondv1.NativeJson{Schema: "antigravity.quota.read", Utf8Json: raw},
				}, nil
			case *sessiondv1.NativeQueryRequest_AntigravitySessionUsage:
				_ = q
				raw, _ := json.Marshal(map[string]any{
					"context_available": true,
					"provider":          "antigravity",
					"model":             "gemini-4-carbon",
					"last_input_tokens": int64(25600),
					"context_window":    int64(512000),
					"input_tokens":      int64(40000),
					"cached_tokens":     int64(30000),
					"output_tokens":     int64(1200),
					"reasoning_tokens":  int64(500),
					"total_tokens":      int64(41200),
				})
				return &sessiondv1.NativeQueryResponse{
					Target: req.Target,
					Result: &sessiondv1.NativeJson{Schema: "antigravity.session.usage", Utf8Json: raw},
				}, nil
			default:
				t.Fatalf("unexpected query: %T", req.Query)
				return nil, nil
			}
		},
	}

	h, st, cleanup := setupModelGatewayWithRuntime(t, rt)
	defer cleanup()
	h.remoteExecutable = &fixtureRemoteExecutable{path: "/opt/fixture/agy"}

	ctx := context.Background()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{
		"id":   "debian-1",
		"host": "192.0.2.100",
		"port": 22,
		"user": "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertAgent(ctx, map[string]any{
		"id":            "ssh-antigravity-debian-1",
		"kind":          "antigravity",
		"name":          "Antigravity · Debian",
		"connection_id": "debian-1",
		"status":        "ready",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Even if Magpie upstream fails / is unused, native Antigravity quota fallback works.
	w := httptest.NewRecorder()
	h.handleGetModelQuota(w, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/model-quota?model=antigravity%2Fgemini-4-carbon&agent_id=ssh-antigravity-debian-1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("native quota fallback status=%d body=%s", w.Code, w.Body.String())
	}
	var quotaResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &quotaResp); err != nil {
		t.Fatal(err)
	}
	if !seenSSHTarget || seenQuotaQueries != 1 {
		t.Fatalf("expected SSH target with custom agy executable, seenSSH=%v calls=%d", seenSSHTarget, seenQuotaQueries)
	}
	accts, _ := quotaResp["accounts"].([]any)
	if len(accts) != 1 {
		t.Fatalf("expected 1 native account quota entry, got: %s", w.Body.String())
	}
	acct := accts[0].(map[string]any)
	if acct["plan"] != "Google AI Ultra" || acct["last"] != false {
		t.Fatalf("unexpected account quota fields: %+v", acct)
	}
	windows := acct["quota"].(map[string]any)["customWindows"].([]any)
	if len(windows) != 1 || windows[0].(map[string]any)["percent"] != float64(20) {
		t.Fatalf("unexpected customWindows: %+v", windows)
	}

	// 2. Magpie double-prefix unwrap for antigravity/claude/<model>.
	magpieUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"provider": "claude",
					"name":     "Claude",
					"user":     "claude@example.com",
					"plan":     "max",
					"windows":  []map[string]any{{"name": "5h", "used": 33}},
				},
			},
		})
	}))
	defer magpieUpstream.Close()
	if _, err := SaveGatewayConfig(ctx, st, map[string]any{
		"gateway_type":  "magpie",
		"inference_url": magpieUpstream.URL + "/v1",
	}); err != nil {
		t.Fatal(err)
	}
	w2 := httptest.NewRecorder()
	h.handleGetModelQuota(w2, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/model-quota?model=antigravity%2Fclaude%2Fclaude-opus-4-6", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("double-prefix unwrap failed: %d %s", w2.Code, w2.Body.String())
	}
	var unwrappedResp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &unwrappedResp)
	if len(unwrappedResp["accounts"].([]any)) != 1 {
		t.Fatalf("expected claude quota account after antigravity/claude/ unwrap: %s", w2.Body.String())
	}

	// 3. handleGetSessionUsage fetches native Antigravity session usage and persists it.
	sessID := "55555555-5555-4555-8555-555555555555"
	w3 := httptest.NewRecorder()
	h.handleGetSessionUsage(w3, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessID+"/usage?agent_id=ssh-antigravity-debian-1", nil))
	if w3.Code != http.StatusOK {
		t.Fatalf("session usage status=%d body=%s", w3.Code, w3.Body.String())
	}
	var usageResp map[string]any
	_ = json.Unmarshal(w3.Body.Bytes(), &usageResp)
	if usageResp["context_available"] != true || usageResp["last_input_tokens"] != float64(25600) || usageResp["context_window"] != float64(512000) {
		t.Fatalf("expected native session usage with context_available=true, got: %s", w3.Body.String())
	}
	if usageResp["used_percentage"] != float64(5) || usageResp["cache_hit_rate"] != float64(75) {
		t.Fatalf("unexpected computed usage percentages: %s", w3.Body.String())
	}
}

func TestAntigravityNativeQuotaGroupsMapping(t *testing.T) {
	prevPaused := antigravityNativeQuotaPaused
	antigravityNativeQuotaPaused = false
	t.Cleanup(func() { antigravityNativeQuotaPaused = prevPaused })
	rt := &stubRuntimeQueryServer{
		queryNative: func(ctx context.Context, req *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
			if _, ok := req.Query.(*sessiondv1.NativeQueryRequest_AntigravityQuotaRead); !ok {
				t.Fatalf("unexpected query: %T", req.Query)
			}
			raw, _ := json.Marshal(map[string]any{
				"provider":  "antigravity",
				"source":    "agy_cli_usage",
				"plan":      "Google AI Pro",
				"plan_name": "Google AI Pro",
				"as_of":     "2026-11-21T01:48:00Z",
				"groups": []map[string]any{
					{
						"name":        "Gemini Models",
						"description": "Models within this group: Gemini 4 Neon, Gemini 4 Hydrogen, Orchestrator (Neon), Gemini 3.8 Flash",
						"buckets": []map[string]any{
							{"id": "gemini-weekly", "name": "Weekly Limit Remaining", "window": "weekly", "used_percent": 68.4, "reset_at": int64(1795701655000)},
							{"id": "gemini-5h", "name": "Five Hour Limit Remaining", "window": "5h", "used_percent": 14.4, "reset_at": int64(1795242622000)},
						},
					},
					{
						"name":        "Claude and GPT models",
						"description": "Models within this group: Claude Opus 5.5, Claude Sonnet 5.5, GPT-OSS 120B",
						"buckets": []map[string]any{
							{"id": "3p-weekly", "name": "Weekly Limit Remaining", "window": "weekly", "used_percent": 0.0},
							{"id": "3p-5h", "name": "Five Hour Limit Remaining", "window": "5h", "used_percent": 0.0},
						},
					},
					{
						"name":        "Gemini 4 Argon",
						"description": "For paid Pro plans, Argon quota is separate.",
						"buckets": []map[string]any{
							{"id": "gemini-argon-wtu-weekly", "name": "Gemini 4 Argon Weekly Limit", "window": "weekly", "used_percent": 26.6, "reset_at": int64(1795761975000)},
						},
					},
				},
			})
			return &sessiondv1.NativeQueryResponse{
				Target: req.Target,
				Result: &sessiondv1.NativeJson{Schema: "antigravity.quota.read", Utf8Json: raw},
			}, nil
		},
	}

	h, st, cleanup := setupModelGatewayWithRuntime(t, rt)
	defer cleanup()

	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{
		"id":            "local-antigravity",
		"kind":          "antigravity",
		"name":          "Antigravity · 本机",
		"connection_id": "local",
		"status":        "ready",
	}); err != nil {
		t.Fatal(err)
	}

	query := func(model string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		h.handleGetModelQuota(w, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/model-quota?model="+model+"&agent_id=local-antigravity", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// 1. Gemini 4 Neon -> Gemini 模型组 (both 5h + weekly windows & reset timestamps)
	neon := query("antigravity%2Fgemini-4-neon-high")
	neonAccts := neon["accounts"].([]any)
	if len(neonAccts) != 1 {
		t.Fatalf("expected 1 neon account, got %+v", neon)
	}
	neonAcct := neonAccts[0].(map[string]any)
	if neonAcct["logLabel"] != "Antigravity · 本机 · Gemini 模型组" || neonAcct["plan"] != "Google AI Pro" {
		t.Fatalf("unexpected neon account metadata: %+v", neonAcct)
	}
	neonQ := neonAcct["quota"].(map[string]any)
	if neonQ["shortPercent"] != 14.4 || neonQ["weeklyPercent"] != 68.4 || neonQ["shortResetAt"] == nil || neonQ["weeklyResetAt"] == nil {
		t.Fatalf("unexpected neon windows: %+v", neonQ)
	}

	// 2. Gemini 4 Argon -> Gemini 4 Argon standalone weekly bucket
	argon := query("antigravity%2Fgemini-4-argon-high")
	argonAccts := argon["accounts"].([]any)
	if len(argonAccts) != 1 {
		t.Fatalf("expected 1 argon account, got %+v", argon)
	}
	argonAcct := argonAccts[0].(map[string]any)
	if argonAcct["logLabel"] != "Antigravity · 本机 · Gemini 4 Argon" {
		t.Fatalf("unexpected argon label: %+v", argonAcct)
	}
	argonQ := argonAcct["quota"].(map[string]any)
	if argonQ["weeklyPercent"] != 26.6 || argonQ["weeklyResetAt"] == nil {
		t.Fatalf("unexpected argon quota: %+v", argonQ)
	}

	// 3. Claude Opus 5.5 -> Claude / GPT 模型组 (0% used, no 1970 resetAt)
	claude := query("antigravity%2Fclaude-opus-5-5")
	claudeAccts := claude["accounts"].([]any)
	if len(claudeAccts) != 1 {
		t.Fatalf("expected 1 claude account, got %+v", claude)
	}
	claudeAcct := claudeAccts[0].(map[string]any)
	claudeQ := claudeAcct["quota"].(map[string]any)
	if claudeAcct["logLabel"] != "Antigravity · 本机 · Claude / GPT 模型组" || claudeQ["shortPercent"] != float64(0) || claudeQ["weeklyPercent"] != float64(0) {
		t.Fatalf("unexpected claude quota: %+v", claudeAcct)
	}
	if _, exists := claudeQ["shortResetAt"]; exists {
		t.Fatalf("unexpected resetAt on full sentinel bucket: %+v", claudeQ)
	}

	// 4. Unknown future model -> all 3 groups returned so nothing is hidden
	unknown := query("antigravity%2Funknown-future-model")
	if len(unknown["accounts"].([]any)) != 3 {
		t.Fatalf("expected all 3 groups for unknown model fallback, got %+v", unknown)
	}
}
