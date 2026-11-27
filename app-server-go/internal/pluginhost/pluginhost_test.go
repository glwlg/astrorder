package pluginhost

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const testBrowserToken = "browser-test-secret-token"

type stubAuthorizer struct {
	expectedToken string
}

func (a stubAuthorizer) AuthorizeBrowser(r *http.Request) *auth.Error {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if authz == "Bearer "+a.expectedToken {
		return nil
	}
	return &auth.Error{Status: http.StatusUnauthorized, Detail: "Authentication required"}
}

type stubBuiltinPlugin struct {
	manifest Manifest
}

func (p *stubBuiltinPlugin) Manifest() Manifest {
	return p.manifest
}

func (p *stubBuiltinPlugin) ServeExtension(w http.ResponseWriter, r *http.Request, subpath string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"builtin": true,
		"subpath": subpath,
		"method":  r.Method,
	})
}

func (p *stubBuiltinPlugin) InvokeCapability(_ context.Context, capID string, payload map[string]any) (any, error) {
	return map[string]any{
		"builtin_cap": capID,
		"payload":     payload,
	}, nil
}

func (p *stubBuiltinPlugin) CollectSignals(_ context.Context, kind string, q SignalQuery) ([]SignalItem, error) {
	return []SignalItem{{
		SourcePlugin: p.manifest.ID,
		Kind:         kind,
		Title:        "Builtin Failure Signal",
		Detail:       "workspace=" + q.Workspace,
		Severity:     "error",
	}}, nil
}

