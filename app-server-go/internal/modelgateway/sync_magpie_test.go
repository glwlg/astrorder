package modelgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/modelgateway"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// magpiePreviewEnv wires a magpie-mode handler whose daemon plan reports the
// given current file contents.
func magpiePreviewEnv(t *testing.T, currentFiles map[sessiondv1.ManagedFile]string) (http.Handler, func()) {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":               "antigravity/gemini-3.8-flash",
					"owned_by":         "antigravity",
					"context_window":   500000,
					"supported_reasoning_levels": []map[string]any{
						{"effort": "low"}, {"effort": "medium"}, {"effort": "high"},
					},
				},
			},
		})
	}))

	mockServer := &mockModelConfigServer{
		planFunc: func(ctx context.Context, req *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
			home := "/home/testuser"
			resp := &sessiondv1.ModelConfigPlanResponse{
				Home:          &home,
				Target:        req.Target,
				TargetSupport: sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED,
			}
			for file, content := range currentFiles {
				c := content
				resp.CurrentFiles = append(resp.CurrentFiles, &sessiondv1.ModelConfigFileState{
					File:    file,
					Content: []byte(c),
				})
			}
			return resp, nil
		},
	}

	st, dc, authz, cleanup := setupTestEnvironment(t, mockServer)

	if _, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{
		"gateway_type":  "magpie",
		"inference_url": upstream.URL + "/v1",
	}); err != nil {
		upstream.Close()
		cleanup()
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
		upstream.Close()
		cleanup()
		t.Fatalf("new handler: %v", err)
	}

	return handler, func() {
		upstream.Close()
		cleanup()
	}
}

func previewModelSync(t *testing.T, handler http.Handler) map[string]any {
	t.Helper()
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
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal preview: %v", err)
	}
	return resp
}

func findChange(t *testing.T, preview map[string]any, file string) map[string]any {
	t.Helper()
	targets := preview["targets"].([]any)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	changes := targets[0].(map[string]any)["changes"].([]any)
	for _, c := range changes {
		m := c.(map[string]any)
		if m["file"] == file {
			return m
		}
	}
	names := make([]string, 0, len(changes))
	for _, c := range changes {
		names = append(names, c.(map[string]any)["file"].(string))
	}
	t.Fatalf("no change entry for %q; got %v", file, names)
	return nil
}

// In magpie mode the codex catalog must be written to the magpie managed file
// (magpie-catalog.json) and config.toml must reference that same path.
func TestModelSyncPreviewMagpieWritesMagpieCatalog(t *testing.T) {
	handler, cleanup := magpiePreviewEnv(t, map[sessiondv1.ManagedFile]string{
		sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG: "model_provider = \"opencodex\"\nmodel_catalog_json = \"/home/testuser/.codex/opencodex-catalog.json\"\n",
	})
	defer cleanup()

	preview := previewModelSync(t, handler)
	body, _ := json.Marshal(preview)

	catalogChange := findChange(t, preview, "codex_magpie_catalog")
	diff, _ := catalogChange["diff"].(string)
	if !strings.Contains(diff, `"slug"`) {
		t.Fatalf("magpie catalog diff must contain slug field:\n%s", diff)
	}
	if strings.Contains(body2str(preview), "default_reasoning_effort") {
		t.Fatalf("stale default_reasoning_effort must not be written: %s", body)
	}

	cfgChange := findChange(t, preview, "codex_config")
	cfgDiff, _ := cfgChange["diff"].(string)
	if !strings.Contains(cfgDiff, "magpie-catalog.json") {
		t.Fatalf("config.toml must reference magpie-catalog.json:\n%s", cfgDiff)
	}

	// No change entry may target the opencodex catalog file in magpie mode.
	for _, c := range preview["targets"].([]any)[0].(map[string]any)["changes"].([]any) {
		if c.(map[string]any)["file"] == "codex_catalog" {
			t.Fatalf("magpie mode must not write codex_catalog (opencodex-catalog.json)")
		}
	}

	// model_diff must be computed from slug, not from the legacy id field.
	modelDiff := preview["targets"].([]any)[0].(map[string]any)["model_diff"].(map[string]any)
	added := modelDiff["added"].([]any)
	if len(added) != 1 || added[0] != "antigravity/gemini-3.8-flash" {
		t.Fatalf("model_diff.added wrong: %v", modelDiff)
	}
	if modelDiff["kept_count"] != float64(0) {
		t.Fatalf("model_diff.kept_count wrong: %v", modelDiff)
	}
}

// A previous magpie catalog written with slug fields must merge (kept_count)
// and provide the baseline for the diff.
func TestModelSyncPreviewMagpieKeepsPreviousSlugs(t *testing.T) {
	handler, cleanup := magpiePreviewEnv(t, map[sessiondv1.ManagedFile]string{
		sessiondv1.ManagedFile_MANAGED_FILE_CODEX_MAGPIE_CATALOG: `{"models":[{"slug":"antigravity/gemini-3.8-flash","custom":true}]}`,
	})
	defer cleanup()

	preview := previewModelSync(t, handler)
	modelDiff := preview["targets"].([]any)[0].(map[string]any)["model_diff"].(map[string]any)
	if modelDiff["kept_count"] != float64(1) {
		t.Fatalf("previous magpie slug must count as kept: %v", modelDiff)
	}
	added := modelDiff["added"].([]any)
	if len(added) != 0 {
		t.Fatalf("no new models expected: %v", modelDiff)
	}

	catalogChange := findChange(t, preview, "codex_magpie_catalog")
	diff, _ := catalogChange["diff"].(string)
	if !strings.Contains(diff, `"custom": true`) {
		t.Fatalf("previous custom field must survive into rendered catalog:\n%s", diff)
	}
}

// OpenCodeX mode keeps the existing behavior: codex_catalog + opencodex path.
func TestModelSyncPreviewOpenCodeXKeepsOpencodexCatalog(t *testing.T) {
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

	mockServer := &mockModelConfigServer{
		planFunc: func(ctx context.Context, req *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
			home := "/home/testuser"
			return &sessiondv1.ModelConfigPlanResponse{
				Home:          &home,
				Target:        req.Target,
				TargetSupport: sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED,
			}, nil
		},
	}
	st, dc, authz, cleanup := setupTestEnvironment(t, mockServer)
	defer cleanup()

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

	preview := previewModelSync(t, handler)
	catalogChange := findChange(t, preview, "codex_catalog")
	diff, _ := catalogChange["diff"].(string)
	if !strings.Contains(diff, `"slug"`) {
		t.Fatalf("opencodex catalog diff must contain slug field:\n%s", diff)
	}
	cfgChange := findChange(t, preview, "codex_config")
	cfgDiff, _ := cfgChange["diff"].(string)
	if !strings.Contains(cfgDiff, "opencodex-catalog.json") {
		t.Fatalf("opencodex mode must reference opencodex-catalog.json:\n%s", cfgDiff)
	}
}

func body2str(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
