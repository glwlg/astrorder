package modelgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/modelgateway"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestModelGatewayRealMetaAndServiceContractObligations(t *testing.T) {
	var capturedApplyMeta *sessiondv1.CommandMeta
	var capturedApplyReq *sessiondv1.ModelConfigApplyRequest
	var capturedReloadMeta *sessiondv1.CommandMeta

	mockServer := &mockModelConfigServer{
		planFunc: func(ctx context.Context, req *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
			home := "/home/testuser"
			return &sessiondv1.ModelConfigPlanResponse{
				Home:          &home,
				Target:        req.Target,
				TargetSupport: sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED,
				CurrentFiles: []*sessiondv1.ModelConfigFileState{
					{
						File:   sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG,
						Sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					},
				},
			}, nil
		},
		applyFunc: func(ctx context.Context, req *sessiondv1.ModelConfigApplyRequest) (*sessiondv1.ModelConfigApplyResponse, error) {
			t.Logf("Apply called: meta=%+v files=%d", req.Meta, len(req.Files))
			capturedApplyMeta = req.Meta
			capturedApplyReq = req
			if req.Meta == nil || req.Meta.OperationId == "" {
				return nil, status.Error(codes.InvalidArgument, "durable operation identity is required")
			}
			if req.Meta.Canonicalization != sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
				return nil, status.Error(codes.InvalidArgument, "unsupported canonicalization")
			}
			return &sessiondv1.ModelConfigApplyResponse{
				OperationId:    req.Meta.OperationId,
				OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
				Changed:        []sessiondv1.ManagedFile{sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG},
			}, nil
		},
		reloadFunc: func(ctx context.Context, req *sessiondv1.ModelConfigReloadRequest) (*sessiondv1.ModelConfigReloadResponse, error) {
			capturedReloadMeta = req.Meta
			if req.Meta == nil || req.Meta.OperationId == "" {
				return nil, status.Error(codes.InvalidArgument, "durable operation identity is required")
			}
			if req.Meta.Canonicalization != sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
				return nil, status.Error(codes.InvalidArgument, "unsupported canonicalization")
			}
			return &sessiondv1.ModelConfigReloadResponse{
				OperationId:    req.Meta.OperationId,
				OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
				Reloaded:       req.Agents,
			}, nil
		},
	}

	st, dc, authz, cleanup := setupTestEnvironment(t, mockServer)
	defer cleanup()

	// Upstream HTTP test server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models":
			models := []map[string]any{
				{"namespaced": "openai/gpt-4o", "id": "gpt-4o", "provider": "openai", "contextWindow": 128000},
			}
			_ = json.NewEncoder(w).Encode(models)
		case "/api/providers":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "openai"}})
		case "/api/combos":
			_ = json.NewEncoder(w).Encode(map[string]any{"combos": []any{}})
		case "/api/provider-quotas":
			_ = json.NewEncoder(w).Encode(map[string]any{"reports": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	_, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{
		"gateway_type":   "opencodex",
		"management_url": upstream.URL,
		"inference_url":  upstream.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("save gateway config: %v", err)
	}

	rawHandler, err := modelgateway.NewHandler(modelgateway.Options{
		Store:      st,
		Client:     dc,
		Authorizer: authz,
		Publisher:  &mockPublisher{},
		HTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	// Verify handler implements Close(context.Context) error
	closer, ok := rawHandler.(interface{ Close(context.Context) error })
	if !ok {
		t.Fatalf("handler does not implement Close(context.Context) error")
	}

	// Trigger job start
	pBytes, _ := json.Marshal(map[string]any{
		"targets": []map[string]any{
			{"target_id": "local", "agents": []string{"codex"}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/model-sync/jobs", bytes.NewReader(pBytes))
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec := httptest.NewRecorder()
	rawHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start job failed: %s", rec.Body.String())
	}

	var startResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &startResp)
	opID := startResp["id"].(string)

	// Wait for job to complete
	time.Sleep(300 * time.Millisecond)

	// Graceful close waits for background drain
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelClose()
	if err := closer.Close(closeCtx); err != nil {
		t.Fatalf("closer.Close: %v", err)
	}

	// Verify captured Meta
	if capturedApplyMeta == nil || capturedApplyMeta.OperationId == "" {
		t.Fatalf("applyMeta was not provided or empty")
	}
	if !strings.HasPrefix(capturedApplyMeta.OperationId, opID) {
		t.Fatalf("expected apply operation_id prefix %s, got %s", opID, capturedApplyMeta.OperationId)
	}
	if capturedApplyMeta.Canonicalization != sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
		t.Fatalf("expected legacy canonicalization, got %v", capturedApplyMeta.Canonicalization)
	}

	// Verify file input expected SHA
	if capturedApplyReq == nil || len(capturedApplyReq.Files) == 0 {
		t.Fatalf("capturedApplyReq files missing")
	}
	for _, f := range capturedApplyReq.Files {
		if f.File == sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG {
			if f.ExpectedSha256 == nil || *f.ExpectedSha256 != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
				t.Fatalf("expected plan hash carried into apply request, got %v", f.ExpectedSha256)
			}
		}
	}

	if capturedReloadMeta == nil || capturedReloadMeta.OperationId == "" {
		t.Fatalf("reloadMeta was not provided or empty")
	}
	if !strings.HasPrefix(capturedReloadMeta.OperationId, opID) {
		t.Fatalf("expected reload operation_id prefix %s, got %s", opID, capturedReloadMeta.OperationId)
	}
}
