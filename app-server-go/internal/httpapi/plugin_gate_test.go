package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/botgroups"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/settingsapi"
	"astrorder.dev/app-server/internal/store"
)

func TestPluginGateMiddlewareStandalone(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	wrapped := PluginGateMiddleware(st, PluginBackups)(next)

	// Enabled by default.
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/backups/targets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 by default, got %d", rec.Code)
	}

	// Disable -> 403 with plugin_id JSON payload.
	if err := st.SetPreference(ctx, store.PrefPluginsEnabled, map[string]any{PluginBackups: false}, true); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/v1/backups/targets", nil))
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when disabled, got %d %s", rec2.Code, rec2.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 403 body: %v", err)
	}
	if body["detail"] != "插件已停用" || body["plugin_id"] != PluginBackups {
		t.Fatalf("unexpected 403 body: %+v", body)
	}

	// Re-enable -> 200 immediately.
	if err := st.SetPreference(ctx, store.PrefPluginsEnabled, map[string]any{PluginBackups: true}, true); err != nil {
		t.Fatal(err)
	}
	rec3 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/api/v1/backups/targets", nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 after re-enable, got %d", rec3.Code)
	}
}

func TestHTTPAPIBuiltinPluginGatingSettingsToggleAndExtensionChangedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "app.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	officialDir := filepath.Join(tmp, "official")
	esmDir := filepath.Join(officialDir, "sample-esm")
	if err := os.MkdirAll(filepath.Join(esmDir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(esmDir, "plugin.json"), []byte(`{
		"schema_version": 1,
		"id": "sample-esm",
		"name": "Sample ESM",
		"version": "1.0.0",
		"default_enabled": false,
		"backend": {"type": "none"},
		"frontend": {"mode": "esm", "entry": "ui/entry.js"}
	}`), 0o644)
	_ = os.WriteFile(filepath.Join(esmDir, "ui", "entry.js"), []byte(`export function activate(){}`), 0o644)

	cfg := config.Config{
		Listen:           "127.0.0.1:0",
		DatabasePath:     dbPath,
		DefaultWorkspace: tmp,
		BrowserSecret:    "gate-secret",
	}
	authenticator := auth.New(cfg, nil)
	hub := application.NewEventHub(st)
	eventCh, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	settingsH := settingsapi.NewHandler(settingsapi.Options{
		Store:      st,
		Authorizer: authenticator,
	})
	modelH, err := modelgateway.NewHandler(modelgateway.Options{
		Store:      st,
		Authorizer: authenticator,
	})
	if err != nil {
		t.Fatal(err)
	}
	connH, err := connections.NewHandler(connections.Options{
		Store:      st,
		Authorizer: authenticator,
	})
	if err != nil {
		t.Fatal(err)
	}
	groupSvc, err := botgroups.New(ctx, botgroups.Options{
		Store:      st,
		Controller: groupAgents{},
		Authorizer: authenticator,
		Publisher:  hub,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer groupSvc.Close()

	h, err := NewHandlerChecked(Options{
		Context:            ctx,
		Config:             cfg,
		Store:              st,
		EventHub:           hub,
		Settings:           settingsH,
		ModelGateway:       modelH,
		Connections:        connH,
		BotGroups:          groupSvc,
		OfficialPluginsDir: officialDir,
	})
	if err != nil {
		t.Fatal(err)
	}

	call := func(method, path string, body string) *httptest.ResponseRecorder {
		t.Helper()
		var rdr *bytes.Reader
		if body != "" {
			rdr = bytes.NewReader([]byte(body))
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rdr)
		req.Header.Set("Authorization", "Bearer gate-secret")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	gatedRoutes := []struct {
		pluginID string
		path     string
	}{
		{PluginLearningRadar, "/api/v1/learning/overview"},
		{PluginBackups, "/api/v1/backups/targets"},
		{PluginDigest, "/api/v1/digest/history"},
		{PluginMeetings, "/api/v1/meetings"},
		{PluginGroups, "/api/v1/bot-groups"},
		{PluginAnalyticsGateway, "/api/v1/analytics/config"},
		{PluginAgentAccounts, "/api/v1/agy-accounts"},
		{PluginIntegrations, "/api/v1/integrations"},
	}

	// 1. All 8 routes work by default.
	for _, tc := range gatedRoutes {
		rec := call(http.MethodGet, tc.path, "")
		if rec.Code == http.StatusForbidden {
			t.Fatalf("expected %s (%s) enabled by default, got 403: %s", tc.pluginID, tc.path, rec.Body.String())
		}
	}

	// 2. Disable all 8 via PUT /api/v1/plugins/settings (must work and invalidate gate immediately!).
	disablePayload := map[string]any{
		"enabled": map[string]bool{
			PluginLearningRadar:    false,
			PluginBackups:          false,
			PluginDigest:           false,
			PluginMeetings:         false,
			PluginGroups:           false,
			PluginAnalyticsGateway: false,
			PluginAgentAccounts:    false,
			PluginIntegrations:     false,
		},
	}
	rawDisable, _ := json.Marshal(disablePayload)
	putRec := call(http.MethodPut, "/api/v1/plugins/settings", string(rawDisable))
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT /api/v1/plugins/settings failed: %d %s", putRec.Code, putRec.Body.String())
	}

	// 3. Every gated route now returns 403 with detail + plugin_id!
	for _, tc := range gatedRoutes {
		rec := call(http.MethodGet, tc.path, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for disabled %s (%s), got %d %s", tc.pluginID, tc.path, rec.Code, rec.Body.String())
		}
		var errBody map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("decode 403 for %s: %v", tc.pluginID, err)
		}
		if errBody["detail"] != "插件已停用" || errBody["plugin_id"] != tc.pluginID {
			t.Fatalf("unexpected 403 body for %s: %+v", tc.pluginID, errBody)
		}
	}

	// Core non-account connections route MUST stay accessible even when workspace-agent-accounts is disabled!
	connRec := call(http.MethodGet, "/api/v1/connections", "")
	if connRec.Code != http.StatusOK {
		t.Fatalf("expected /api/v1/connections to stay ungated, got %d %s", connRec.Code, connRec.Body.String())
	}

	// Core chat session context usage route MUST stay accessible even when workspace-analytics-gateway is disabled!
	sessUsageRec := call(http.MethodGet, "/api/v1/sessions/sess-1/usage", "")
	if sessUsageRec.Code == http.StatusForbidden {
		t.Fatalf("expected /api/v1/sessions/{id}/usage to stay ungated when %s is disabled, got 403: %s", PluginAnalyticsGateway, sessUsageRec.Body.String())
	}

	// Model-sync route owned by workspace-analytics-gateway MUST be gated with 403!
	syncRec := call(http.MethodGet, "/api/v1/model-sync/targets", "")
	if syncRec.Code != http.StatusForbidden {
		t.Fatalf("expected /api/v1/model-sync/targets to be gated with 403, got %d %s", syncRec.Code, syncRec.Body.String())
	}

	// Settings GET MUST remain accessible so user can inspect and re-enable plugins!
	getSettingsRec := call(http.MethodGet, "/api/v1/plugins/settings", "")
	if getSettingsRec.Code != http.StatusOK {
		t.Fatalf("expected GET /api/v1/plugins/settings to remain 200, got %d", getSettingsRec.Code)
	}

	// 4. Re-enable via PUT /api/v1/plugins/settings -> immediate cache invalidation restores access!
	enablePayload := map[string]any{
		"enabled": map[string]bool{
			PluginBackups:       true,
			PluginIntegrations:  true,
			PluginLearningRadar: true,
		},
	}
	rawEnable, _ := json.Marshal(enablePayload)
	if rec := call(http.MethodPut, "/api/v1/plugins/settings", string(rawEnable)); rec.Code != http.StatusOK {
		t.Fatalf("re-enable PUT failed: %d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/v1/backups/targets", "/api/v1/integrations", "/api/v1/learning/overview"} {
		rec := call(http.MethodGet, path, "")
		if rec.Code == http.StatusForbidden {
			t.Fatalf("expected %s to recover immediately after re-enable, got 403", path)
		}
	}

	// 5. Enabling an official ESM plugin triggers extension.changed event via EventHub!
	enRec := call(http.MethodPost, "/api/v1/extensions/sample-esm/enable", `{"enabled":true}`)
	if enRec.Code != http.StatusOK {
		t.Fatalf("enable sample-esm: %d %s", enRec.Code, enRec.Body.String())
	}
	deadline := time.After(2 * time.Second)
	foundExtChanged := false
	for !foundExtChanged {
		select {
		case ev := <-eventCh:
			if ev.Type == "extension.changed" && ev.Data["plugin_id"] == "sample-esm" && ev.Data["action"] == "enabled" {
				foundExtChanged = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for extension.changed event for sample-esm")
		}
	}
}
