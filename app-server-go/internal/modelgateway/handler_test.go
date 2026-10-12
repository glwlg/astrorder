package modelgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type mockModelConfigServer struct {
	sessiondv1.UnimplementedModelConfigServer
	planFunc   func(context.Context, *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error)
	applyFunc  func(context.Context, *sessiondv1.ModelConfigApplyRequest) (*sessiondv1.ModelConfigApplyResponse, error)
	reloadFunc func(context.Context, *sessiondv1.ModelConfigReloadRequest) (*sessiondv1.ModelConfigReloadResponse, error)
}

func (m *mockModelConfigServer) Plan(ctx context.Context, req *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
	if m.planFunc != nil {
		return m.planFunc(ctx, req)
	}
	return &sessiondv1.ModelConfigPlanResponse{
		Target:        req.Target,
		TargetSupport: sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED,
		CurrentFiles:  []*sessiondv1.ModelConfigFileState{},
	}, nil
}

func (m *mockModelConfigServer) Apply(ctx context.Context, req *sessiondv1.ModelConfigApplyRequest) (*sessiondv1.ModelConfigApplyResponse, error) {
	if m.applyFunc != nil {
		return m.applyFunc(ctx, req)
	}
	return &sessiondv1.ModelConfigApplyResponse{
		OperationId:    "op-1",
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		TargetSupport:  sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED,
	}, nil
}

func (m *mockModelConfigServer) Reload(ctx context.Context, req *sessiondv1.ModelConfigReloadRequest) (*sessiondv1.ModelConfigReloadResponse, error) {
	if m.reloadFunc != nil {
		return m.reloadFunc(ctx, req)
	}
	return &sessiondv1.ModelConfigReloadResponse{
		OperationId:    "op-2",
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
	}, nil
}

type mockPublisher struct{}

func (m *mockPublisher) Publish(ctx context.Context, eventType string, payload map[string]any) error {
	return nil
}

func setupTestEnvironment(t *testing.T, mockServer *mockModelConfigServer) (*store.Store, *daemonclient.Client, *auth.Authenticator, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	// Setup mock gRPC daemon
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok || len(md.Get("authorization")) == 0 || md.Get("authorization")[0] != "Bearer test-cred" {
				// return unauthenticated if needed
			}
			return handler(ctx, req)
		}),
	)
	if mockServer == nil {
		mockServer = &mockModelConfigServer{}
	}
	sessiondv1.RegisterModelConfigServer(grpcSrv, mockServer)

	mockDaemonControl := &mockDaemonControlServer{}
	sessiondv1.RegisterDaemonControlServer(grpcSrv, mockDaemonControl)
	go grpcSrv.Serve(lis)

	clientCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dc, err := daemonclient.Dial(clientCtx, daemonclient.Config{
		Target:         lis.Addr().String(),
		Credential:     "test-cred",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("daemonclient.Dial: %v", err)
	}

	authorizer := auth.New(config.Config{BrowserSecret: "browser-secret"}, nil)

	cleanup := func() {
		_ = dc.Close()
		grpcSrv.Stop()
		_ = lis.Close()
		_ = st.Close()
	}

	return st, dc, authorizer, cleanup
}

type mockDaemonControlServer struct {
	sessiondv1.UnimplementedDaemonControlServer
}

