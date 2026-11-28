package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/pluginhost"
)

func TestPluginInitAllTemplates(t *testing.T) {
	cases := []struct {
		name          string
		template      string
		id            string
		ext           string
		expectedFiles []string
		checkFile     string
		mustContain   []string
	}{
		{
			name:          "esm-viewer",
			template:      "esm-viewer",
			id:            "acme-cad-viewer",
			ext:           ".cad",
			expectedFiles: []string{"package.json", "plugin.json", "src/astrorder-sdk.d.ts", "src/entry.tsx", "tsconfig.json", "vite.config.ts"},
			checkFile:     "src/entry.tsx",
			mustContain:   []string{"defineAstrorderPlugin", "ctx.registerFileHandler", "priority: 90", "defaultForExtensions: true", "{toolbarAction}", ".cad"},
		},
		{
			name:          "iframe-viewer",
			template:      "iframe-viewer",
			id:            "acme-iframe-viewer",
			ext:           "myfmt",
			expectedFiles: []string{"plugin.json", "ui/viewer.html"},
			checkFile:     "ui/viewer.html",
			mustContain:   []string{"/api/v1/ext/_sdk/bridge.js", "readFile(", "saveFile("},
		},
		{
			name:          "chat-hook",
			template:      "chat-hook",
			id:            "acme-chat-hook",
			ext:           ".unused",
			expectedFiles: []string{"package.json", "plugin.json", "src/astrorder-sdk.d.ts", "src/entry.tsx", "tsconfig.json", "vite.config.ts"},
			checkFile:     "src/entry.tsx",
			mustContain:   []string{"defineAstrorderPlugin", "registerMessageInterceptor", "registerMarkdownBlockRenderer", "onHostEvent", "registerService"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), tc.id)
			var out bytes.Buffer
			code := run([]string{
				"plugin", "init", dir,
				"--id", tc.id,
				"--template", tc.template,
				"--ext", tc.ext,
			}, &out)
			if code != 0 {
				t.Fatalf("plugin init failed (code=%d): %s", code, out.String())
			}

			var resp struct {
				OK       bool     `json:"ok"`
				Dir      string   `json:"dir"`
				ID       string   `json:"id"`
				Template string   `json:"template"`
				Files    []string `json:"files"`
			}
			if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
				t.Fatalf("decode init json: %v (%s)", err, out.String())
			}
			if !resp.OK || resp.ID != tc.id || resp.Template != tc.template {
				t.Fatalf("unexpected init response: %+v", resp)
			}
			if len(resp.Files) != len(tc.expectedFiles) {
				t.Fatalf("files=%v, want %v", resp.Files, tc.expectedFiles)
			}
			for i, want := range tc.expectedFiles {
				if resp.Files[i] != want {
					t.Fatalf("files[%d]=%q, want %q", i, resp.Files[i], want)
				}
			}

			manifest, err := pluginhost.LoadManifestFile(filepath.Join(dir, "plugin.json"))
			if err != nil {
				t.Fatalf("LoadManifestFile failed: %v", err)
			}
			if manifest.ID != tc.id {
				t.Fatalf("manifest.ID=%q, want %q", manifest.ID, tc.id)
			}

			switch tc.template {
			case "esm-viewer":
				if manifest.Frontend == nil || manifest.Frontend.Mode != pluginhost.FrontendModeESM {
					t.Fatalf("expected frontend.mode=esm, got %+v", manifest.Frontend)
				}
				if len(manifest.Contributes.Viewers) != 1 || manifest.Contributes.Viewers[0].Extensions[0] != ".cad" {
					t.Fatalf("unexpected viewers: %+v", manifest.Contributes.Viewers)
				}
				if manifest.Contributes.Viewers[0].Priority != 90 || !manifest.Contributes.Viewers[0].DefaultForExtensions {
					t.Fatalf("expected priority=90 and default_for_extensions=true, got %+v", manifest.Contributes.Viewers[0])
				}
				if len(manifest.Permissions) != 1 || manifest.Permissions[0] != pluginhost.PermUISlots {
					t.Fatalf("expected minimal ui:slots permission, got %v", manifest.Permissions)
				}
			case "iframe-viewer":
				if manifest.Frontend == nil || manifest.Frontend.Mode != pluginhost.FrontendModeIframe {
					t.Fatalf("expected frontend.mode=iframe, got %+v", manifest.Frontend)
				}
				if len(manifest.Contributes.Viewers) != 1 || manifest.Contributes.Viewers[0].UIEntry != "ui/viewer.html" {
					t.Fatalf("unexpected iframe viewers: %+v", manifest.Contributes.Viewers)
				}
				if manifest.Contributes.Viewers[0].Extensions[0] != ".myfmt" {
					t.Fatalf("expected normalized .myfmt extension, got %+v", manifest.Contributes.Viewers[0].Extensions)
				}
				if manifest.Contributes.Viewers[0].Priority != 90 || !manifest.Contributes.Viewers[0].DefaultForExtensions {
					t.Fatalf("expected priority=90 and default_for_extensions=true, got %+v", manifest.Contributes.Viewers[0])
				}
				if len(manifest.Permissions) != 1 || manifest.Permissions[0] != pluginhost.PermUISlots {
					t.Fatalf("expected minimal ui:slots permission on iframe-viewer, got %v", manifest.Permissions)
				}
			case "chat-hook":
				if manifest.Frontend == nil || manifest.Frontend.Mode != pluginhost.FrontendModeESM {
					t.Fatalf("expected frontend.mode=esm, got %+v", manifest.Frontend)
				}
			}

			if tc.template == "esm-viewer" || tc.template == "chat-hook" {
				for _, jsonFile := range []string{"package.json", "tsconfig.json"} {
					raw, err := os.ReadFile(filepath.Join(dir, jsonFile))
					if err != nil {
						t.Fatalf("read %s: %v", jsonFile, err)
					}
					var parsed map[string]any
					if err := json.Unmarshal(raw, &parsed); err != nil {
						t.Fatalf("%s is not valid JSON: %v", jsonFile, err)
					}
				}
				viteCfg, err := os.ReadFile(filepath.Join(dir, "vite.config.ts"))
				if err != nil {
					t.Fatalf("read vite.config.ts: %v", err)
				}
				if strings.Contains(string(viteCfg), "@astrorder/plugin-sdk/vite-preset") {
					t.Fatalf("standalone vite.config.ts must not require unpublished @astrorder/plugin-sdk package")
				}
				if !strings.Contains(string(viteCfg), "@astrorder/sdk") || !strings.Contains(string(viteCfg), "astrorder-copy-manifest") {
					t.Fatalf("vite.config.ts missing host externals or manifest copy plugin")
				}
			}

			content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(tc.checkFile)))
			if err != nil {
				t.Fatalf("read %s: %v", tc.checkFile, err)
			}
			for _, substr := range tc.mustContain {
				if !strings.Contains(string(content), substr) {
					t.Fatalf("%s missing expected snippet %q", tc.checkFile, substr)
				}
			}
		})
	}
}

