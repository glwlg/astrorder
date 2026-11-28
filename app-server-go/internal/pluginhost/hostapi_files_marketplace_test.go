package pluginhost

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/targetfs"
)

type permissiveAuth struct{}

func (permissiveAuth) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

func buildTestZipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestManifestDependsOnAndViewerUIEntry(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"id": "demo-viewer",
		"name": "Demo Viewer",
		"version": "1.0.0",
		"depends_on": ["base-lib", "base-lib"],
		"permissions": ["workspace:files.read", "workspace:files.write"],
		"backend": {"type": "none"},
		"settings": {"mode": "none"},
		"contributes": {
			"viewers": [{
				"id": "demo-file",
				"title": "Demo File",
				"extensions": ["xyz", ".Demo.JSON"],
				"ui_entry": "ui/viewer.html",
				"default_for_extensions": true
			}]
		}
	}`)
	m, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if len(m.DependsOn) != 1 || m.DependsOn[0] != "base-lib" {
		t.Fatalf("unexpected depends_on: %+v", m.DependsOn)
	}
	if len(m.Contributes.Viewers) != 1 {
		t.Fatalf("expected 1 viewer")
	}
	exts := m.Contributes.Viewers[0].Extensions
	if len(exts) != 2 || exts[0] != ".xyz" || exts[1] != ".demo.json" {
		t.Fatalf("extensions not normalized: %+v", exts)
	}

	// Self-dependency must fail validation.
	selfDep := []byte(`{"schema_version":1,"id":"self-loop","name":"Loop","version":"1.0.0","depends_on":["self-loop"],"backend":{"type":"none"}}`)
	if _, err := ParseManifest(selfDep); err == nil {
		t.Fatalf("expected self-dependency manifest to fail")
	}

	// Path traversal in viewer ui_entry must fail validation.
	badEntry := []byte(`{"schema_version":1,"id":"bad-viewer","name":"Bad","version":"1.0.0","backend":{"type":"none"},"contributes":{"viewers":[{"id":"v1","title":"V1","ui_entry":"../secret.html"}]}}`)
	if _, err := ParseManifest(badEntry); err == nil {
		t.Fatalf("expected traversal ui_entry to fail")
	}
}

func TestHostAPIWorkspaceFilesConfinementAndPermissions(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	workspaceDir := filepath.Join(t.TempDir(), "ws")
	outsideFile := filepath.Join(filepath.Dir(workspaceDir), "outside.txt")
	if err := os.MkdirAll(filepath.Join(workspaceDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := targetfs.NewResolver(targetfs.ResolverOptions{AllowedRoots: []string{workspaceDir}, AllowTempDirs: true})
	mgr := NewManager(Options{
		Store:              st,
		Authorizer:         permissiveAuth{},
		Resolver:           resolver,
		PluginsDir:         pluginsDir,
		AppProfileOverride: AppProfileDeveloper,
	})

	// Register a running dummy process entry token directly on supervisor for testing Host API.
	pluginID := "file-agent"
	if err := mgr.storage.SetEnabled(ctx, pluginID, true); err != nil {
		t.Fatal(err)
	}
	hostToken := "test-host-token-files-123"
	mgr.supervisor.mu.Lock()
	mgr.supervisor.procs[pluginID] = &managedProcess{
		pluginID:  pluginID,
		hostToken: hostToken,
		state:     RuntimeStateReady,
	}
	mgr.supervisor.mu.Unlock()

	callHost := func(method, path string, body any) *httptest.ResponseRecorder {
		var r io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, r)
		req.Header.Set("X-Astrorder-Host-Token", hostToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		mgr.ServeHTTP(rec, req)
		return rec
	}

	// 1. Without workspace:files.read -> 403.
	rec := callHost(http.MethodGet, "/api/v1/plugin-host/workspace/files/read?workspace="+urlQueryEscape(workspaceDir)+"&path=hello.txt", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without read permission, got %d: %s", rec.Code, rec.Body.String())
	}

	// Grant read only.
	if err := mgr.storage.SetGrantedPermissions(ctx, pluginID, []string{PermWorkspaceFilesRead}); err != nil {
		t.Fatal(err)
	}

	// 2. Read allowed file inside workspace.
	rec = callHost(http.MethodGet, "/api/v1/plugin-host/workspace/files/read?workspace="+urlQueryEscape(workspaceDir)+"&path=hello.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 reading hello.txt, got %d: %s", rec.Code, rec.Body.String())
	}
	var readResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &readResp); err != nil || readResp["content"] != "hello world" || readResp["encoding"] != "utf8" {
		t.Fatalf("unexpected read response: %s", rec.Body.String())
	}

	// 3. List & stat inside workspace.
	rec = callHost(http.MethodGet, "/api/v1/plugin-host/workspace/files/list?workspace="+urlQueryEscape(workspaceDir), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hello.txt") {
		t.Fatalf("expected 200 list containing hello.txt, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = callHost(http.MethodGet, "/api/v1/plugin-host/workspace/files/stat?workspace="+urlQueryEscape(workspaceDir)+"&path=hello.txt", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hello.txt") {
		t.Fatalf("expected 200 stat, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. Traversal outside workspace MUST be rejected with 403.
	rec = callHost(http.MethodGet, "/api/v1/plugin-host/workspace/files/read?workspace="+urlQueryEscape(workspaceDir)+"&path=../outside.txt", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on path traversal, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Write without workspace:files.write -> 403.
	rec = callHost(http.MethodPut, "/api/v1/plugin-host/workspace/files/write", map[string]any{
		"workspace": workspaceDir,
		"path":      "created.txt",
		"content":   "new data",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without write permission, got %d: %s", rec.Code, rec.Body.String())
	}

	// Grant write too.
	if err := mgr.storage.SetGrantedPermissions(ctx, pluginID, []string{PermWorkspaceFilesRead, PermWorkspaceFilesWrite}); err != nil {
		t.Fatal(err)
	}

	// 6. Write utf8 and base64 files + mkdir.
	rec = callHost(http.MethodPost, "/api/v1/plugin-host/workspace/files/mkdir", map[string]any{
		"workspace": workspaceDir,
		"parent":    ".",
		"name":      "generated",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 mkdir, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = callHost(http.MethodPut, "/api/v1/plugin-host/workspace/files/write", map[string]any{
		"workspace": workspaceDir,
		"path":      "generated/bin.dat",
		"encoding":  "base64",
		"content":   base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04}),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 write base64, got %d: %s", rec.Code, rec.Body.String())
	}
	gotBytes, err := os.ReadFile(filepath.Join(workspaceDir, "generated", "bin.dat"))
	if err != nil || !bytes.Equal(gotBytes, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("written bytes mismatch: %v, %x", err, gotBytes)
	}
}

func TestMarketplaceRegistriesAndRemoteInstall(t *testing.T) {
	st := newTestStore(t)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	officialDir := filepath.Join(t.TempDir(), "official")
	if err := os.MkdirAll(filepath.Join(officialDir, "sample-off", "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(officialDir, "sample-off", "plugin.json"), []byte(`{
		"schema_version": 1, "id": "sample-off", "name": "Official Sample", "version": "1.0.0",
		"category": "diagram", "backend": {"type": "none"},
		"frontend": {"mode": "esm", "entry": "ui/entry.js"}
	}`), 0o644)
	_ = os.WriteFile(filepath.Join(officialDir, "sample-off", "ui", "entry.js"), []byte("export function activate(){}"), 0o644)

	archiveBytes := buildTestZipArchive(t, map[string]string{
		"plugin.json": `{
			"schema_version": 1,
			"id": "remote-ext-demo",
			"name": "Remote Demo Plugin",
			"version": "1.2.0",
			"category": "media",
			"backend": {"type": "none"},
			"frontend": {"mode": "esm", "entry": "ui/entry.js"},
			"contributes": {"viewers": [{"id": "remote-ext-demo", "title": "Remote Demo", "extensions": [".rdemo"]}]}
		}`,
		"ui/entry.js": "export function activate() {}",
	})
	sum := sha256.Sum256(archiveBytes)
	archiveSHA := hex.EncodeToString(sum[:])

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			writeJSON(w, http.StatusOK, MarketplaceRegistryIndex{
				SchemaVersion: 1,
				Name:          "Test Registry",
				Plugins: []MarketplacePluginEntry{
					{
						ID:           "remote-ext-demo",
						Name:         "Remote Demo Plugin",
						Version:      "1.2.0",
						Category:     "media",
						DownloadURL:  srv.URL + "/remote-ext-demo.zip",
						SHA256:       archiveSHA,
						FrontendMode: "esm",
						BackendType:  "none",
						Extensions:   []string{".rdemo"},
					},
				},
			})
		case "/remote-ext-demo.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archiveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	mgr := NewManager(Options{
		Store:              st,
		Authorizer:         permissiveAuth{},
		PluginsDir:         pluginsDir,
		OfficialPluginsDir: officialDir,
		AppProfileOverride: AppProfileDeveloper,
	})

	// 1. Metadata IP SSRF must be rejected when saving registries.
	badBody, _ := json.Marshal(map[string]any{
		"items": []PluginRegistrySource{{ID: "ssrf", Name: "Bad", URL: "http://169.254.169.254/latest/meta-data", Enabled: true}},
	})
	rec := httptest.NewRecorder()
	mgr.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/extensions/registries", bytes.NewReader(badBody)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for metadata SSRF registry URL, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Save valid test registry.
	goodBody, _ := json.Marshal(map[string]any{
		"items": []PluginRegistrySource{
			{ID: builtinOfficialRegistry, Enabled: true},
			{ID: "test-reg", Name: "Test Registry", URL: srv.URL + "/index.json", Enabled: true},
		},
	})
	rec = httptest.NewRecorder()
	mgr.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/extensions/registries", bytes.NewReader(goodBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 saving registries, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Fetch marketplace -> includes both local official + remote entry.
	rec = httptest.NewRecorder()
	mgr.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/extensions/marketplace", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 marketplace, got %d: %s", rec.Code, rec.Body.String())
	}
	var mp MarketplaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &mp); err != nil {
		t.Fatalf("decode marketplace: %v", err)
	}
	if len(mp.Items) != 2 {
		t.Fatalf("expected 2 marketplace items (1 official + 1 remote), got %d: %+v", len(mp.Items), mp.Items)
	}

	// 4. Remote install with mismatched SHA256 must fail.
	badInstall, _ := json.Marshal(map[string]any{
		"download_url": srv.URL + "/remote-ext-demo.zip",
		"sha256":       "0000000000000000000000000000000000000000000000000000000000000000",
		"plugin_id":    "remote-ext-demo",
	})
	rec = httptest.NewRecorder()
	mgr.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/extensions/install-remote", bytes.NewReader(badInstall)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on SHA256 mismatch, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Remote install with valid SHA256 succeeds and starts disabled pending consent.
	goodInstall, _ := json.Marshal(map[string]any{
		"download_url": srv.URL + "/remote-ext-demo.zip",
		"sha256":       archiveSHA,
		"plugin_id":    "remote-ext-demo",
	})
	rec = httptest.NewRecorder()
	mgr.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/extensions/install-remote", bytes.NewReader(goodInstall)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 remote install, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"remote-ext-demo"`) {
		t.Fatalf("expected installed remote-ext-demo in response: %s", rec.Body.String())
	}
}

func urlQueryEscape(s string) string {
	return strings.NewReplacer(`\`, `%5C`, `/`, `%2F`, `:`, `%3A`, ` `, `%20`).Replace(s)
}