func (m *mockDaemonControlServer) GetCapabilities(ctx context.Context, req *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor: 1,
		ProtocolMinor: 0,
		DaemonId:      "mock-daemon",
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

func TestModelGatewayConfigEndpoints(t *testing.T) {
	st, dc, authz, cleanup := setupTestEnvironment(t, nil)
	defer cleanup()

	handler, err := modelgateway.NewHandler(modelgateway.Options{
		Store:      st,
		Client:     dc,
		Authorizer: authz,
		Publisher:  &mockPublisher{},
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	// 1. Unauthorized GET /api/v1/analytics/config
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// 2. Authorized GET default config
	req = httptest.NewRequest(http.MethodGet, "/api/v1/analytics/config", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var pubCfg modelgateway.PublicGatewayConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &pubCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pubCfg.GatewayType != "opencodex" {
		t.Fatalf("expected opencodex, got %s", pubCfg.GatewayType)
	}

	// 3. PUT update config with credentials
	putBody := `{"gateway_type":"opencodex","api_key":"sk-secret-token-123456"}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/analytics/config", strings.NewReader(putBody))
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var updatedPub modelgateway.PublicGatewayConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &updatedPub); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(updatedPub.MaskedKey, "...") && updatedPub.MaskedKey != "已配置" {
		t.Fatalf("expected masked key, got %s", updatedPub.MaskedKey)
	}
	// Verify raw secret is NOT leaked
	if strings.Contains(rec.Body.String(), "sk-secret-token-123456") {
		t.Fatalf("secret leaked in public response!")
	}

	// 4. Verify ResolverConfig reads internal decrypted credential
	resCfg, err := modelgateway.ResolverConfig(context.Background(), st)
	if err != nil {
		t.Fatalf("ResolverConfig: %v", err)
	}
	if resCfg["api_key"] != "sk-secret-token-123456" {
		t.Fatalf("expected resolved api_key 'sk-secret-token-123456', got %v", resCfg["api_key"])
	}
}

func TestModelGatewaySyncPreviewAndJobs(t *testing.T) {
	st, dc, authz, cleanup := setupTestEnvironment(t, nil)
	defer cleanup()

	// Upstream test server simulating OpenCodeX /api/models and /api/providers
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models":
			models := []map[string]any{
				{
					"namespaced":       "openai/gpt-4o",
					"id":               "gpt-4o",
					"provider":         "openai",
					"contextWindow":    128000,
					"maxInputTokens":   120000,
					"reasoningEfforts": []string{"low", "medium", "high"},
				},
			}
			_ = json.NewEncoder(w).Encode(models)
		case "/api/providers":
			providers := []map[string]any{
				{"name": "openai", "disabled": false},
			}
			_ = json.NewEncoder(w).Encode(providers)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	// Update gateway config to point to upstream test server
	_, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{
		"gateway_type":   "opencodex",
		"management_url": upstream.URL,
		"inference_url":  upstream.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("save gateway config: %v", err)
	}

	handler, err := modelgateway.NewHandler(modelgateway.Options{
		Store:      st,
		Client:     dc,
		Authorizer: authz,
		Publisher:  &mockPublisher{},
		HTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	// 1. GET /api/v1/model-sync/targets
	req := httptest.NewRequest(http.MethodGet, "/api/v1/model-sync/targets", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// 2. POST /api/v1/model-sync/preview
	previewPayload := map[string]any{
		"targets": []map[string]any{
			{"target_id": "local", "agents": []string{"codex"}},
		},
	}
	pBytes, _ := json.Marshal(previewPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/model-sync/preview", bytes.NewReader(pBytes))
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var previewResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &previewResp); err != nil {
		t.Fatalf("unmarshal preview: %v", err)
	}
	if previewResp["model_count"].(float64) != 1 {
		t.Fatalf("expected 1 model, got %v", previewResp["model_count"])
	}

	// 3. POST /api/v1/model-sync/jobs
	req = httptest.NewRequest(http.MethodPost, "/api/v1/model-sync/jobs", bytes.NewReader(pBytes))
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start job expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var jobStart map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &jobStart); err != nil {
		t.Fatalf("unmarshal job start: %v", err)
	}
	opID := jobStart["id"].(string)

	// Wait briefly for job to run
	time.Sleep(100 * time.Millisecond)

	// 4. GET /api/v1/model-sync/jobs
	req = httptest.NewRequest(http.MethodGet, "/api/v1/model-sync/jobs", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get jobs expected 200, got %d", rec.Code)
	}

	var jobsList map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &jobsList); err != nil {
		t.Fatalf("unmarshal jobs: %v", err)
	}
	items := jobsList["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected job items, got 0")
	}

	// 5. POST cancel
	req = httptest.NewRequest(http.MethodPost, "/api/v1/model-sync/jobs/"+opID+"/cancel", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel job expected 200, got %d", rec.Code)
	}
}

func TestModelGatewayMatches(t *testing.T) {
	cases := []struct {
		method string
		path   string
		match  bool
	}{
		{http.MethodGet, "/api/v1/analytics/config", true},
		{http.MethodPut, "/api/v1/analytics/config", true},
		{http.MethodPost, "/api/v1/analytics/config", false},
		{http.MethodGet, "/api/v1/analytics/usage", true},
		{http.MethodGet, "/api/v1/analytics/model-quota", true},
		{http.MethodGet, "/api/v1/model-sync/targets", true},
		{http.MethodPost, "/api/v1/model-sync/preview", true},
		{http.MethodPost, "/api/v1/model-sync/jobs", true},
		{http.MethodGet, "/api/v1/model-sync/jobs", true},
		{http.MethodPost, "/api/v1/model-sync/jobs/123/cancel", true},
		{http.MethodGet, "/api/v1/sessions/s-1/usage", true},
		{http.MethodPost, "/api/v1/sessions/s-1/usage", false},
		{http.MethodGet, "/api/v1/unknown", false},
	}

	for _, c := range cases {
		if got := modelgateway.Matches(c.method, c.path); got != c.match {
			t.Errorf("Matches(%s, %s) = %v, expected %v", c.method, c.path, got, c.match)
		}
	}
}

func TestListJobsDoesNotLeakNilLiteralForMissingError(t *testing.T) {
	st, _, _, cleanup := setupTestEnvironment(t, nil)
	defer cleanup()

	ctx := context.Background()
	stored := []map[string]any{
		{"id": "model-sync-1", "status": "success", "created_at": "2026-10-10T03:00:00Z", "updated_at": "2026-10-10T03:00:01Z", "targets": []any{}},
		{"id": "model-sync-2", "status": "failed", "error": "real failure", "created_at": "2026-10-10T03:01:00Z", "updated_at": "2026-10-10T03:01:01Z", "targets": []any{}},
	}
	if err := st.SetPreference(ctx, "model_sync:jobs", stored, true); err != nil {
		t.Fatalf("seed jobs: %v", err)
	}

	jobs, err := modelgateway.ListJobsForTest(ctx, st)
	if err != nil {
		t.Fatalf("listJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Error != "" {
		t.Fatalf("missing error must map to empty string, got %q (UI would show literal <nil>)", jobs[0].Error)
	}
	if jobs[1].Error != "real failure" {
		t.Fatalf("real error must be preserved, got %q", jobs[1].Error)
	}
	if jobs[0].CatalogFingerprint != "" {
		t.Fatalf("missing fingerprint must map to empty string, got %q", jobs[0].CatalogFingerprint)
	}
}

func TestModelGatewayPreviewSerializesEmptyDiffAsArrays(t *testing.T) {
	st, dc, authz, cleanup := setupTestEnvironment(t, nil)
	defer cleanup()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"namespaced": "openai/gpt-4o", "id": "gpt-4o", "provider": "openai"},
			})
		case "/api/providers":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "openai", "disabled": false}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	if _, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{
		"gateway_type":   "opencodex",
		"management_url": upstream.URL,
		"inference_url":  upstream.URL + "/v1",
	}); err != nil {
		t.Fatalf("save gateway config: %v", err)
	}

	handler, err := modelgateway.NewHandler(modelgateway.Options{
		Store:      st,
		Client:     dc,
		Authorizer: authz,
		Publisher:  &mockPublisher{},
		HTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	pBytes, _ := json.Marshal(map[string]any{
		"targets": []map[string]any{{"target_id": "local", "agents": []string{"codex"}}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/model-sync/preview", bytes.NewReader(pBytes))
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"removed":[]`) {
		t.Fatalf("model_diff.removed must serialize as [] not null: %s", body)
	}
	if !strings.Contains(body, `"diff"`) {
		t.Fatalf("changes must include the unified diff field: %s", body)
	}

	var resp struct {
		Targets []struct {
			ModelDiff map[string]any `json:"model_diff"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal preview: %v", err)
	}
	if len(resp.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(resp.Targets))
	}
	if resp.Targets[0].ModelDiff["removed"] == nil {
		t.Fatalf("model_diff.removed decoded as nil (JSON null): %s", body)
	}
}