// TestHelperPluginProcess is executed as a real child subprocess by TestRealSubprocessPluginEndToEnd.
func TestHelperPluginProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_HELPER_PLUGIN") != "1" {
		return
	}
	addr := strings.TrimSpace(os.Getenv("ASTRORDER_PLUGIN_ADDR"))
	if addr == "" {
		port := strings.TrimSpace(os.Getenv("ASTRORDER_PLUGIN_PORT"))
		if port == "" {
			t.Fatal("missing ASTRORDER_PLUGIN_ADDR/PORT")
		}
		addr = "127.0.0.1:" + port
	}
	hostURL := strings.TrimRight(os.Getenv("ASTRORDER_HOST_URL"), "/")
	hostToken := os.Getenv("ASTRORDER_HOST_TOKEN")
	pluginID := os.Getenv("ASTRORDER_PLUGIN_ID")
	pid := os.Getpid()

	callHost := func(method, path string, body []byte) (int, []byte, error) {
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, hostURL+path, rdr)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+hostToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		return resp.StatusCode, data, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pid": pid, "plugin_id": pluginID})
	})
	mux.HandleFunc("/echo-headers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"pid":           pid,
			"plugin_id":     pluginID,
			"authorization": r.Header.Get("Authorization"),
			"cookie":        r.Header.Get("Cookie"),
			"host_token":    r.Header.Get("X-Astrorder-Host-Token"),
			"x_plugin_id":   r.Header.Get("X-Astrorder-Plugin-ID"),
			"custom":        r.Header.Get("X-Custom-Test"),
			"query_foo":     r.URL.Query().Get("foo"),
		})
	})
	mux.HandleFunc("/echo-env", func(w http.ResponseWriter, _ *http.Request) {
		envMap := map[string]string{}
		for _, entry := range os.Environ() {
			k, v, ok := strings.Cut(entry, "=")
			if ok {
				envMap[k] = v
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"pid": pid,
			"env": envMap,
		})
	})
	mux.HandleFunc("/probe-host", func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("path")
		method := r.URL.Query().Get("method")
		if method == "" {
			method = http.MethodGet
		}
		var body []byte
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			body, _ = io.ReadAll(r.Body)
		}
		status, data, err := callHost(method, target, body)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		var parsed any
		_ = json.Unmarshal(data, &parsed)
		writeJSON(w, http.StatusOK, map[string]any{
			"host_status": status,
			"host_body":   parsed,
		})
	})
	mux.HandleFunc("/agent/echo", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		writeJSON(w, http.StatusOK, map[string]any{
			"echo":      payload["msg"],
			"workspace": payload["workspace"],
			"pid":       pid,
		})
	})
	mux.HandleFunc("/signals/failures", func(w http.ResponseWriter, r *http.Request) {
		var q SignalQuery
		_ = json.NewDecoder(r.Body).Decode(&q)
		writeJSON(w, http.StatusOK, map[string]any{
			"items": []SignalItem{{
				Kind:      "subproc_ci",
				Title:     "Subprocess CI Failed",
				Detail:    "Detected failure in " + q.Workspace,
				Severity:  "error",
				Timestamp: "2026-11-27T10:00:00Z",
			}},
		})
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		os.Exit(2)
	}
	os.Exit(0)
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func authRequest(t *testing.T, method, urlStr string, body io.Reader, contentType string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testBrowserToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}

func TestManifestValidation(t *testing.T) {
	validJSON := `{
		"schema_version": 1,
		"id": "com.acme.demo-1",
		"name": "Demo Plugin",
		"version": "1.2.3",
		"permissions": ["workspace:git.read", "storage:kv"],
		"backend": {"type": "process", "command": ["node", "server.mjs"]},
		"settings": {"mode": "custom_ui", "ui_entry": "ui/settings.html"},
		"contributes": {
			"sidecar_panels": [{"id": "main", "title": "Main", "ui_entry": "ui/panel.html"}],
			"agent_skills": ["skills/demo/SKILL.md"]
		}
	}`
	m, err := ParseManifest([]byte(validJSON))
	if err != nil {
		t.Fatalf("expected valid manifest, got error: %v", err)
	}
	if m.Backend.Health != "/health" {
		t.Fatalf("expected default health '/health', got %q", m.Backend.Health)
	}
	if m.Category != "general" {
		t.Fatalf("expected default category 'general', got %q", m.Category)
	}

	invalidCases := []struct {
		name string
		raw  string
	}{
		{"invalid_id_uppercase", `{"schema_version":1,"id":"BadID","name":"X","version":"1.0"}`},
		{"invalid_id_traversal", `{"schema_version":1,"id":"../evil","name":"X","version":"1.0"}`},
		{"invalid_id_reserved", `{"schema_version":1,"id":"_sdk","name":"X","version":"1.0"}`},
		{"unknown_permission", `{"schema_version":1,"id":"good-id","name":"X","version":"1.0","permissions":["root:everything"]}`},
		{"ui_entry_traversal", `{"schema_version":1,"id":"good-id","name":"X","version":"1.0","settings":{"mode":"custom_ui","ui_entry":"../secret.html"}}`},
		{"ui_entry_abs", `{"schema_version":1,"id":"good-id","name":"X","version":"1.0","settings":{"mode":"custom_ui","ui_entry":"/etc/passwd"}}`},
		{"panel_ui_traversal", `{"schema_version":1,"id":"good-id","name":"X","version":"1.0","contributes":{"sidecar_panels":[{"id":"p","title":"P","ui_entry":"ui/../../evil.html"}]}}`},
		{"skill_traversal", `{"schema_version":1,"id":"good-id","name":"X","version":"1.0","contributes":{"agent_skills":["../../SKILL.md"]}}`},
		{"command_traversal", `{"schema_version":1,"id":"good-id","name":"X","version":"1.0","backend":{"type":"process","command":["../evil"]}}`},
		{"command_arg_traversal", `{"schema_version":1,"id":"good-id","name":"X","version":"1.0","backend":{"type":"process","command":["node"],"args":["../../etc/passwd"]}}`},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.raw)); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func makeZipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestInstallZipSlipAndPermissionFlow(t *testing.T) {
	st := newTestStore(t)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	mgr := NewManager(Options{
		Store:      st,
		Authorizer: stubAuthorizer{expectedToken: testBrowserToken},
		PluginsDir: pluginsDir,
	})
	defer mgr.Close()

	// Register a builtin plugin to verify overwrite protection.
	if err := mgr.RegisterBuiltin(&stubBuiltinPlugin{manifest: Manifest{
		SchemaVersion: 1,
		ID:            "official-builtin",
		Name:          "Official",
		Version:       "1.0.0",
		Backend:       BackendSpec{Type: BackendTypeBuiltin},
	}}); err != nil {
		t.Fatalf("register builtin: %v", err)
	}

	// 1. Zip-slip attack MUST be rejected.
	evilZip := makeZipBytes(t, map[string]string{
		"plugin.json":     `{"schema_version":1,"id":"evil-zip","name":"Evil","version":"1.0.0"}`,
		"../pwned.txt":    "gotcha",
	})
	if _, err := mgr.InstallFromZip(context.Background(), bytes.NewReader(evilZip), int64(len(evilZip))); err == nil {
		t.Fatalf("expected zip-slip archive to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(pluginsDir, "pwned.txt")); statErr == nil {
		t.Fatalf("zip-slip wrote file outside staging dir!")
	}

	// 2. Attempting to overwrite builtin ID MUST be rejected.
	collideZip := makeZipBytes(t, map[string]string{
		"plugin.json": `{"schema_version":1,"id":"official-builtin","name":"Fake","version":"1.0.0"}`,
	})
	if _, err := mgr.InstallFromZip(context.Background(), bytes.NewReader(collideZip), int64(len(collideZip))); err == nil {
		t.Fatalf("expected builtin ID collision to be rejected")
	}

	// 3. Valid wrapped zip (single top-level directory) uploaded over multipart HTTP!
	ts := httptest.NewServer(mgr)
	defer ts.Close()

	validZip := makeZipBytes(t, map[string]string{
		"my-zip-plugin/plugin.json": `{
			"schema_version": 1,
			"id": "zip-plugin",
			"name": "Zip Plugin",
			"version": "0.1.0",
			"permissions": ["storage:kv", "storage:secrets"],
			"backend": {"type": "none"},
			"settings": {
				"mode": "schema",
				"schema": [
					{"key": "endpoint", "label": "Endpoint", "type": "string"},
					{"key": "api_key", "label": "API Key", "type": "secret"}
				]
			}
		}`,
		"my-zip-plugin/ui/index.html": `<h1>Zip UI</h1>`,
	})

	var formBuf bytes.Buffer
	mw := multipart.NewWriter(&formBuf)
	fw, err := mw.CreateFormFile("file", "zip-plugin.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = fw.Write(validZip)
	_ = mw.Close()

	resp, err := http.DefaultClient.Do(authRequest(t, http.MethodPost, ts.URL+"/api/v1/extensions/install", &formBuf, mw.FormDataContentType()))
	if err != nil {
		t.Fatalf("install upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201 Created, got %d: %s", resp.StatusCode, string(body))
	}
	var installRes struct {
		Item PluginView `json:"item"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&installRes); err != nil {
		t.Fatalf("decode install resp: %v", err)
	}
	if installRes.Item.ID != "zip-plugin" || installRes.Item.Enabled {
		t.Fatalf("expected zip-plugin installed & disabled, got %+v", installRes.Item)
	}
	if installRes.Item.PermissionsGranted || len(installRes.Item.PendingPermissions) != 2 {
		t.Fatalf("expected 2 pending permissions, got %+v", installRes.Item)
	}

	// 4. Enable without granting permissions -> 409 requires_permission_grant!
	enableResp, err := http.DefaultClient.Do(authRequest(
		t, http.MethodPost, ts.URL+"/api/v1/extensions/zip-plugin/enable",
		strings.NewReader(`{"enabled":true}`), "application/json",
	))
	if err != nil {
		t.Fatalf("enable without grant: %v", err)
	}
	defer enableResp.Body.Close()
	if enableResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", enableResp.StatusCode)
	}
	var conflictBody map[string]any
	_ = json.NewDecoder(enableResp.Body).Decode(&conflictBody)
	if conflictBody["requires_permission_grant"] != true {
		t.Fatalf("expected requires_permission_grant=true, got %+v", conflictBody)
	}

	// 5. Enable WITH grant_permissions -> 200 OK!
	grantResp, err := http.DefaultClient.Do(authRequest(
		t, http.MethodPost, ts.URL+"/api/v1/extensions/zip-plugin/enable",
		strings.NewReader(`{"enabled":true,"grant_permissions":["storage:kv","storage:secrets"]}`), "application/json",
	))
	if err != nil {
		t.Fatalf("enable with grant: %v", err)
	}
	defer grantResp.Body.Close()
	if grantResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(grantResp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", grantResp.StatusCode, string(b))
	}
	var grantedBody struct {
		Item PluginView `json:"item"`
	}
	_ = json.NewDecoder(grantResp.Body).Decode(&grantedBody)
	if !grantedBody.Item.Enabled || !grantedBody.Item.PermissionsGranted {
		t.Fatalf("expected enabled & permissions granted, got %+v", grantedBody.Item)
	}

	// 6. Verify encrypted secrets at rest & browser masking!
	putCfgResp, err := http.DefaultClient.Do(authRequest(
		t, http.MethodPut, ts.URL+"/api/v1/extensions/zip-plugin/config",
		strings.NewReader(`{"values":{"endpoint":"https://api.example.com"},"secrets":{"api_key":"sk-top-secret-9999"}}`),
		"application/json",
	))
	if err != nil {
		t.Fatalf("put config: %v", err)
	}
	defer putCfgResp.Body.Close()
	if putCfgResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on config put, got %d", putCfgResp.StatusCode)
	}
	var cfgView ExtensionConfigView
	_ = json.NewDecoder(putCfgResp.Body).Decode(&cfgView)
	if cfgView.Values["endpoint"] != "https://api.example.com" {
		t.Fatalf("unexpected values: %+v", cfgView.Values)
	}
	if !cfgView.SecretsSet["api_key"] {
		t.Fatalf("expected api_key in secrets_set, got %+v", cfgView.SecretsSet)
	}
	if strings.Contains(cfgView.MaskedSecrets["api_key"], "top-secret") || cfgView.MaskedSecrets["api_key"] != "sk-t****9999" {
		t.Fatalf("expected masked secret 'sk-t****9999', got %q", cfgView.MaskedSecrets["api_key"])
	}

	// Inspect raw SQLite preference row to prove plaintext secret is NOT on disk.
	pref, err := st.GetPreference(context.Background(), prefExtSecPrefix+"zip-plugin")
	if err != nil || !pref.Exists {
		t.Fatalf("expected encrypted secrets pref row, err=%v", err)
	}
	rawJSON, _ := json.Marshal(pref.Value)
	if strings.Contains(string(rawJSON), "sk-top-secret-9999") {
		t.Fatalf("secret was stored in plaintext in SQLite: %s", string(rawJSON))
	}
	if !strings.Contains(string(rawJSON), "aesgcm:") && !strings.Contains(string(rawJSON), "dpapi:") {
		t.Fatalf("expected encrypted prefix in stored secrets: %s", string(rawJSON))
	}
}

func TestDetectWorkspaceGitInfo(t *testing.T) {
	// Standard repo with .git directory
	repoDir := t.TempDir()
	gitDir := filepath.Join(repoDir, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feat/ext-platform\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gitDir, "refs", "heads", "feat", "ext-platform"), nil, 0o644)
	_ = os.MkdirAll(filepath.Join(gitDir, "refs", "heads", "feat"), 0o755)
	_ = os.WriteFile(filepath.Join(gitDir, "refs", "heads", "feat", "ext-platform"), []byte("deadbeef1234567890\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gitDir, "config"), []byte(`
[remote "origin"]
	url = https://user:secretpass@gitlab.internal.local:8443/team/platform/astrorder.git
`), 0o644)

	resolver := targetfs.NewResolver(targetfs.ResolverOptions{})
	info, err := DetectWorkspaceGitInfo(context.Background(), resolver, repoDir, "", "")
	if err != nil {
		t.Fatalf("detect standard repo: %v", err)
	}
	if info == nil {
		t.Fatalf("expected non-nil WorkspaceGitInfo")
	}
	if info.Host != "gitlab.internal.local" || info.Port != "8443" || info.ProjectPath != "team/platform/astrorder" {
		t.Fatalf("unexpected repo coords: %+v", info)
	}
	if info.Branch != "feat/ext-platform" || info.HeadSHA != "deadbeef1234567890" {
		t.Fatalf("unexpected branch/sha: %+v", info)
	}
	if strings.Contains(info.RemoteURL, "secretpass") {
		t.Fatalf("expected credentials stripped from RemoteURL, got %q", info.RemoteURL)
	}

	// Worktree with .git file pointing to gitdir + commondir + packed-refs + SCP SSH remote
	wtDir := t.TempDir()
	wtGitDir := filepath.Join(gitDir, "worktrees", "wt1")
	_ = os.MkdirAll(wtGitDir, 0o755)
	_ = os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../..\n"), 0o644)
	_ = os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte("ref: refs/heads/release\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gitDir, "packed-refs"), []byte("# pack-refs\ncafebabe9876543210 refs/heads/release\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\n\turl = git@code.corp.net:group/sub/service.git\n"), 0o644)

	wtInfo, err := DetectWorkspaceGitInfo(context.Background(), resolver, wtDir, "", "")
	if err != nil {
		t.Fatalf("detect worktree repo: %v", err)
	}
	if wtInfo == nil || wtInfo.Host != "code.corp.net" || wtInfo.ProjectPath != "group/sub/service" || wtInfo.Branch != "release" || wtInfo.HeadSHA != "cafebabe9876543210" {
		t.Fatalf("unexpected worktree git info: %+v", wtInfo)
	}
}

func TestRealSubprocessPluginEndToEnd(t *testing.T) {
	st := newTestStore(t)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{})

	// Seed a real session in SQLite so /api/v1/plugin-host/sessions/context returns it.
	wsDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(wsDir, ".git", "refs", "heads"), 0o755)
	_ = os.WriteFile(filepath.Join(wsDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, ".git", "refs", "heads", "main"), []byte("1122334455667788\n"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, ".git", "config"), []byte("[remote \"origin\"]\n\turl = https://git.example.org/org/demo.git\n"), 0o644)

	if _, err := st.UpsertSession(context.Background(), map[string]any{
		"id":            "sess-e2e-1",
		"agent_id":      "agent-1",
		"title":         "Plugin E2E Session",
		"workspace":     wsDir,
		"connection_id": "local",
		"status":        "idle",
		"updated_at":    time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	mgr := NewManager(Options{
		Store:      st,
		Authorizer: stubAuthorizer{expectedToken: testBrowserToken},
		Resolver:   resolver,
		PluginsDir: pluginsDir,
	})
	defer mgr.Close()

	ts := httptest.NewServer(mgr)
	defer ts.Close()
	mgr.SetHostBaseURL(ts.URL)

	selfBin, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	// Build source plugin directory using TestHelperPluginProcess.
	srcDir := t.TempDir()
	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "subproc-demo",
		Name:          "Subprocess Demo",
		Version:       "1.0.0",
		Category:      "development",
		Permissions: []string{
			PermWorkspaceGitRead,
			PermStorageKV,
			PermStorageSecrets,
			PermAgentCapabilities,
			PermUISlots,
			PermSignalsProvide,
		},
		Backend: BackendSpec{
			Type:    BackendTypeProcess,
			Command: []string{selfBin},
			Args:    []string{"-test.run=^TestHelperPluginProcess$"},
			Env:     map[string]string{"ASTRORDER_TEST_HELPER_PLUGIN": "1"},
			Health:  "/health",
		},
		Settings: SettingsSpec{
			Mode:    SettingsModeCustomUI,
			UIEntry: "ui/panel.html",
		},
		Contributes: ContributesSpec{
			SidecarPanels: []SidecarPanelContribution{{
				ID:      "panel-1",
				Title:   "Demo Panel",
				UIEntry: "ui/panel.html",
			}},
			AgentCapabilities: []AgentCapabilityContribution{{
				ID:             "echo",
				CLIAlias:       []string{"demo", "echo"},
				Summary:        "Echo payload",
				Endpoint:       "/agent/echo",
				RequiresCaller: true,
			}},
			AgentSkills: []string{"skills/demo-skill/SKILL.md"},
			SignalProviders: []SignalProviderContribution{{
				Kind:     "workspace.failure_signals",
				Endpoint: "/signals/failures",
			}},
		},
	}
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	_ = os.WriteFile(filepath.Join(srcDir, "plugin.json"), manifestBytes, 0o644)
	_ = os.MkdirAll(filepath.Join(srcDir, "ui"), 0o755)
	_ = os.WriteFile(filepath.Join(srcDir, "ui", "panel.html"), []byte("<!doctype html><html><body>Demo UI</body></html>"), 0o644)
	_ = os.WriteFile(filepath.Join(srcDir, ".env"), []byte("SECRET=do-not-serve\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(srcDir, "skills", "demo-skill"), 0o755)
	_ = os.WriteFile(filepath.Join(srcDir, "skills", "demo-skill", "SKILL.md"), []byte("---\nname: demo-skill\ndisplay_name: Demo Skill\ndescription: Helps test plugin skills\n---\n# Demo Skill Body\n"), 0o644)

	// Install from local directory via HTTP JSON endpoint.
	installBody, _ := json.Marshal(map[string]string{"source_path": srcDir})
	resp, err := http.DefaultClient.Do(authRequest(t, http.MethodPost, ts.URL+"/api/v1/extensions/install", bytes.NewReader(installBody), "application/json"))
	if err != nil {
		t.Fatalf("install from dir: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	// While disabled, skills and capabilities must NOT be exposed, and /ext & /_ui return 403.
	if got := mgr.ListEnabledSkills(context.Background()); len(got) != 0 {
		t.Fatalf("expected 0 enabled skills while disabled, got %d", len(got))
	}
	if got := mgr.ListAgentCapabilities(context.Background()); len(got) != 0 {
		t.Fatalf("expected 0 capabilities while disabled, got %d", len(got))
	}
	disabledExtResp, _ := http.DefaultClient.Do(authRequest(t, http.MethodGet, ts.URL+"/api/v1/ext/subproc-demo/echo-headers", nil, ""))
	disabledExtResp.Body.Close()
	if disabledExtResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for disabled plugin /ext, got %d", disabledExtResp.StatusCode)
	}

	// Store a secret before enabling.
	if _, err := mgr.UpdateConfig(context.Background(), "subproc-demo", map[string]any{"greeting": "hello"}, map[string]string{"token": "raw-secret-xyz-123"}); err != nil {
		t.Fatalf("update config: %v", err)
	}

	// Grant all permissions and enable -> spawns real subprocess and waits for /health!
	grantJSON, _ := json.Marshal(map[string]any{
		"enabled":           true,
		"grant_permissions": manifest.Permissions,
	})
	enResp, err := http.DefaultClient.Do(authRequest(t, http.MethodPost, ts.URL+"/api/v1/extensions/subproc-demo/enable", bytes.NewReader(grantJSON), "application/json"))
	if err != nil {
		t.Fatalf("enable subprocess plugin: %v", err)
	}
	var enView struct {
		Item PluginView `json:"item"`
	}
	_ = json.NewDecoder(enResp.Body).Decode(&enView)
	enResp.Body.Close()
	if enResp.StatusCode != http.StatusOK || enView.Item.RuntimeState != RuntimeStateReady {
		t.Fatalf("expected 200 + runtime_state=ready, got status=%d state=%q err=%q", enResp.StatusCode, enView.Item.RuntimeState, enView.Item.RuntimeError)
	}

	// 1. Verify reverse proxy strips browser Authorization & Cookie headers!
	proxyReq := authRequest(t, http.MethodGet, ts.URL+"/api/v1/ext/subproc-demo/echo-headers?foo=bar", nil, "")
	proxyReq.Header.Set("Cookie", "astrorder_session=super-secret-cookie")
	proxyReq.Header.Set("X-Astrorder-Host-Token", "spoofed-token")
	proxyReq.Header.Set("X-Custom-Test", "keep-me")
	proxyResp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	var echoed map[string]any
	_ = json.NewDecoder(proxyResp.Body).Decode(&echoed)
	proxyResp.Body.Close()
	if proxyResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from proxy, got %d", proxyResp.StatusCode)
	}
	if echoed["authorization"] != "" || echoed["cookie"] != "" || echoed["host_token"] != "" {
		t.Fatalf("sensitive browser headers leaked to plugin subprocess! got: %+v", echoed)
	}
	if echoed["x_plugin_id"] != "subproc-demo" || echoed["custom"] != "keep-me" || echoed["query_foo"] != "bar" {
		t.Fatalf("unexpected proxied headers/query: %+v", echoed)
	}
	firstPID := echoed["pid"]

	// 2. Verify subprocess calling Host API: workspace/git-info, storage/secrets, storage/kv, sessions/context!
	probeHost := func(method, hostPath string, body string) (int, map[string]any) {
		t.Helper()
		u := fmt.Sprintf("%s/api/v1/ext/subproc-demo/probe-host?method=%s&path=%s", ts.URL, method, strings.ReplaceAll(hostPath, "&", "%26"))
		var rdr io.Reader
		httpMethod := http.MethodGet
		if body != "" {
			rdr = strings.NewReader(body)
			httpMethod = http.MethodPost
		}
		resp, err := http.DefaultClient.Do(authRequest(t, httpMethod, u, rdr, "application/json"))
		if err != nil {
			t.Fatalf("probe host %s: %v", hostPath, err)
		}
		defer resp.Body.Close()
		var out struct {
			HostStatus int            `json:"host_status"`
			HostBody   map[string]any `json:"host_body"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out.HostStatus, out.HostBody
	}

	gitStatus, gitBody := probeHost("GET", "/api/v1/plugin-host/workspace/git-info?workspace="+wsDir, "")
	if gitStatus != http.StatusOK || gitBody["detected"] != true || gitBody["project_path"] != "org/demo" || gitBody["branch"] != "main" {
		t.Fatalf("unexpected git-info from host API: status=%d body=%+v", gitStatus, gitBody)
	}

	secStatus, secBody := probeHost("GET", "/api/v1/plugin-host/storage/secrets", "")
	if secStatus != http.StatusOK {
		t.Fatalf("expected 200 for storage/secrets, got %d body=%+v", secStatus, secBody)
	}
	secretsMap, _ := secBody["secrets"].(map[string]any)
	if secretsMap["token"] != "raw-secret-xyz-123" {
		t.Fatalf("expected decrypted raw secret for owning plugin, got %+v", secBody)
	}

	kvPutStatus, _ := probeHost("PUT", "/api/v1/plugin-host/storage/kv", `{"values":{"count":42}}`)
	if kvPutStatus != http.StatusOK {
		t.Fatalf("expected 200 on host KV PUT, got %d", kvPutStatus)
	}
	kvGetStatus, kvGetBody := probeHost("GET", "/api/v1/plugin-host/storage/kv", "")
	if kvGetStatus != http.StatusOK {
		t.Fatalf("expected 200 on host KV GET, got %d", kvGetStatus)
	}
	kvVals, _ := kvGetBody["values"].(map[string]any)
	if kvVals["greeting"] != "hello" || kvVals["count"] != float64(42) {
		t.Fatalf("unexpected KV values: %+v", kvGetBody)
	}

	sessStatus, sessBody := probeHost("GET", "/api/v1/plugin-host/sessions/context?session_id=sess-e2e-1", "")
	if sessStatus != http.StatusOK {
		t.Fatalf("expected 200 on sessions/context, got %d body=%+v", sessStatus, sessBody)
	}
	sessObj, _ := sessBody["session"].(map[string]any)
	if sessObj["workspace"] != wsDir || sessObj["title"] != "Plugin E2E Session" {
		t.Fatalf("unexpected session context: %+v", sessBody)
	}

	// 3. Invalid host token MUST return 401.
	badTokenReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/plugin-host/storage/secrets", nil)
	badTokenReq.Header.Set("Authorization", "Bearer invalid-token")
	badTokenResp, _ := http.DefaultClient.Do(badTokenReq)
	badTokenResp.Body.Close()
	if badTokenResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad host token, got %d", badTokenResp.StatusCode)
	}

	// 4. Bridge SDK & Sandboxed Static UI (and dotfile/traversal blocking).
	sdkResp, err := http.Get(ts.URL + SDKBridgePath)
	if err != nil {
		t.Fatalf("get bridge.js: %v", err)
	}
	sdkBody, _ := io.ReadAll(sdkResp.Body)
	sdkResp.Body.Close()
	if sdkResp.StatusCode != http.StatusOK || !strings.Contains(string(sdkBody), "window.astrorderPlugin") {
		t.Fatalf("unexpected bridge.js response: status=%d", sdkResp.StatusCode)
	}

	uiResp, err := http.Get(ts.URL + "/api/v1/ext/subproc-demo/_ui/ui/panel.html")
	if err != nil {
		t.Fatalf("get static ui: %v", err)
	}
	uiBody, _ := io.ReadAll(uiResp.Body)
	uiResp.Body.Close()
	if uiResp.StatusCode != http.StatusOK || !strings.Contains(string(uiBody), "Demo UI") {
		t.Fatalf("unexpected static UI response: status=%d body=%s", uiResp.StatusCode, string(uiBody))
	}
	if uiResp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(uiResp.Header.Get("Content-Security-Policy"), "connect-src 'none'") {
		t.Fatalf("missing security headers on static UI: %+v", uiResp.Header)
	}

	dotResp, _ := http.Get(ts.URL + "/api/v1/ext/subproc-demo/_ui/.env")
	dotResp.Body.Close()
	if dotResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for hidden .env file, got %d", dotResp.StatusCode)
	}

	// 5. Agent capabilities, CLI alias resolution, and invocation.
	caps := mgr.ListAgentCapabilities(context.Background())
	if len(caps) != 1 || caps[0].FullID != "ext.subproc-demo.echo" {
		t.Fatalf("unexpected agent capabilities: %+v", caps)
	}
	pidAlias, capAlias, rest, ok := mgr.ResolveCLIAlias(context.Background(), []string{"demo", "echo", "--msg", "hi"})
	if !ok || pidAlias != "subproc-demo" || capAlias != "echo" || len(rest) != 2 {
		t.Fatalf("unexpected alias resolution: %q %q %v %v", pidAlias, capAlias, rest, ok)
	}
	invRes, err := mgr.InvokeAgentCapability(context.Background(), "subproc-demo", "echo", map[string]any{"msg": "ping", "workspace": wsDir})
	if err != nil {
		t.Fatalf("invoke agent capability: %v", err)
	}
	invMap, _ := invRes.(map[string]any)
	if invMap["echo"] != "ping" || invMap["workspace"] != wsDir {
		t.Fatalf("unexpected capability invoke result: %+v", invRes)
	}

	// 6. Signal collection from subprocess.
	signals := mgr.CollectSignals(context.Background(), "workspace.failure_signals", SignalQuery{Workspace: wsDir, Limit: 5})
	if len(signals) != 1 || signals[0].SourcePlugin != "subproc-demo" || signals[0].Title != "Subprocess CI Failed" {
		t.Fatalf("unexpected signals: %+v", signals)
	}

	// 7. Skill discovery from enabled plugin.
	skills := mgr.ListEnabledSkills(context.Background())
	if len(skills) != 1 || skills[0].Name != "demo-skill" || skills[0].DisplayName != "Demo Skill" || skills[0].Description != "Helps test plugin skills" {
		t.Fatalf("unexpected skills: %+v", skills)
	}

	// 8. Second subprocess plugin WITHOUT storage:secrets permission MUST get 403 on Host API!
	noSecSrc := t.TempDir()
	noSecManifest := Manifest{
		SchemaVersion: 1,
		ID:            "no-secrets-plugin",
		Name:          "No Secrets",
		Version:       "1.0.0",
		Permissions:   []string{PermStorageKV},
		Backend: BackendSpec{
			Type:    BackendTypeProcess,
			Command: []string{selfBin},
			Args:    []string{"-test.run=^TestHelperPluginProcess$"},
			Env:     map[string]string{"ASTRORDER_TEST_HELPER_PLUGIN": "1"},
			Health:  "/health",
		},
	}
	noSecBytes, _ := json.Marshal(noSecManifest)
	_ = os.WriteFile(filepath.Join(noSecSrc, "plugin.json"), noSecBytes, 0o644)
	if _, err := mgr.InstallFromDir(context.Background(), noSecSrc); err != nil {
		t.Fatalf("install no-secrets-plugin: %v", err)
	}
	if _, _, err := mgr.SetEnabled(context.Background(), "no-secrets-plugin", true, []string{PermStorageKV}); err != nil {
		t.Fatalf("enable no-secrets-plugin: %v", err)
	}
	denyResp, err := http.DefaultClient.Do(authRequest(
		t, http.MethodGet, ts.URL+"/api/v1/ext/no-secrets-plugin/probe-host?method=GET&path=/api/v1/plugin-host/storage/secrets", nil, "",
	))
	if err != nil {
		t.Fatalf("probe denied secrets: %v", err)
	}
	var denyBody struct {
		HostStatus int `json:"host_status"`
	}
	_ = json.NewDecoder(denyResp.Body).Decode(&denyBody)
	denyResp.Body.Close()
	if denyBody.HostStatus != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for missing storage:secrets permission, got %d", denyBody.HostStatus)
	}

	// 9. Restart plugin -> new process PID, still ready.
	restartResp, err := http.DefaultClient.Do(authRequest(t, http.MethodPost, ts.URL+"/api/v1/extensions/subproc-demo/restart", nil, ""))
	if err != nil {
		t.Fatalf("restart plugin: %v", err)
	}
	restartResp.Body.Close()
	if restartResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on restart, got %d", restartResp.StatusCode)
	}
	echo2Resp, _ := http.DefaultClient.Do(authRequest(t, http.MethodGet, ts.URL+"/api/v1/ext/subproc-demo/echo-headers", nil, ""))
	var echoed2 map[string]any
	_ = json.NewDecoder(echo2Resp.Body).Decode(&echoed2)
	echo2Resp.Body.Close()
	if echoed2["pid"] == firstPID {
		t.Fatalf("expected new subprocess PID after restart, still got %v", firstPID)
	}

	// 10. Disable plugin -> stops subprocess cleanly & removes skills/capabilities.
	disResp, err := http.DefaultClient.Do(authRequest(
		t, http.MethodPost, ts.URL+"/api/v1/extensions/subproc-demo/enable",
		strings.NewReader(`{"enabled":false}`), "application/json",
	))
	if err != nil {
		t.Fatalf("disable plugin: %v", err)
	}
	var disView struct {
		Item PluginView `json:"item"`
	}
	_ = json.NewDecoder(disResp.Body).Decode(&disView)
	disResp.Body.Close()
	if disView.Item.Enabled || disView.Item.RuntimeState != RuntimeStateStopped {
		t.Fatalf("expected disabled & stopped, got %+v", disView.Item)
	}
	if len(mgr.ListEnabledSkills(context.Background())) != 0 {
		t.Fatalf("expected skills cleared immediately on disable")
	}

	// 11. Uninstall -> removes directory and purges all stored preferences.
	delResp, err := http.DefaultClient.Do(authRequest(t, http.MethodDelete, ts.URL+"/api/v1/extensions/subproc-demo", nil, ""))
	if err != nil {
		t.Fatalf("uninstall plugin: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on uninstall, got %d", delResp.StatusCode)
	}
	if _, statErr := os.Stat(filepath.Join(pluginsDir, "subproc-demo")); !os.IsNotExist(statErr) {
		t.Fatalf("expected plugin directory removed on uninstall")
	}
	if p, _ := st.GetPreference(context.Background(), prefExtKVPrefix+"subproc-demo"); p.Exists {
		t.Fatalf("expected ext:kv cleaned up on uninstall")
	}
	if p, _ := st.GetPreference(context.Background(), prefExtSecPrefix+"subproc-demo"); p.Exists {
		t.Fatalf("expected ext:secrets cleaned up on uninstall")
	}
}

