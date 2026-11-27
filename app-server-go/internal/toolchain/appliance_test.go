package toolchain

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
)

type stubDaemonReloader struct {
	mu       sync.Mutex
	calls    int
	reloaded bool
	detail   string
	err      error
}

func (r *stubDaemonReloader) ReloadConfig(_ context.Context) (bool, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.reloaded, r.detail, r.err
}

func (r *stubDaemonReloader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestWindowsRecipesPreferPortableWithoutWinget(t *testing.T) {
	nodeRecipes := RecipesFor("node", "windows")
	if len(nodeRecipes) == 0 || nodeRecipes[0].ID != RecipeIDNodePortable || nodeRecipes[0].Mode != InstallModeBackground {
		t.Fatalf("expected Windows node default recipe to be background portable, got %+v", nodeRecipes)
	}
	if strings.Contains(strings.ToLower(nodeRecipes[0].Command), "winget") {
		t.Fatalf("default Windows node recipe must not require winget: %+v", nodeRecipes[0])
	}

	uvRecipes := RecipesFor("uv", "windows")
	if len(uvRecipes) == 0 || uvRecipes[0].ID != RecipeIDUVPortable || uvRecipes[0].Mode != InstallModeBackground {
		t.Fatalf("expected Windows uv default recipe to be background portable, got %+v", uvRecipes)
	}
	if strings.Contains(strings.ToLower(uvRecipes[0].Command), "winget") {
		t.Fatalf("default Windows uv recipe must not require winget: %+v", uvRecipes[0])
	}

	t.Setenv("ASTRORDER_NODE_MIRROR", "")
	t.Setenv("ASTRORDER_UV_MIRROR", "")
	t.Setenv("ASTRORDER_GITHUB_MIRROR", "")
	t.Setenv("GITHUB_MIRROR", "")

	nodeCN, _, err := PortableDownloadURLs("node", "windows", "amd64", "")
	if err != nil || len(nodeCN) < 2 {
		t.Fatalf("expected node CN + official URLs, got %v err=%v", nodeCN, err)
	}
	if !strings.Contains(nodeCN[0], "npmmirror.com") {
		t.Fatalf("expected npmmirror first for default/cn mirror, got %v", nodeCN)
	}
	if !strings.Contains(nodeCN[len(nodeCN)-1], "nodejs.org") {
		t.Fatalf("expected official nodejs.org fallback last, got %v", nodeCN)
	}

	nodeOfficial, _, err := PortableDownloadURLs("node", "windows", "amd64", "official")
	if err != nil || !strings.Contains(nodeOfficial[0], "nodejs.org") {
		t.Fatalf("expected nodejs.org first for official mirror, got %v err=%v", nodeOfficial, err)
	}

	uvCN, _, err := PortableDownloadURLs("uv", "windows", "amd64", "cn")
	if err != nil || len(uvCN) < 2 {
		t.Fatalf("expected uv CN + official URLs, got %v err=%v", uvCN, err)
	}
	if !strings.Contains(uvCN[len(uvCN)-1], "https://github.com/astral-sh/uv/releases/download/") {
		t.Fatalf("expected official GitHub uv fallback last, got %v", uvCN)
	}
}

func buildTestTarGz(t *testing.T, files map[string]struct {
	body []byte
	mode int64
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, entry := range files {
		mode := entry.mode
		if mode == 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name: name,
			Mode: mode,
			Size: int64(len(entry.body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildTestZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPortableRuntimeInstallMirrorFallbackAndImmediateDetection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script archive fixture uses POSIX sh")
	}

	// Archive has top-level wrapper dir like official Node.js tarballs.
	nodeArchive := buildTestTarGz(t, map[string]struct {
		body []byte
		mode int64
	}{
		"node-v22.14.0-linux-x64/bin/node": {body: []byte("#!/bin/sh\necho v22.14.0\n"), mode: 0o755},
		"node-v22.14.0-linux-x64/bin/npm":  {body: []byte("#!/bin/sh\necho 10.9.2\n"), mode: 0o755},
	})

	primaryHits := 0
	fallbackHits := 0
	primaryMirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer primaryMirror.Close()

	fallbackMirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(nodeArchive)
	}))
	defer fallbackMirror.Close()

	// Route custom mirror -> primary (503), and npmmirror -> fallback (200) via custom transport.
	t.Setenv("ASTRORDER_NODE_MIRROR", primaryMirror.URL)
	t.Setenv("ASTRORDER_NODE_VERSION", "v22.14.0")

	transport := &rewriteHostTransport{
		base: http.DefaultTransport,
		rewrites: map[string]string{
			"npmmirror.com": strings.TrimPrefix(fallbackMirror.URL, "http://"),
		},
	}

	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	t.Setenv("PATH", "/nonexistent-dir-only")

	svc := NewService(Options{
		StateDir:   tmp,
		DataDir:    dataDir,
		HTTPClient: &http.Client{Transport: transport},
	})
	defer svc.Close(context.Background())

	var logBuf bytes.Buffer
	exe, err := svc.InstallPortableRuntime(context.Background(), "node", NetworkProfile{Mirror: "cn"}, &logBuf)
	if err != nil {
		t.Fatalf("InstallPortableRuntime failed: %v\nlog:\n%s", err, logBuf.String())
	}
	if primaryHits == 0 || fallbackHits == 0 {
		t.Fatalf("expected fallback from primary (%d) to secondary mirror (%d)", primaryHits, fallbackHits)
	}
	expectedPath := filepath.Join(dataDir, "runtimes", "node", "bin", "node")
	if exe != expectedPath {
		t.Fatalf("expected installed path %q, got %q", expectedPath, exe)
	}

	// Must be immediately discoverable and executable without restarting process or mutating OS PATH.
	val, err := svc.ValidateExecutable(context.Background(), "local", "node", "node")
	if err != nil || !val.Valid || val.Version != "22.14.0" || val.ResolvedPath != expectedPath {
		t.Fatalf("expected immediate detection of portable node 22.14.0, got val=%+v err=%v", val, err)
	}
}

type rewriteHostTransport struct {
	base     http.RoundTripper
	rewrites map[string]string
}

func (rt *rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if target, ok := rt.rewrites[clone.URL.Host]; ok {
		clone.URL.Scheme = "http"
		clone.URL.Host = target
	}
	return rt.base.RoundTrip(clone)
}

func TestPortableExtractionRejectsPathTraversal(t *testing.T) {
	tmp := t.TempDir()
	badZipPath := filepath.Join(tmp, "bad.zip")
	if err := os.WriteFile(badZipPath, buildTestZip(t, map[string][]byte{
		"../evil.txt": []byte("escape"),
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractZipPortable(badZipPath, filepath.Join(tmp, "out-zip")); err == nil {
		t.Fatal("expected zip-slip path traversal to be rejected")
	}

	badTarPath := filepath.Join(tmp, "bad.tar.gz")
	if err := os.WriteFile(badTarPath, buildTestTarGz(t, map[string]struct {
		body []byte
		mode int64
	}{
		"../../evil.txt": {body: []byte("escape"), mode: 0o644},
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGzPortable(badTarPath, filepath.Join(tmp, "out-tar")); err == nil {
		t.Fatal("expected tar-slip path traversal to be rejected")
	}
}

func TestRealTimePathRefreshMergesManagedAndRegistryPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX sh fixture")
	}
	tmp := t.TempDir()
	regDir := filepath.Join(tmp, "reg-bin")
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeUV := filepath.Join(regDir, "uv")
	if err := os.WriteFile(fakeUV, []byte("#!/bin/sh\necho uv 0.6.14\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	origReader := readRegistryPathsFunc
	readRegistryPathsFunc = func() []string { return []string{regDir, regDir} }
	t.Cleanup(func() { readRegistryPathsFunc = origReader })

	t.Setenv("PATH", "/usr/bin:/bin")
	dataDir := filepath.Join(tmp, "data")
	svc := NewService(Options{StateDir: tmp, DataDir: dataDir})
	defer svc.Close(context.Background())

	refreshed := svc.refreshedLocalPath()
	if !strings.Contains(refreshed, filepath.Join(dataDir, "runtimes", "node")) {
		t.Fatalf("refreshed PATH missing managed node dir: %s", refreshed)
	}
	if !strings.Contains(refreshed, regDir) {
		t.Fatalf("refreshed PATH missing live registry dir: %s", refreshed)
	}

	found, ok := LookPathInPath("uv", refreshed)
	if !ok || found != fakeUV {
		t.Fatalf("expected uv found via live registry path %q, got %q (%v)", fakeUV, found, ok)
	}

	// Verify MergeProcessEnv deduplicates PATH cleanly.
	merged := MergeProcessEnv([]string{"PATH=/old", "KEEP=1"}, map[string]string{"PATH": refreshed})
	pathCount := 0
	for _, kv := range merged {
		if strings.HasPrefix(kv, "PATH=") {
			pathCount++
			if kv != "PATH="+refreshed {
				t.Fatalf("unexpected PATH value: %s", kv)
			}
		}
	}
	if pathCount != 1 {
		t.Fatalf("expected exactly one PATH entry, got %d (%v)", pathCount, merged)
	}
}

func TestOAuthStartPollCompleteAndCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture uses POSIX sh")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Fake codex CLI that emits ANSI-colored OAuth URL + device code, writes auth.json, then exits 0.
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeCodex := filepath.Join(binDir, "codex")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "codex 0.50.0"
  exit 0
fi
if [ "$1" = "login" ]; then
  printf '\033[1;34mOpen https://auth.openai.com/codex/device?state=abc\033[0m\n'
  printf 'Enter one-time code: ABCD-9876\n'
  sleep 0.15
  mkdir -p "$HOME/.codex"
  printf '{"tokens":{"access_token":"ok"}}\n' > "$HOME/.codex/auth.json"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var openedURLs []string
	var openMu sync.Mutex
	reloader := &stubDaemonReloader{reloaded: true, detail: "reloaded"}
	svc := NewService(Options{
		Store:          st,
		StateDir:       filepath.Join(tmp, "state"),
		DataDir:        filepath.Join(tmp, "data"),
		DaemonReloader: reloader,
		BrowserOpener: func(u string) error {
			openMu.Lock()
			openedURLs = append(openedURLs, u)
			openMu.Unlock()
			return nil
		},
	})
	defer svc.Close(context.Background())

	h := NewHandler(svc, nil)
	if !Matches(http.MethodPost, "/api/v1/toolchain/oauth/start") ||
		!Matches(http.MethodGet, "/api/v1/toolchain/oauth/status") ||
		!Matches(http.MethodPost, "/api/v1/toolchain/oauth/cancel") {
		t.Fatal("OAuth routes not registered in Matches")
	}

	// Start OAuth via HTTP handler.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/toolchain/oauth/start", bytes.NewBufferString(`{"agent_kind":"codex","mode":"device"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("oauth start returned %d: %s", rec.Code, rec.Body.String())
	}
	var started OAuthSessionView
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.ID == "" || started.AuthURL != "https://auth.openai.com/codex/device?state=abc" || started.DeviceCode != "ABCD-9876" {
		t.Fatalf("unexpected oauth start view: %+v", started)
	}
	if !started.BrowserOpened {
		t.Fatalf("expected browser_opened=true, got %+v", started)
	}

	// Poll status until completed.
	deadline := time.Now().Add(5 * time.Second)
	var finalView OAuthSessionView
	for time.Now().Before(deadline) {
		pollReq := httptest.NewRequest(http.MethodGet, "/api/v1/toolchain/oauth/status?id="+started.ID, nil)
		pollRec := httptest.NewRecorder()
		h.ServeHTTP(pollRec, pollReq)
		if pollRec.Code != http.StatusOK {
			t.Fatalf("oauth status returned %d: %s", pollRec.Code, pollRec.Body.String())
		}
		if err := json.Unmarshal(pollRec.Body.Bytes(), &finalView); err != nil {
			t.Fatal(err)
		}
		if finalView.Status == "succeeded" || finalView.Status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finalView.Status != "succeeded" || !finalView.AuthConfigured || finalView.AgentStatus == nil || finalView.AgentStatus.Readiness != "ready" {
		t.Fatalf("expected succeeded oauth with ready agent_status, got %+v", finalView)
	}
	openMu.Lock()
	if len(openedURLs) != 1 || openedURLs[0] != "https://auth.openai.com/codex/device?state=abc" {
		t.Fatalf("expected browser opened once with auth URL, got %v", openedURLs)
	}
	openMu.Unlock()
	if reloader.callCount() == 0 {
		t.Fatal("expected daemon hot reload to be triggered after OAuth completion")
	}

	// Reject unsupported Hermes OAuth cleanly.
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/toolchain/oauth/start", bytes.NewBufferString(`{"agent_kind":"hermes"}`))
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for hermes oauth, got %d: %s", badRec.Code, badRec.Body.String())
	}

	// Validate browser URL security guard.
	if _, err := validateBrowserURL("file:///etc/passwd"); err == nil {
		t.Fatal("non-http URL must be rejected")
	}
}

func TestOAuthCancelStopsRunningProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture uses POSIX sh")
	}
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGrok := filepath.Join(binDir, "grok")
	if err := os.WriteFile(fakeGrok, []byte("#!/bin/sh\necho 'Login at https://accounts.x.ai/oauth/authorize?client=grok'\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	svc := NewService(Options{
		StateDir:      tmp,
		BrowserOpener: func(string) error { return nil },
	})
	defer svc.Close(context.Background())

	openFalse := false
	view, err := svc.StartOAuth(context.Background(), OAuthStartRequest{
		AgentKind:   "grok",
		OpenBrowser: &openFalse,
	})
	if err != nil {
		t.Fatalf("StartOAuth failed: %v", err)
	}
	if view.Status != "waiting_user" || view.AuthURL == "" {
		t.Fatalf("expected waiting_user with auth_url, got %+v", view)
	}

	cancelled, err := svc.CancelOAuth(view.ID)
	if err != nil {
		t.Fatalf("CancelOAuth failed: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("expected cancelled status, got %+v", cancelled)
	}
}

func TestDaemonHotReloadSuccessAndGracefulFallbackOnUnimplemented(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture uses POSIX sh")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fakeBin := filepath.Join(tmp, "fake-codex")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\necho codex 1.2.3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Case 1: Daemon supports hot reload -> RequiresDaemonRestart becomes false!
	state1 := filepath.Join(tmp, "state1")
	_ = os.MkdirAll(state1, 0o755)
	okReloader := &stubDaemonReloader{reloaded: true, detail: "已热重载小内核并接入引擎：codex"}
	svcOK := NewService(Options{
		Store:            st,
		StateDir:         state1,
		DefaultWorkspace: tmp,
		DaemonReloader:   okReloader,
	})
	defer svcOK.Close(context.Background())

	saved, err := svcOK.SaveExecutable(context.Background(), "local", "codex", fakeBin)
	if err != nil {
		t.Fatalf("SaveExecutable failed: %v", err)
	}
	if !saved.Saved || !saved.DaemonReloaded || saved.RequiresDaemonRestart {
		t.Fatalf("expected hot reload to clear RequiresDaemonRestart, got %+v", saved)
	}

	onboardOK, err := svcOK.ConfigureOnboarding(context.Background(), OnboardingConfigureRequest{
		EnvironmentID: "local",
		AgentKind:     "codex",
		SkipKey:       true,
	})
	if err != nil {
		t.Fatalf("ConfigureOnboarding failed: %v", err)
	}
	if !onboardOK.OK || !onboardOK.DaemonReloaded || onboardOK.RequiresDaemonRestart {
		t.Fatalf("expected onboarding hot reload success, got %+v", onboardOK)
	}

	// Case 2: Old daemon returns ErrReloadUnsupported (codes.Unimplemented) -> graceful fallback!
	state2 := filepath.Join(tmp, "state2")
	_ = os.MkdirAll(state2, 0o755)
	oldReloader := &stubDaemonReloader{err: daemonclient.ErrReloadUnsupported}
	svcOld := NewService(Options{
		Store:            st,
		StateDir:         state2,
		DefaultWorkspace: tmp,
		DaemonReloader:   oldReloader,
	})
	defer svcOld.Close(context.Background())

	savedOld, err := svcOld.SaveExecutable(context.Background(), "local", "codex", fakeBin)
	if err != nil {
		t.Fatalf("SaveExecutable must not fail on Unimplemented reload: %v", err)
	}
	if !savedOld.Saved || savedOld.DaemonReloaded || !savedOld.RequiresDaemonRestart {
		t.Fatalf("expected graceful degradation on old daemon, got %+v", savedOld)
	}
	if !strings.Contains(savedOld.Message, "更新小内核") {
		t.Fatalf("expected actionable upgrade hint in message, got %q", savedOld.Message)
	}

	onboardOld, err := svcOld.ConfigureOnboarding(context.Background(), OnboardingConfigureRequest{
		EnvironmentID: "local",
		AgentKind:     "codex",
		SkipKey:       true,
	})
	if err != nil {
		t.Fatalf("ConfigureOnboarding must not fail on old daemon: %v", err)
	}
	if !onboardOld.OK || onboardOld.DaemonReloaded || !onboardOld.RequiresDaemonRestart || len(onboardOld.Warnings) == 0 {
		t.Fatalf("expected graceful fallback warning on old daemon, got %+v", onboardOld)
	}

	// Case 3: Daemon temporarily unavailable -> graceful fallback without error!
	unavailableReloader := &stubDaemonReloader{err: errors.New("connection refused")}
	svcDown := NewService(Options{
		Store:            st,
		StateDir:         filepath.Join(tmp, "state3"),
		DefaultWorkspace: tmp,
		DaemonReloader:   unavailableReloader,
	})
	defer svcDown.Close(context.Background())
	reloadRes := svcDown.tryReloadDaemonConfig(context.Background())
	if reloadRes.Reloaded || !reloadRes.Unavailable || reloadRes.Detail == "" {
		t.Fatalf("expected unavailable fallback result, got %+v", reloadRes)
	}
}
