package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsProductionAndRemoteTargets(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	for _, backend := range []string{"http://127.0.0.1:30001", "http://example.com:30102", "http://127.0.0.1:30102/api", "http://user:pass@127.0.0.1:30102", "https://127.0.0.1:30102"} {
		if _, err := loadConfig(backend, "", false); err == nil {
			t.Errorf("accepted unsafe target %q", backend)
		}
	}
	for _, name := range []string{"Astrorder", "Astrorder/a", "Astrorder/desktop"} {
		if _, err := loadConfig("", filepath.Join(os.Getenv("LOCALAPPDATA"), name), false); err == nil {
			t.Errorf("accepted production directory %q", name)
		}
	}
	if _, err := loadConfig("http://127.0.0.1:30102", "", false); err != nil {
		t.Fatal(err)
	}
}

func TestAssetsKeepBusinessRequestsOnExplicitBackend(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/test" || r.URL.Query().Get("session_id") != "isolation" {
			t.Errorf("lost route or context: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	cfg, err := loadConfig(upstream.URL, "", false)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := assetHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/test?session_id=isolation", nil))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("proxy response: %d %s", w.Code, w.Body.String())
	}
	cfg.Backend = nil
	handler, err = assetHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/sessions", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured backend status = %d", w.Code)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/chat", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("React deep link status = %d", w.Code)
	}
}

func TestProxyPreservesWebviewBodyWithoutContentLength(t *testing.T) {
	const payload = `{"token":"isolated-fixture"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != payload {
			t.Errorf("forwarded body = %q, error = %v", body, err)
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := assetHandler(Config{Backend: target})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session", nil)
	// WebView2 supplies an opaque stream and may omit Content-Length.
	r.Body = io.NopCloser(strings.NewReader(payload))
	r.ContentLength = 0
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("proxy status = %d", w.Code)
	}
}

func TestIndexLoadsDesktopBridgeBeforeReactEntry(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	cfg, err := loadConfig("http://127.0.0.1:30102", "", false)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := assetHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/", "/index.html", "/chat/deep/link"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		body := w.Body.String()
		bridge := strings.Index(body, `src="`+bridgePath+`"`)
		entry := strings.Index(body, `<script type="module" crossorigin`)
		if w.Code != 200 || bridge < 0 || entry < 0 || bridge > entry {
			t.Fatalf("%s: bridge must precede React entry (code=%d bridge=%d entry=%d)", route, w.Code, bridge, entry)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", bridgePath, nil))
	script := w.Body.String()
	if !strings.Contains(script, `window.__ASTRORDER_BACKEND_HOST__ = "127.0.0.1:30102"`) || !strings.Contains(script, "svc.ReportReady()") {
		t.Fatalf("bridge script missing backend host or ready report:\n%s", script)
	}
	for _, forbidden := range []string{"CopyAccessToken", "confirm", "SaveImageDialog"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("bridge exposes privileged or bogus capability %q", forbidden)
		}
	}
	if w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("bridge content type = %q", w.Header().Get("Content-Type"))
	}
}

func TestBridgeWithoutBackendInjectsNoHost(t *testing.T) {
	if !strings.Contains(string(bridgeScript(Config{})), `window.__ASTRORDER_BACKEND_HOST__ = "";`) {
		t.Fatal("bridge must not inject a default backend")
	}
}

func TestDaemonStopConfirmationTreatsAmbiguityAsActive(t *testing.T) {
	cases := []struct {
		name   string
		daemon map[string]any
		want   bool
	}{
		{"unreadable", nil, true},
		{"unknown state", map[string]any{"state": "unknown"}, true},
		{"active sessions", map[string]any{"state": "running", "active_sessions": map[string]any{"s": "running"}}, true},
		{"missing activity", map[string]any{"state": "running"}, true},
		{"confirmed idle", map[string]any{"state": "running", "active_sessions": map[string]any{}}, false},
	}
	for _, tc := range cases {
		if got, _ := daemonStopNeedsConfirmation(tc.daemon); got != tc.want {
			t.Errorf("%s: needs confirmation = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDaemonStopWithoutDialogIsRefused(t *testing.T) {
	// No app/dialog: confirmation can never be granted implicitly.
	if (&ShellService{}).confirm("stop", "") {
		t.Fatal("confirmation granted without a native dialog")
	}
}

func TestOpenExternalRejectsNonWebURLs(t *testing.T) {
	s := &ShellService{}
	for _, target := range []string{"file:///C:/Windows/System32/calc.exe", "C:\\Windows\\notepad.exe", "shell:startup", "javascript:alert(1)", "http://", "ms-settings:"} {
		if err := s.OpenExternal(target); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("accepted %q: %v", target, err)
		}
	}
}
