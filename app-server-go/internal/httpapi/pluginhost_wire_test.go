package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestHTTPAPIPluginHostWiringAndGitLabBuiltin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	handler, err := NewHandlerChecked(Options{
		Context: ctx,
		Config: config.Config{
			Listen:        "127.0.0.1:0",
			DatabasePath:  dbPath,
			BrowserSecret: "test-browser-secret",
		},
		Store: st,
	})
	if err != nil {
		t.Fatalf("NewHandlerChecked: %v", err)
	}

	// 1. GET /api/v1/extensions exposes registered builtin gitlab-ci-viewer plugin.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/extensions", nil)
	req.Header.Set("Authorization", "Bearer test-browser-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/extensions status=%d body=%s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Items []struct {
			ID          string `json:"id"`
			Source      string `json:"source"`
			Enabled     bool   `json:"enabled"`
			BackendType string `json:"backend_type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode extensions: %v", err)
	}
	foundGitLab := false
	for _, item := range listResp.Items {
		if item.ID == "gitlab-ci-viewer" && item.Source == "builtin" && item.Enabled {
			foundGitLab = true
		}
	}
	if !foundGitLab {
		t.Fatalf("expected enabled builtin gitlab-ci-viewer in /api/v1/extensions, got %+v", listResp.Items)
	}

	// 2. GET /api/v1/ext/_sdk/bridge.js serves the iframe bridge SDK.
	sdkReq := httptest.NewRequest(http.MethodGet, "/api/v1/ext/_sdk/bridge.js", nil)
	sdkRec := httptest.NewRecorder()
	handler.ServeHTTP(sdkRec, sdkReq)
	if sdkRec.Code != http.StatusOK || !strings.Contains(sdkRec.Body.String(), "astrorderPlugin") {
		t.Fatalf("GET bridge.js status=%d body_len=%d", sdkRec.Code, sdkRec.Body.Len())
	}

	// 3. GET /api/v1/ext/gitlab-ci-viewer/status-pill returns declarative pill JSON.
	pillReq := httptest.NewRequest(http.MethodGet, "/api/v1/ext/gitlab-ci-viewer/status-pill?workspace=/nonexistent", nil)
	pillReq.Header.Set("Authorization", "Bearer test-browser-secret")
	pillRec := httptest.NewRecorder()
	handler.ServeHTTP(pillRec, pillReq)
	if pillRec.Code != http.StatusOK {
		t.Fatalf("GET status-pill status=%d body=%s", pillRec.Code, pillRec.Body.String())
	}

	// 4. GET /api/v1/agent/catalog exposes ext.list and ext.gitlab-ci-viewer.pipelines.
	catReq := httptest.NewRequest(http.MethodGet, "/api/v1/agent/catalog", nil)
	catReq.Header.Set("Authorization", "Bearer test-browser-secret")
	catRec := httptest.NewRecorder()
	handler.ServeHTTP(catRec, catReq)
	if catRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/agent/catalog status=%d body=%s", catRec.Code, catRec.Body.String())
	}
	if !strings.Contains(catRec.Body.String(), `"ext.list"`) || !strings.Contains(catRec.Body.String(), `"ext.gitlab-ci-viewer.pipelines"`) {
		t.Fatalf("expected dynamic extension capabilities in catalog, got %s", catRec.Body.String())
	}

	// 5. POST /api/v1/agent/invoke with capability="ext.list" succeeds.
	invokeBody := bytes.NewBufferString(`{"capability":"ext.list","payload":{}}`)
	invReq := httptest.NewRequest(http.MethodPost, "/api/v1/agent/invoke", invokeBody)
	invReq.Header.Set("Authorization", "Bearer test-browser-secret")
	invReq.Header.Set("Content-Type", "application/json")
	invRec := httptest.NewRecorder()
	handler.ServeHTTP(invRec, invReq)
	if invRec.Code != http.StatusOK || !strings.Contains(invRec.Body.String(), `"gitlab-ci-viewer"`) {
		t.Fatalf("POST ext.list status=%d body=%s", invRec.Code, invRec.Body.String())
	}
}
