package pluginhost

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSignatureCacheReusesVerifiedResultAndDetectsTampering(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	uiDir := filepath.Join(dir, "ui", "chunks")
	if err := os.MkdirAll(uiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entryPath := filepath.Join(dir, "ui", "entry.js")
	chunkPath := filepath.Join(uiDir, "ThreeViewer-abc123.js")
	if err := os.WriteFile(entryPath, []byte(`export function activate() {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chunkPath, []byte(`export const mesh = 1;`), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "cached-signed-esm",
		Name:          "Cached Signed ESM",
		Version:       "1.0.0",
		Category:      "media",
		Backend:       BackendSpec{Type: BackendTypeNone},
		Frontend:      &FrontendSpec{Mode: FrontendModeESM, Entry: "ui/entry.js"},
	}
	mBytes, _ := json.MarshalIndent(manifest, "", "  ")
	manifestPath := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(manifestPath, mBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	sig, err := SignPluginDir(dir, "test-key", priv)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature = sig
	mBytes, _ = json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(manifestPath, mBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Age all file mtimes past the 2s racy-clean threshold so the stat-fingerprint cache activates.
	oldTime := time.Now().Add(-10 * time.Second)
	for _, p := range []string{manifestPath, entryPath, chunkPath} {
		if err := os.Chtimes(p, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}

	mgr := NewManager(Options{
		TrustedPublicKeys: map[string]ed25519.PublicKey{"test-key": pub},
	})
	if !mgr.verifyPluginSignatureCached(manifest, dir) {
		t.Fatal("expected first verification to succeed")
	}
	mgr.sigCacheMu.RLock()
	cacheEntries := len(mgr.sigCache)
	mgr.sigCacheMu.RUnlock()
	if cacheEntries != 1 {
		t.Fatalf("expected 1 cached signature entry for aged plugin dir, got %d", cacheEntries)
	}
	if !mgr.verifyPluginSignatureCached(manifest, dir) {
		t.Fatal("expected cached verification hit to succeed")
	}

	// Tampering with a file invalidates stat fingerprint immediately -> fails verification!
	if err := os.WriteFile(entryPath, []byte(`export function activate() { /* tampered */ }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if mgr.verifyPluginSignatureCached(manifest, dir) {
		t.Fatal("expected tampered plugin directory to fail signature verification immediately")
	}
}

func TestServePluginESMImmutableCacheOnHashedChunks(t *testing.T) {
	officialDir := t.TempDir()
	plugDir := filepath.Join(officialDir, "chunked-viewer")
	if err := os.MkdirAll(filepath.Join(plugDir, "ui", "chunks"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(plugDir, "plugin.json"), []byte(`{
		"schema_version": 1,
		"id": "chunked-viewer",
		"name": "Chunked Viewer",
		"version": "1.0.0",
		"category": "media",
		"default_enabled": true,
		"backend": {"type": "none"},
		"frontend": {"mode": "esm", "entry": "ui/entry.js"}
	}`), 0o644)
	_ = os.WriteFile(filepath.Join(plugDir, "ui", "entry.js"), []byte(`export function activate() {}`), 0o644)
	_ = os.WriteFile(filepath.Join(plugDir, "ui", "chunks", "Heavy-deadbeef.js"), []byte(`export const ok = true;`), 0o644)

	mgr := NewManager(Options{
		Store:              newTestStore(t),
		OfficialPluginsDir: officialDir,
		Authorizer:         cookieOrBearerAuthorizer{expectedToken: testBrowserToken},
	})
	defer mgr.Close()
	_ = mgr.ReloadExternal(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ext/chunked-viewer/_esm/ui/chunks/Heavy-deadbeef.js", nil)
	req.Header.Set("Authorization", "Bearer "+testBrowserToken)
	rec := httptest.NewRecorder()
	mgr.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for lazy chunk, got %d: %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("expected immutable Cache-Control on hashed ui/chunks asset, got %q", cc)
	}
}