func TestPluginInitRejectsInvalidAndReservedIDs(t *testing.T) {
	for _, badID := range []string{"BadUpper", "a", "../escape", "_sdk", "r2t2"} {
		var out bytes.Buffer
		dir := filepath.Join(t.TempDir(), "plug")
		code := run([]string{"plugin", "init", dir, "--id", badID}, &out)
		if code == 0 {
			t.Fatalf("expected plugin init to reject id %q, got success: %s", badID, out.String())
		}
	}
}

func TestPluginKeygenSignVerifyAndTamperDetection(t *testing.T) {
	tmp := t.TempDir()
	keyDir := filepath.Join(tmp, "keys")
	plugDir := filepath.Join(tmp, "my-signed-plugin")

	// 1. Keygen
	var keyOut bytes.Buffer
	if code := run([]string{"plugin", "keygen", "--out-dir", keyDir, "--key-id", "release-k1"}, &keyOut); code != 0 {
		t.Fatalf("plugin keygen failed: %s", keyOut.String())
	}
	var keyResp struct {
		OK              bool   `json:"ok"`
		KeyID           string `json:"key_id"`
		PrivateKeyPath  string `json:"private_key_path"`
		PublicKeyPath   string `json:"public_key_path"`
		PublicKeyBase64 string `json:"public_key_base64"`
	}
	if err := json.Unmarshal(keyOut.Bytes(), &keyResp); err != nil {
		t.Fatalf("decode keygen json: %v", err)
	}
	if !keyResp.OK || keyResp.KeyID != "release-k1" {
		t.Fatalf("unexpected keygen response: %+v", keyResp)
	}
	privInfo, err := os.Stat(keyResp.PrivateKeyPath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if runtime.GOOS != "windows" && privInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %o, want 0600", privInfo.Mode().Perm())
	}
	pubRaw, err := base64.StdEncoding.DecodeString(keyResp.PublicKeyBase64)
	if err != nil || len(pubRaw) != ed25519.PublicKeySize {
		t.Fatalf("invalid public key base64: len=%d err=%v", len(pubRaw), err)
	}

	// 2. Scaffold an iframe-viewer plugin so all declared assets exist immediately.
	var initOut bytes.Buffer
	if code := run([]string{"plugin", "init", plugDir, "--id", "signed-viewer", "--template", "iframe-viewer"}, &initOut); code != 0 {
		t.Fatalf("plugin init failed: %s", initOut.String())
	}

	// 3. Sign
	var signOut bytes.Buffer
	if code := run([]string{"plugin", "sign", plugDir, "--key", keyResp.PrivateKeyPath, "--key-id", "release-k1"}, &signOut); code != 0 {
		t.Fatalf("plugin sign failed: %s", signOut.String())
	}
	var signResp struct {
		OK        bool   `json:"ok"`
		ID        string `json:"id"`
		KeyID     string `json:"key_id"`
		Algorithm string `json:"algorithm"`
		Digest    string `json:"digest"`
	}
	if err := json.Unmarshal(signOut.Bytes(), &signResp); err != nil {
		t.Fatalf("decode sign json: %v", err)
	}
	if !signResp.OK || signResp.ID != "signed-viewer" || signResp.KeyID != "release-k1" || signResp.Algorithm != "ed25519" || len(signResp.Digest) != 16 {
		t.Fatalf("unexpected sign response: %+v", signResp)
	}

	// 4. Verify using public key file path
	var verifyFileOut bytes.Buffer
	if code := run([]string{"plugin", "verify", plugDir, "--pub-key", keyResp.PublicKeyPath}, &verifyFileOut); code != 0 {
		t.Fatalf("plugin verify (file) failed: %s", verifyFileOut.String())
	}
	var verifyResp struct {
		OK       bool   `json:"ok"`
		ID       string `json:"id"`
		KeyID    string `json:"key_id"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(verifyFileOut.Bytes(), &verifyResp); err != nil {
		t.Fatalf("decode verify json: %v", err)
	}
	if !verifyResp.OK || !verifyResp.Verified || verifyResp.ID != "signed-viewer" || verifyResp.KeyID != "release-k1" {
		t.Fatalf("unexpected verify response: %+v", verifyResp)
	}

	// 5. Verify using inline base64 public key
	var verifyB64Out bytes.Buffer
	if code := run([]string{"plugin", "verify", plugDir, "--pub-key", keyResp.PublicKeyBase64}, &verifyB64Out); code != 0 {
		t.Fatalf("plugin verify (raw base64) failed: %s", verifyB64Out.String())
	}

	// 6. Cross-check against runtime pluginhost.Manager trusted key verification
	extParent := filepath.Join(tmp, "installed-ext")
	if err := os.MkdirAll(extParent, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := pluginhost.NewManager(pluginhost.Options{
		PluginsDir: extParent,
		TrustedPublicKeys: map[string]ed25519.PublicKey{
			"release-k1": ed25519.PublicKey(pubRaw),
		},
	})
	defer mgr.Close()
	view, err := mgr.InstallFromDir(context.Background(), plugDir)
	if err != nil {
		t.Fatalf("Manager.InstallFromDir failed on CLI-signed plugin: %v", err)
	}
	if !view.SignatureVerified {
		t.Fatalf("expected pluginhost.Manager to report SignatureVerified=true for CLI-signed plugin")
	}

	// 7. Verify with wrong key fails
	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var wrongKeyOut bytes.Buffer
	if code := run([]string{"plugin", "verify", plugDir, "--pub-key", base64.StdEncoding.EncodeToString(otherPub)}, &wrongKeyOut); code == 0 {
		t.Fatalf("expected verify with wrong key to fail, got success: %s", wrongKeyOut.String())
	}

	// 8. Tamper with plugin file -> verify MUST fail!
	viewerPath := filepath.Join(plugDir, "ui", "viewer.html")
	origViewer, err := os.ReadFile(viewerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(viewerPath, append(origViewer, []byte("\n<!-- tampered -->\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	var tamperOut bytes.Buffer
	if code := run([]string{"plugin", "verify", plugDir, "--pub-key", keyResp.PublicKeyPath}, &tamperOut); code == 0 {
		t.Fatalf("expected verify to fail after file tampering, got success: %s", tamperOut.String())
	}
}

func TestPluginPackDeterministicZipAndFiltering(t *testing.T) {
	tmp := t.TempDir()
	plugDir := filepath.Join(tmp, "packable-viewer")
	var initOut bytes.Buffer
	if code := run([]string{"plugin", "init", plugDir, "--id", "packable-viewer", "--template", "esm-viewer"}, &initOut); code != 0 {
		t.Fatalf("plugin init failed: %s", initOut.String())
	}

	// Packing before ui/entry.js exists must fail!
	var missingOut bytes.Buffer
	if code := run([]string{"plugin", "pack", plugDir}, &missingOut); code == 0 {
		t.Fatalf("expected pack to fail when frontend.entry is missing: %s", missingOut.String())
	}

	// Create built entry, plus ignored dirs/files and an escaping symlink.
	if err := os.MkdirAll(filepath.Join(plugDir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, "ui", "entry.js"), []byte("export function activate() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(plugDir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, "node_modules", "pkg", "index.js"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(plugDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(tmp, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(outsideFile, filepath.Join(plugDir, "escape-link.txt"))

	outZip1 := filepath.Join(tmp, "bundle-1.zip")
	outZip2 := filepath.Join(tmp, "bundle-2.zip")

	var packOut1 bytes.Buffer
	if code := run([]string{"plugin", "pack", plugDir, "--out", outZip1}, &packOut1); code != 0 {
		t.Fatalf("plugin pack #1 failed: %s", packOut1.String())
	}
	var packResp1 struct {
		OK      bool   `json:"ok"`
		ID      string `json:"id"`
		Version string `json:"version"`
		Archive string `json:"archive"`
		Size    int64  `json:"size"`
		SHA256  string `json:"sha256"`
	}
	if err := json.Unmarshal(packOut1.Bytes(), &packResp1); err != nil {
		t.Fatalf("decode pack json: %v", err)
	}
	if !packResp1.OK || packResp1.ID != "packable-viewer" || packResp1.Version != "0.1.0" || packResp1.Size <= 0 || len(packResp1.SHA256) != 64 {
		t.Fatalf("unexpected pack response: %+v", packResp1)
	}

	// Verify actual file SHA-256 matches reported sha256.
	zipBytes, err := os.ReadFile(outZip1)
	if err != nil {
		t.Fatal(err)
	}
	actualSum := sha256.Sum256(zipBytes)
	if hex.EncodeToString(actualSum[:]) != packResp1.SHA256 {
		t.Fatalf("sha256 mismatch: reported=%s actual=%s", packResp1.SHA256, hex.EncodeToString(actualSum[:]))
	}

	// Deterministic check: packing again yields the exact same SHA-256.
	var packOut2 bytes.Buffer
	if code := run([]string{"plugin", "pack", plugDir, "--out", outZip2}, &packOut2); code != 0 {
		t.Fatalf("plugin pack #2 failed: %s", packOut2.String())
	}
	var packResp2 struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(packOut2.Bytes(), &packResp2); err != nil {
		t.Fatal(err)
	}
	if packResp2.SHA256 != packResp1.SHA256 {
		t.Fatalf("expected deterministic zip sha256, got %s vs %s", packResp1.SHA256, packResp2.SHA256)
	}

	// Inspect zip contents: node_modules, .git, .env, escape-link.txt MUST be absent.
	zr, err := zip.OpenReader(outZip1)
	if err != nil {
		t.Fatalf("open packed zip: %v", err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		if strings.Contains(f.Name, "node_modules") || strings.Contains(f.Name, ".git") || strings.HasPrefix(filepath.Base(f.Name), ".") || f.Name == "escape-link.txt" {
			t.Fatalf("forbidden entry packed into zip: %s", f.Name)
		}
	}
	for _, want := range []string{"plugin.json", "ui/entry.js", "src/entry.tsx", "vite.config.ts"} {
		if !names[want] {
			t.Fatalf("expected %q in zip entries, got %v", want, names)
		}
	}

	// Verify the archive installs cleanly via pluginhost.Manager.InstallFromZip!
	mgr := pluginhost.NewManager(pluginhost.Options{PluginsDir: filepath.Join(tmp, "ext-install")})
	defer mgr.Close()
	view, err := mgr.InstallFromZip(context.Background(), bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("Manager.InstallFromZip rejected packed archive: %v", err)
	}
	if view.ID != "packable-viewer" {
		t.Fatalf("installed view.ID=%q, want packable-viewer", view.ID)
	}
}

func TestPluginDevSnapshotChangeDetectionAndReloadHTTP(t *testing.T) {
	plugDir := filepath.Join(t.TempDir(), "dev-viewer")
	var initOut bytes.Buffer
	if code := run([]string{"plugin", "init", plugDir, "--id", "dev-viewer", "--template", "iframe-viewer"}, &initOut); code != 0 {
		t.Fatalf("plugin init failed: %s", initOut.String())
	}

	// Unit test snapshotPluginDir + diffPluginSnapshots directly.
	snap1, err := snapshotPluginDir(plugDir)
	if err != nil {
		t.Fatalf("snapshotPluginDir #1: %v", err)
	}
	if len(diffPluginSnapshots(snap1, snap1)) != 0 {
		t.Fatalf("identical snapshots reported diff")
	}

	// Hidden files and node_modules must not trigger spurious diffs.
	if err := os.WriteFile(filepath.Join(plugDir, ".hidden-temp"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapIgnored, err := snapshotPluginDir(plugDir)
	if err != nil {
		t.Fatal(err)
	}
	if changed := diffPluginSnapshots(snap1, snapIgnored); len(changed) != 0 {
		t.Fatalf("hidden file triggered change: %v", changed)
	}

	// Modifying ui/viewer.html must be detected.
	viewerPath := filepath.Join(plugDir, "ui", "viewer.html")
	if err := os.WriteFile(viewerPath, []byte("<html><body>v2</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap2, err := snapshotPluginDir(plugDir)
	if err != nil {
		t.Fatal(err)
	}
	changed := diffPluginSnapshots(snap1, snap2)
	if len(changed) != 1 || changed[0] != "ui/viewer.html" {
		t.Fatalf("changed=%v, want [ui/viewer.html]", changed)
	}

	// Test end-to-end dev loop with httptest server (install + file change -> reload -> cancel).
	var mu sync.Mutex
	installSource := ""
	installAuth := ""
	reloadCount := 0
	reloadAuth := ""

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/extensions/install":
			var body struct {
				SourcePath string `json:"source_path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			installSource = body.SourcePath
			installAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"item":{"id":"dev-viewer"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/extensions/reload":
			mu.Lock()
			reloadCount++
			reloadAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchingCh := make(chan struct{}, 1)
	reloadedCh := make(chan struct{}, 2)
	devOut := &eventWatchBuffer{
		onWrite: func(chunk string) {
			if strings.Contains(chunk, `"event":"watching"`) {
				select {
				case watchingCh <- struct{}{}:
				default:
				}
			}
			if strings.Contains(chunk, `"event":"reloaded"`) {
				select {
				case reloadedCh <- struct{}{}:
				default:
				}
			}
		},
	}

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- runPluginDevLoop(ctx, pluginDevOptions{
			Dir:        plugDir,
			Server:     srv.URL,
			Token:      "secret-dev-token",
			IntervalMS: 30,
			Client:     srv.Client(),
		}, devOut)
	}()

	select {
	case <-watchingCh:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatalf("timed out waiting for watcher startup; output=%s", devOut.String())
	}

	if err := os.WriteFile(viewerPath, []byte("<html><body>v3-hot-reloaded</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-reloadedCh:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatalf("timed out waiting for reload HTTP call; output=%s", devOut.String())
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("runPluginDevLoop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runPluginDevLoop did not stop after context cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	absPlugDir, _ := filepath.Abs(plugDir)
	if installSource != absPlugDir {
		t.Fatalf("install source_path=%q, want %q", installSource, absPlugDir)
	}
	if installAuth != "Bearer secret-dev-token" {
		t.Fatalf("install Authorization=%q, want Bearer secret-dev-token", installAuth)
	}
	if reloadCount < 1 {
		t.Fatalf("reloadCount=%d, want >= 1", reloadCount)
	}
	if reloadAuth != "Bearer secret-dev-token" {
		t.Fatalf("reload Authorization=%q, want Bearer secret-dev-token", reloadAuth)
	}
	if !strings.Contains(devOut.String(), `"event":"reloaded"`) {
		t.Fatalf("expected reloaded event in dev log output, got: %s", devOut.String())
	}
}

type eventWatchBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	onWrite func(string)
}

func (b *eventWatchBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	n, err := b.buf.Write(p)
	b.mu.Unlock()
	if b.onWrite != nil {
		b.onWrite(string(p))
	}
	return n, err
}

func (b *eventWatchBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
