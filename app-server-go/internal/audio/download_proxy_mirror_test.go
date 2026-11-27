package audio

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestResolveR2T2MirrorURLs(t *testing.T) {
	// 1. Default without mirror or env -> official URLs.
	t.Setenv("HF_ENDPOINT", "")
	t.Setenv("ASTRORDER_HF_ENDPOINT", "")
	t.Setenv("GITHUB_MIRROR", "")
	t.Setenv("ASTRORDER_GITHUB_MIRROR", "")
	t.Setenv("GH_PROXY", "")
	t.Setenv("ASTRORDER_MIRROR", "")
	t.Setenv("ASTRORDER_PROXY_URL", "")
	t.Setenv("ASTRORDER_R2T2_MODEL_BASE", "")
	t.Setenv("ASTRORDER_R2T2_RUNTIME_URL", "")

	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mgr, err := newLocalR2T2Manager(Options{Store: st, ResourceDir: t.TempDir()}, context.Background())
	if err != nil {
		t.Fatal(err)
	}

	cfg := mgr.resolveDownloadNetworkConfig(context.Background())
	if got := resolveModelURLs(cfg, ModelName); len(got) != 1 || got[0] != ModelBase+ModelName {
		t.Fatalf("expected official model URL, got %v", got)
	}
	if got := resolveRuntimeURLs(cfg); len(got) != 1 || got[0] != RuntimeURL {
		t.Fatalf("expected official runtime URL, got %v", got)
	}

	// 2. Unified toolchain:network mirror="cn" + proxy_url -> CN mirrors first, official fallback!
	if err := st.SetPreference(context.Background(), prefToolchainNetwork, map[string]any{
		"default": map[string]any{
			"proxy_url": "http://127.0.0.1:7890",
			"mirror":    "cn",
		},
		"by_environment": map[string]any{
			"local": map[string]any{
				"proxy_url": "http://127.0.0.1:7891",
			},
		},
	}, true); err != nil {
		t.Fatal(err)
	}

	cfgCN := mgr.resolveDownloadNetworkConfig(context.Background())
	if cfgCN.ProxyURL != "http://127.0.0.1:7891" {
		t.Fatalf("expected local override proxy http://127.0.0.1:7891, got %q", cfgCN.ProxyURL)
	}
	modelCN := resolveModelURLs(cfgCN, ModelName)
	if len(modelCN) != 2 || !strings.HasPrefix(modelCN[0], DefaultCNHFMirror+"/") || modelCN[1] != ModelBase+ModelName {
		t.Fatalf("expected hf-mirror primary + official fallback, got %v", modelCN)
	}
	rtCN := resolveRuntimeURLs(cfgCN)
	if len(rtCN) != 2 || !strings.HasPrefix(rtCN[0], DefaultCNGitHubProxy+"/https://github.com/") || rtCN[1] != RuntimeURL {
		t.Fatalf("expected cn github proxy primary + official fallback, got %v", rtCN)
	}

	// 3. Explicit HF_ENDPOINT & GITHUB_MIRROR env vars take highest priority.
	t.Setenv("HF_ENDPOINT", "https://hf.corp.internal/")
	t.Setenv("GITHUB_MIRROR", "https://gh.corp.internal")
	cfgEnv := mgr.resolveDownloadNetworkConfig(context.Background())
	modelEnv := resolveModelURLs(cfgEnv, MMProjName)
	if len(modelEnv) < 2 || modelEnv[0] != "https://hf.corp.internal"+modelRepoSuffix+MMProjName {
		t.Fatalf("expected custom HF_ENDPOINT first, got %v", modelEnv)
	}
	rtEnv := resolveRuntimeURLs(cfgEnv)
	if len(rtEnv) < 2 || rtEnv[0] != "https://gh.corp.internal/ggml-org/llama.cpp/releases/download/b10950/llama-b10950-bin-win-vulkan-x64.zip" {
		t.Fatalf("expected origin-style GITHUB_MIRROR first, got %v", rtEnv)
	}

	// 4. Prefix-proxy & {url} template styles for GITHUB_MIRROR.
	if got := applyGitHubMirror(RuntimeURL, "https://mirror.ghproxy.com/"); got != "https://mirror.ghproxy.com/"+RuntimeURL {
		t.Fatalf("unexpected ghproxy prefix rewrite: %q", got)
	}
	if got := applyGitHubMirror(RuntimeURL, "https://dl.example.com/proxy?target={url}"); got != "https://dl.example.com/proxy?target="+RuntimeURL {
		t.Fatalf("unexpected {url} template rewrite: %q", got)
	}
}