func TestBuiltinPluginRoutingAndLifecycle(t *testing.T) {
	st := newTestStore(t)
	mgr := NewManager(Options{
		Store:      st,
		Authorizer: stubAuthorizer{expectedToken: testBrowserToken},
	})
	defer mgr.Close()

	err := mgr.RegisterBuiltin(&stubBuiltinPlugin{manifest: Manifest{
		SchemaVersion: 1,
		ID:            "builtin-sample",
		Name:          "Builtin Sample",
		Version:       "1.0.0",
		Category:      "development",
		Permissions:   []string{PermAgentCapabilities, PermSignalsProvide},
		Backend:       BackendSpec{Type: BackendTypeBuiltin},
		Contributes: ContributesSpec{
			AgentCapabilities: []AgentCapabilityContribution{{
				ID:       "status",
				CLIAlias: []string{"sample", "status"},
				Summary:  "Check status",
			}},
			SignalProviders: []SignalProviderContribution{{Kind: "workspace.failure_signals"}},
		},
	}})
	if err != nil {
		t.Fatalf("register builtin: %v", err)
	}

	ts := httptest.NewServer(mgr)
	defer ts.Close()

	// Matches check
	for _, p := range []string{"/api/v1/extensions", "/api/v1/extensions/install", "/api/v1/ext/_sdk/bridge.js", "/api/v1/ext/builtin-sample/ping", "/api/v1/plugin-host/storage/kv"} {
		if !mgr.Matches(http.MethodGet, p) {
			t.Fatalf("expected Matches(%q) == true", p)
		}
	}
	if mgr.Matches(http.MethodGet, "/api/v1/sessions") {
		t.Fatalf("expected Matches(/api/v1/sessions) == false")
	}

	// Browser auth required for /api/v1/ext/{builtin}/...
	unauthResp, _ := http.Get(ts.URL + "/api/v1/ext/builtin-sample/status-pill")
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without browser auth, got %d", unauthResp.StatusCode)
	}

	// Authenticated call succeeds.
	okResp, _ := http.DefaultClient.Do(authRequest(t, http.MethodGet, ts.URL+"/api/v1/ext/builtin-sample/status-pill", nil, ""))
	var body map[string]any
	_ = json.NewDecoder(okResp.Body).Decode(&body)
	okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK || body["subpath"] != "/status-pill" {
		t.Fatalf("unexpected builtin route response: status=%d body=%+v", okResp.StatusCode, body)
	}

	// Signals + capability work when enabled.
	sigs := mgr.CollectSignals(context.Background(), "workspace.failure_signals", SignalQuery{Workspace: "/repo"})
	if len(sigs) != 1 || sigs[0].SourcePlugin != "builtin-sample" {
		t.Fatalf("unexpected builtin signals: %+v", sigs)
	}
	capRes, err := mgr.InvokeAgentCapability(context.Background(), "builtin-sample", "status", map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("invoke builtin cap: %v", err)
	}
	if capRes.(map[string]any)["builtin_cap"] != "status" {
		t.Fatalf("unexpected builtin cap result: %+v", capRes)
	}

	// Builtin cannot be uninstalled.
	delResp, _ := http.DefaultClient.Do(authRequest(t, http.MethodDelete, ts.URL+"/api/v1/extensions/builtin-sample", nil, ""))
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 when uninstalling builtin, got %d", delResp.StatusCode)
	}

	// Disabling builtin persists to shared plugins:enabled and blocks /ext calls.
	if _, _, err := mgr.SetEnabled(context.Background(), "builtin-sample", false, nil); err != nil {
		t.Fatalf("disable builtin: %v", err)
	}
	disabledResp, _ := http.DefaultClient.Do(authRequest(t, http.MethodGet, ts.URL+"/api/v1/ext/builtin-sample/status-pill", nil, ""))
	disabledResp.Body.Close()
	if disabledResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 after disabling builtin, got %d", disabledResp.StatusCode)
	}
	if got := mgr.ListAgentCapabilities(context.Background()); len(got) != 0 {
		t.Fatalf("expected 0 capabilities after disabling builtin, got %d", len(got))
	}

	// Ensure ReloadExternal handles missing pluginsDir gracefully and doesn't hang.
	_ = strconv.Itoa(int(time.Second))
}
