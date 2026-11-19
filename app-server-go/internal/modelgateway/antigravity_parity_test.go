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

func TestMagpieDoublePrefixUnwrapAndAntigravityNativeFallback(t *testing.T) {
	var seenQuotaQueries int
	var seenSSHTarget bool
	rt := &stubRuntimeQueryServer{
		queryNative: func(ctx context.Context, req *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
			switch q := req.Query.(type) {
			case *sessiondv1.NativeQueryRequest_AntigravityQuotaRead:
				seenQuotaQueries++
				if req.Target.GetRuntime() == sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH && req.Target.GetSsh().GetAntigravityExecutable() == "/home/luwei/.local/bin/agy" {
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

	ctx := context.Background()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{
		"id":   "debian-1",
		"host": "192.168.1.100",
		"port": 22,
		"user": "luwei",
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