func TestR2T2EnsureResourcesUsesUnifiedProxyAndMirrors(t *testing.T) {
	t.Setenv("ASTRORDER_HF_ENDPOINT", "")
	t.Setenv("ASTRORDER_GITHUB_MIRROR", "")
	t.Setenv("GH_PROXY", "")
	t.Setenv("ASTRORDER_MIRROR", "")
	t.Setenv("ASTRORDER_PROXY_URL", "")
	t.Setenv("ASTRORDER_R2T2_MODEL_BASE", "")
	t.Setenv("ASTRORDER_R2T2_RUNTIME_URL", "")

	exeName := "llama-server"
	if runtime.GOOS == "windows" {
		exeName = "llama-server.exe"
	}
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	fw, err := zw.Create(exeName)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("fake-llama-binary"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	runtimeZipBytes := zipBuf.Bytes()

	// Spin up a real local HTTP proxy server that intercepts mirror URLs!
	var (
		mu          sync.Mutex
		proxiedURLs []string
	)
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		proxiedURLs = append(proxiedURLs, r.URL.String())
		mu.Unlock()

		switch {
		case strings.HasSuffix(r.URL.Path, ".zip"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(runtimeZipBytes)
		case strings.HasSuffix(r.URL.Path, "/"+MMProjName):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fake-mmproj-model"))
		case strings.HasSuffix(r.URL.Path, "/"+ModelName):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fake-gguf-model"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer proxySrv.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Configure unified proxy in toolchain:network and custom mirrors via env.
	if err := st.SetPreference(context.Background(), prefToolchainNetwork, map[string]any{
		"default": map[string]any{
			"proxy_url": proxySrv.URL,
		},
	}, true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HF_ENDPOINT", "http://hf-mirror.invalid")
	t.Setenv("GITHUB_MIRROR", "http://gh-mirror.invalid")

	resDir := t.TempDir()
	mgr, err := newLocalR2T2Manager(Options{
		Store:       st,
		ResourceDir: resDir,
	}, context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.ensureResources(context.Background()); err != nil {
		t.Fatalf("ensureResources through unified proxy & mirrors failed: %v", err)
	}

	// Verify all 3 resources were downloaded via the configured proxy and mirror hosts!
	mu.Lock()
	gotURLs := append([]string(nil), proxiedURLs...)
	mu.Unlock()
	if len(gotURLs) != 3 {
		t.Fatalf("expected 3 proxied downloads, got %d: %v", len(gotURLs), gotURLs)
	}
	var sawGHMirror, sawHFMirror bool
	for _, u := range gotURLs {
		if strings.HasPrefix(u, "http://gh-mirror.invalid/") {
			sawGHMirror = true
		}
		if strings.HasPrefix(u, "http://hf-mirror.invalid/") {
			sawHFMirror = true
		}
	}
	if !sawGHMirror || !sawHFMirror {
		t.Fatalf("expected downloads via configured gh-mirror and hf-mirror through proxy, got %v", gotURLs)
	}

	if data, err := os.ReadFile(mgr.modelPath); err != nil || string(data) != "fake-gguf-model" {
		t.Fatalf("unexpected model file: err=%v data=%q", err, string(data))
	}
	if data, err := os.ReadFile(mgr.mmprojPath); err != nil || string(data) != "fake-mmproj-model" {
		t.Fatalf("unexpected mmproj file: err=%v data=%q", err, string(data))
	}
	if data, err := os.ReadFile(mgr.serverPath); err != nil || string(data) != "fake-llama-binary" {
		t.Fatalf("unexpected server binary: err=%v data=%q", err, string(data))
	}
}

func TestR2T2DownloadCandidateFallbackAndPartCleanup(t *testing.T) {
	var attempts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, r.URL.Path)
		if r.URL.Path == "/primary" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fallback-ok"))
	}))
	defer srv.Close()

	resDir := t.TempDir()
	mgr, err := newLocalR2T2Manager(Options{
		ResourceDir: resDir,
		HTTPClient:  srv.Client(),
	}, context.Background())
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(resDir, "test.bin")
	err = mgr.downloadFileFromCandidates(context.Background(), srv.Client(), []string{
		srv.URL + "/primary",
		srv.URL + "/fallback",
	}, target, 0, 1)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if len(attempts) != 2 || attempts[0] != "/primary" || attempts[1] != "/fallback" {
		t.Fatalf("unexpected candidate attempts: %v", attempts)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "fallback-ok" {
		t.Fatalf("unexpected target content: err=%v data=%q", err, string(got))
	}
	if _, err := os.Stat(target + ".part"); !os.IsNotExist(err) {
		t.Fatalf("expected .part file cleaned up, stat err=%v", err)
	}
}

func TestR2T2DownloadClientRejectsInvalidProxyAndFallsBackToEnvProxy(t *testing.T) {
	mgr, err := newLocalR2T2Manager(Options{ResourceDir: t.TempDir()}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.buildDownloadClient(downloadNetworkConfig{ProxyURL: "not-a-valid-proxy"}); err == nil {
		t.Fatalf("expected invalid proxy URL to return an error")
	}

	var envProxied bool
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envProxied = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("via-env-proxy"))
	}))
	defer proxySrv.Close()

	t.Setenv("HTTP_PROXY", proxySrv.URL)
	t.Setenv("NO_PROXY", "")
	client, err := mgr.buildDownloadClient(downloadNetworkConfig{})
	if err != nil {
		t.Fatalf("buildDownloadClient: %v", err)
	}
	target := filepath.Join(t.TempDir(), "env-proxy.bin")
	if err := mgr.downloadFile(context.Background(), client, "http://upstream.invalid/model.bin", target, 0, 1); err != nil {
		t.Fatalf("download via HTTP_PROXY env failed: %v", err)
	}
	if !envProxied {
		t.Fatalf("expected request to route through HTTP_PROXY environment variable")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "via-env-proxy" {
		t.Fatalf("unexpected downloaded data: err=%v data=%q", err, string(got))
	}
}
