package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/memory"
	"astrorder.dev/app-server/internal/store"
)

func testConfig() config.Config {
	return config.Config{
		Listen:          "127.0.0.1:30002",
		BrowserSecret:   "browser-secret",
		ConnectorSecret: "connector-secret",
		AllowedOrigins:  []string{"http://testserver"},
	}
}

func request(t *testing.T, handler http.Handler, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestHealthAndUnknownAPIWithoutUpstream(t *testing.T) {
	handler := NewHandler(Options{Config: testConfig()})
	health := request(t, handler, http.MethodGet, "/health", nil, nil)
	var healthPayload map[string]any
	if err := json.Unmarshal(health.Body.Bytes(), &healthPayload); err != nil {
		t.Fatal(err)
	}
	if health.Code != http.StatusOK || healthPayload["status"] != "ok" || healthPayload["service"] != "astrorder" || healthPayload["protocol_version"] != float64(1) {
		t.Fatalf("health = %d %s", health.Code, health.Body.String())
	}
	missing := request(t, handler, http.MethodGet, "/api/v1/not-migrated", nil, nil)
	if missing.Code != http.StatusNotFound || missing.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("missing API = %d %#v %s", missing.Code, missing.Header(), missing.Body.String())
	}
	wrongMethod := request(t, handler, http.MethodPost, "/health", nil, nil)
	if wrongMethod.Code != http.StatusMethodNotAllowed || wrongMethod.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("health wrong method = %d %#v", wrongMethod.Code, wrongMethod.Header())
	}
}

func TestAuthSessionLifecycle(t *testing.T) {
	handler := NewHandler(Options{Config: testConfig()})
	wrong := request(t, handler, http.MethodPost, "/api/v1/auth/session", strings.NewReader(`{"token":"wrong"}`), map[string]string{"Origin": "http://testserver"})
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", wrong.Code)
	}
	evil := request(t, handler, http.MethodPost, "/api/v1/auth/session", strings.NewReader(`{"token":"browser-secret"}`), map[string]string{"Origin": "https://evil.example"})
	if evil.Code != http.StatusForbidden {
		t.Fatalf("evil origin status = %d", evil.Code)
	}
	invalidBodies := []string{
		`{}`,
		`{"token":""}`,
		`{"token":1}`,
		`null`,
		`{broken`,
		`{"token":"browser-secret"} {"second":true}`,
	}
	for _, body := range invalidBodies {
		invalid := request(t, handler, http.MethodPost, "/api/v1/auth/session", strings.NewReader(body), map[string]string{
			"Content-Type": "application/json",
			"Origin":       "http://testserver",
		})
		if invalid.Code != http.StatusUnprocessableEntity || strings.TrimSpace(invalid.Body.String()) != `{"detail":"Invalid request"}` {
			t.Fatalf("invalid request %q = %d %s", body, invalid.Code, invalid.Body.String())
		}
	}
	extra := request(t, handler, http.MethodPost, "/api/v1/auth/session", strings.NewReader(`{"token":"browser-secret","extra":true}`), map[string]string{"Origin": "http://testserver"})
	if extra.Code != http.StatusUnprocessableEntity || strings.TrimSpace(extra.Body.String()) != `{"detail":"Invalid request"}` {
		t.Fatalf("Pydantic extra=forbid request = %d %s", extra.Code, extra.Body.String())
	}
	login := request(t, handler, http.MethodPost, "/api/v1/auth/session", strings.NewReader(`{"token":"browser-secret"}`), map[string]string{"Origin": "http://testserver"})
	if login.Code != http.StatusOK || !strings.Contains(login.Header().Get("Set-Cookie"), "astrorder_session=browser-secret") || !strings.Contains(login.Header().Get("Set-Cookie"), "HttpOnly") || !strings.Contains(login.Header().Get("Set-Cookie"), "SameSite=Lax") {
		t.Fatalf("login = %d %#v %s", login.Code, login.Header(), login.Body.String())
	}

	restored := request(t, handler, http.MethodGet, "/api/v1/auth/session", nil, map[string]string{"Authorization": "Bearer browser-secret"})
	if restored.Code != http.StatusOK || !strings.Contains(restored.Header().Get("Set-Cookie"), "astrorder_session=browser-secret") || strings.TrimSpace(restored.Body.String()) != `{"authenticated":true}` {
		t.Fatalf("restored = %d %#v %s", restored.Code, restored.Header(), restored.Body.String())
	}
	logout := request(t, handler, http.MethodDelete, "/api/v1/auth/session", nil, map[string]string{"Origin": "http://testserver"})
	if logout.Code != http.StatusNoContent || !strings.Contains(logout.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout = %d %#v", logout.Code, logout.Header())
	}
}

func TestDesktopShutdownRequiresLoopbackBrowserBearer(t *testing.T) {
	calls := 0
	handler := NewHandler(Options{Config: testConfig(), DesktopShutdown: func() { calls++ }})
	shutdownRequest := func(remoteAddr, credential string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/_desktop/shutdown", nil)
		req.RemoteAddr = remoteAddr
		req.Header.Set("Authorization", "Bearer "+credential)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	connector := shutdownRequest("127.0.0.1:4567", "connector-secret")
	if connector.Code != http.StatusForbidden || calls != 0 {
		t.Fatalf("connector shutdown = %d calls=%d", connector.Code, calls)
	}

	recorder := shutdownRequest("192.168.1.20:4567", "browser-secret")
	if recorder.Code != http.StatusForbidden || calls != 0 {
		t.Fatalf("LAN shutdown = %d calls=%d", recorder.Code, calls)
	}

	allowed := shutdownRequest("127.0.0.1:4567", "browser-secret")
	if allowed.Code != http.StatusOK || calls != 1 || strings.TrimSpace(allowed.Body.String()) != `{"stopping":true}` {
		t.Fatalf("allowed shutdown = %d calls=%d body=%s", allowed.Code, calls, allowed.Body.String())
	}
}

func TestCORSAndGzipCompatibility(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(staticDir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "assets", "app.js"), bytes.Repeat([]byte("a"), 2000), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.StaticDir = staticDir
	handler := NewHandler(Options{Config: cfg})

	preflight := request(t, handler, http.MethodOptions, "/api/v1/auth/session", nil, map[string]string{
		"Origin":                         "http://testserver",
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "Authorization, Content-Type",
	})
	if preflight.Code != http.StatusOK || preflight.Header().Get("Access-Control-Allow-Origin") != "http://testserver" || preflight.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("preflight = %d %#v %s", preflight.Code, preflight.Header(), preflight.Body.String())
	}
	if got := preflight.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, PATCH, DELETE, OPTIONS" {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}
	if got := preflight.Header().Get("Access-Control-Allow-Headers"); got != "Accept, Accept-Language, Authorization, Content-Language, Content-Type" {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
	if got := preflight.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("Access-Control-Max-Age = %q", got)
	}
	if got := preflight.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" || strings.TrimSpace(preflight.Body.String()) != "OK" {
		t.Fatalf("preflight body contract = %q %q", got, preflight.Body.String())
	}
	for _, tc := range []struct {
		name, method, headers, detail string
	}{
		{"method", "TRACE", "Content-Type", "Disallowed CORS method"},
		{"headers", "POST", "X-Private", "Disallowed CORS headers"},
		{"both", "TRACE", "X-Private", "Disallowed CORS method, headers"},
	} {
		t.Run("reject "+tc.name, func(t *testing.T) {
			response := request(t, handler, http.MethodOptions, "/api/v1/auth/session", nil, map[string]string{
				"Origin":                         "http://testserver",
				"Access-Control-Request-Method":  tc.method,
				"Access-Control-Request-Headers": tc.headers,
			})
			if response.Code != http.StatusBadRequest || strings.TrimSpace(response.Body.String()) != tc.detail || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
				t.Fatalf("preflight rejection = %d %q %#v", response.Code, response.Body.String(), response.Header())
			}
		})
	}
	asset := request(t, handler, http.MethodGet, "/assets/app.js", nil, map[string]string{"Accept-Encoding": "gzip"})
	if asset.Code != http.StatusOK || asset.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset = %d %#v", asset.Code, asset.Header())
	}
}

func TestStaticSPAAndReserved404(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<main>Astrorder SPA</main>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "sw.js"), []byte("service-worker"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.StaticDir = staticDir
	handler := NewHandler(Options{Config: cfg})
	spa := request(t, handler, http.MethodGet, "/chat/session-1", nil, nil)
	if spa.Code != http.StatusOK || !strings.Contains(spa.Body.String(), "Astrorder SPA") || spa.Header().Get("Cache-Control") != "" {
		t.Fatalf("SPA fallback = %d %#v %s", spa.Code, spa.Header(), spa.Body.String())
	}
	sw := request(t, handler, http.MethodGet, "/sw.js", nil, nil)
	if sw.Code != http.StatusOK || !strings.Contains(sw.Header().Get("Cache-Control"), "no-cache") {
		t.Fatalf("service worker = %d %#v", sw.Code, sw.Header())
	}
	api := request(t, handler, http.MethodGet, "/api/v1/missing", nil, nil)
	if api.Code != http.StatusNotFound || strings.Contains(api.Body.String(), "Astrorder SPA") {
		t.Fatalf("reserved API = %d %s", api.Code, api.Body.String())
	}
}

func TestStaticMissingSemanticsAndPathConfinement(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("SPA"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.StaticDir = staticDir
	handler := NewHandler(Options{Config: cfg})

	missingAsset := request(t, handler, http.MethodGet, "/assets/missing.js", nil, nil)
	if missingAsset.Code != http.StatusOK || strings.TrimSpace(missingAsset.Body.String()) != "SPA" || missingAsset.Header().Get("Cache-Control") != "" {
		t.Fatalf("missing asset SPA fallback = %d %#v %q", missingAsset.Code, missingAsset.Header(), missingAsset.Body.String())
	}
	if err := os.WriteFile(filepath.Join(staticDir, "404.html"), []byte("CUSTOM404"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(staticDir, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "folder", "index.html"), []byte("FOLDER"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingAsset = request(t, NewHandler(Options{Config: cfg}), http.MethodGet, "/assets/missing.js", nil, nil)
	if missingAsset.Code != http.StatusNotFound || strings.TrimSpace(missingAsset.Body.String()) != "CUSTOM404" || !strings.Contains(missingAsset.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("missing asset 404 file = %d %#v %q", missingAsset.Code, missingAsset.Header(), missingAsset.Body.String())
	}
	folderRedirect := request(t, NewHandler(Options{Config: cfg}), http.MethodGet, "/folder?keep=yes", nil, nil)
	if folderRedirect.Code != http.StatusTemporaryRedirect || folderRedirect.Header().Get("Location") != "/folder/?keep=yes" {
		t.Fatalf("directory redirect = %d %#v", folderRedirect.Code, folderRedirect.Header())
	}
	folderIndex := request(t, NewHandler(Options{Config: cfg}), http.MethodGet, "/folder/", nil, nil)
	if folderIndex.Code != http.StatusOK || strings.TrimSpace(folderIndex.Body.String()) != "FOLDER" {
		t.Fatalf("directory index = %d %q", folderIndex.Code, folderIndex.Body.String())
	}

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(staticDir, "safe"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "safe", "inside.txt"), []byte("INSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outsidePath, filepath.Join(staticDir, "escape.txt")); err != nil {
			t.Fatal(err)
		}
		escape := request(t, NewHandler(Options{Config: cfg}), http.MethodGet, "/escape.txt", nil, nil)
		if strings.Contains(escape.Body.String(), "PRIVATE") {
			t.Fatalf("candidate symlink escaped static root: %d %q", escape.Code, escape.Body.String())
		}
	}
	for _, target := range []string{"/..%5coutside.txt", "/safe%5c..%5c..%5coutside.txt"} {
		traversal := request(t, NewHandler(Options{Config: cfg}), http.MethodGet, target, nil, nil)
		if strings.Contains(traversal.Body.String(), "PRIVATE") {
			t.Fatalf("backslash traversal %q escaped root: %d %q", target, traversal.Code, traversal.Body.String())
		}
	}
}

func TestRecoveryReturnsStableJSONForUncommittedPanics(t *testing.T) {
	loggerBuffer := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(loggerBuffer, nil))
	handler := recoverPanics(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret-panic-detail")
	}))
	response := request(t, handler, http.MethodGet, "/api/v1/panic?token=hidden", nil, nil)
	if response.Code != http.StatusInternalServerError || strings.TrimSpace(response.Body.String()) != `{"detail":"Internal server error"}` {
		t.Fatalf("panic response = %d %#v %q", response.Code, response.Header(), response.Body.String())
	}
	if strings.Contains(loggerBuffer.String(), "secret-panic-detail") || strings.Contains(loggerBuffer.String(), "token=hidden") {
		t.Fatalf("panic log exposed detail: %s", loggerBuffer.String())
	}
}

func TestStaticRootSymlinkPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a directory symlink requires privileges on Windows")
	}
	realRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(realRoot, "index.html"), []byte("ROOT"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "static-link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.StaticDir = link
	response := request(t, NewHandler(Options{Config: cfg}), http.MethodGet, "/", nil, nil)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "ROOT" {
		t.Fatalf("root symlink policy = %d %q", response.Code, response.Body.String())
	}
}

func TestUnknownLegacyRouteIsNotProxied(t *testing.T) {
	handler := NewHandler(Options{Config: testConfig()})
	for _, headers := range []map[string]string{nil, {"Authorization": "Bearer browser-secret"}} {
		response := request(t, handler, http.MethodGet, "/api/v1/legacy/1", nil, headers)
		if response.Code != http.StatusNotFound {
			t.Fatalf("unknown route = %d", response.Code)
		}
	}
}

func TestGzipMatchesStarletteStreamingSSEAndQZeroBehavior(t *testing.T) {
	largeBody := bytes.Repeat([]byte("a"), 2000)
	handler := gzipResponses(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/body":
			response.Header().Set("Content-Type", "text/plain")
			response.Header().Set("Content-Length", "2000")
			_, _ = response.Write(largeBody)
		case "/stream":
			response.Header().Set("Content-Type", "text/plain")
			response.Header().Set("Content-Length", "1000")
			_, _ = response.Write(bytes.Repeat([]byte("a"), 500))
			response.(http.Flusher).Flush()
			_, _ = response.Write(bytes.Repeat([]byte("b"), 500))
		case "/sse":
			response.Header().Set("Content-Type", "text/event-stream")
			response.Header().Set("Content-Length", "2008")
			_, _ = response.Write(append(append([]byte("data: "), largeBody...), '\n', '\n'))
		case "/partial":
			response.Header().Set("Content-Type", "text/plain")
			response.WriteHeader(http.StatusPartialContent)
			_, _ = response.Write(largeBody)
		}
	}))

	for _, target := range []string{"/body", "/stream"} {
		response := request(t, handler, http.MethodGet, target, nil, map[string]string{"Accept-Encoding": "gzip;q=0"})
		if response.Header().Get("Content-Encoding") != "gzip" || response.Header().Get("Content-Length") != "" || !strings.Contains(response.Header().Get("Vary"), "Accept-Encoding") {
			t.Fatalf("gzip q=0 %s = %d %#v", target, response.Code, response.Header())
		}
	}
	for _, target := range []string{"/sse", "/partial"} {
		response := request(t, handler, http.MethodGet, target, nil, map[string]string{"Accept-Encoding": "gzip"})
		if response.Header().Get("Content-Encoding") != "" {
			t.Fatalf("excluded gzip %s = %d %#v", target, response.Code, response.Header())
		}
	}
}

func TestRegistrationsAreTheConstructedDispatchTable(t *testing.T) {
	registrations := Registrations()
	want := map[string]Registration{
		"GET /api/v1/draft-shelf":     {Kind: KindHTTP, Method: http.MethodGet, Path: "/api/v1/draft-shelf", Auth: AuthBrowser, Ownership: OwnershipGo},
		"PUT /api/v1/draft-shelf":     {Kind: KindHTTP, Method: http.MethodPut, Path: "/api/v1/draft-shelf", Auth: AuthBrowser, Ownership: OwnershipGo},
		"GET /health":                 {Kind: KindHTTP, Method: http.MethodGet, Path: "/health", Auth: AuthPublic, Ownership: OwnershipGo},
		"POST /api/v1/auth/session":   {Kind: KindHTTP, Method: http.MethodPost, Path: "/api/v1/auth/session", Auth: AuthPublic, Ownership: OwnershipGo},
		"GET /api/v1/auth/session":    {Kind: KindHTTP, Method: http.MethodGet, Path: "/api/v1/auth/session", Auth: AuthPublic, Ownership: OwnershipGo},
		"DELETE /api/v1/auth/session": {Kind: KindHTTP, Method: http.MethodDelete, Path: "/api/v1/auth/session", Auth: AuthPublic, Ownership: OwnershipGo},
		"POST /_desktop/shutdown":     {Kind: KindHTTP, Method: http.MethodPost, Path: "/_desktop/shutdown", Auth: AuthDesktopBearer, Ownership: OwnershipGo},
	}
	for _, action := range []string{"create", "poll", "inspect", "approve"} {
		policy := AuthPublic
		if action == "inspect" || action == "approve" {
			policy = AuthBrowser
		}
		p := "/api/v1/auth/qr/" + action
		want["POST "+p] = Registration{Kind: KindHTTP, Method: http.MethodPost, Path: p, Auth: policy, Ownership: OwnershipGo}
	}
	for _, path := range []string{"/openapi.json", "/docs", "/redoc", "/docs/oauth2-redirect"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			want[method+" "+path] = Registration{Kind: KindHTTP, Method: method, Path: path, Auth: AuthPublic, Ownership: OwnershipGo}
		}
	}
	if len(registrations) != len(want) {
		t.Fatalf("Registrations length = %d, want %d: %#v", len(registrations), len(want), registrations)
	}
	for _, registration := range registrations {
		key := registration.Method + " " + registration.Path
		if expected, ok := want[key]; !ok || registration != expected {
			t.Fatalf("registration %q = %#v, want %#v present=%v", key, registration, expected, ok)
		}
	}

	handler := NewHandler(Options{Config: testConfig(), DesktopShutdown: func() {}})
	for _, registration := range registrations {
		req := httptest.NewRequest(registration.Method, registration.Path, nil)
		req.RemoteAddr = "127.0.0.1:4567"
		if registration.Auth == AuthDesktopBearer {
			req.Header.Set("Authorization", "Bearer browser-secret")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code == http.StatusNotFound || response.Code == http.StatusMethodNotAllowed {
			t.Fatalf("registered route was not dispatched: %#v => %d %s", registration, response.Code, response.Body.String())
		}
	}
}

func TestAccessLoggingDoesNotRecordQueryCredentials(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := NewHandler(Options{Config: testConfig(), Logger: logger})
	_ = request(t, handler, http.MethodGet, "/health?token=very-secret", nil, map[string]string{"Authorization": "Bearer another-secret"})
	logged := output.String()
	if strings.Contains(logged, "very-secret") || strings.Contains(logged, "another-secret") || !strings.Contains(logged, `"path":"/health"`) {
		t.Fatalf("unsafe access log: %s", logged)
	}
}

// TestMemoryUpstreamIntegration exercises memory config, search, and health routes against a local httptest upstream.
func TestMemoryUpstreamIntegration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "openviking"})
		case "/api/v1/search/search":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"memories": []any{}}})
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer upstream.Close()

	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfgFile := filepath.Join(tempDir, "ovcli.conf")
	memConfigService, err := memory.NewConfigService(memory.ConfigServiceOptions{
		Preferences:  st,
		FallbackPath: cfgFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	targetResolver := &application.AppTargetResolver{Store: st}
	syncer, err := memory.NewSyncer(memory.SyncerOptions{
		Resolver: targetResolver,
		FS:       application.DispatchingTargetFS{},
		Runner:   application.DispatchingRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}

	clientFactory := memory.NewClientFactory(memConfigService, memory.OpenVikingClientOptions{})
	memSvc, err := memory.NewService(memory.ServiceOptions{
		Config:  memConfigService,
		Client:  clientFactory,
		Targets: targetResolver,
		Syncer:  syncer,
		Authorize: func(r *http.Request) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	handler := NewHandler(Options{
		Config:        cfg,
		Store:         st,
		MemoryService: memSvc,
	})

	authHeader := map[string]string{
		"Authorization": "Bearer browser-secret",
		"Origin":        "http://testserver",
		"Content-Type":  "application/json",
	}

	// 1. Post config with upstream URL
	postCfgBody := fmt.Sprintf(`{"url":%q}`, upstream.URL)
	cfgResp := request(t, handler, http.MethodPost, "/api/v1/memory/config", strings.NewReader(postCfgBody), authHeader)
	if cfgResp.Code != http.StatusOK {
		t.Fatalf("memory post config failed: %d %s", cfgResp.Code, cfgResp.Body.String())
	}

	// 2. Query health against upstream
	healthResp := request(t, handler, http.MethodGet, "/api/v1/memory/health", nil, authHeader)
	if healthResp.Code != http.StatusOK || !strings.Contains(healthResp.Body.String(), "ok") {
		t.Fatalf("memory health failed: %d %s", healthResp.Code, healthResp.Body.String())
	}

	// 3. Search memories against upstream
	searchResp := request(t, handler, http.MethodPost, "/api/v1/memory/search", strings.NewReader(`{"query":"needle","limit":5}`), authHeader)
	if searchResp.Code != http.StatusOK {
		t.Fatalf("memory search failed: %d %s", searchResp.Code, searchResp.Body.String())
	}
}

func TestDynamicOriginLookupIsInjected(t *testing.T) {
	lookup := auth.OriginLookupFunc(func(context.Context) (auth.OriginPreferences, error) {
		return auth.OriginPreferences{AllowedOrigins: []string{"https://dynamic.example"}}, nil
	})
	handler := NewHandler(Options{Config: testConfig(), DynamicOrigins: lookup})
	login := request(t, handler, http.MethodPost, "/api/v1/auth/session", strings.NewReader(`{"token":"browser-secret"}`), map[string]string{"Origin": "https://dynamic.example"})
	if login.Code != http.StatusOK {
		t.Fatalf("dynamic origin login = %d %s", login.Code, login.Body.String())
	}
}

func TestProductDomainIntegration(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	attachmentsDir := filepath.Join(tempDir, "attachments")
	if err := os.MkdirAll(attachmentsDir, 0700); err != nil {
		t.Fatal(err)
	}
	stagingDir := filepath.Join(tempDir, "staging")
	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.AttachmentsDir = attachmentsDir
	cfg.StagingDir = stagingDir
	cfg.DefaultWorkspace = tempDir

	handler := NewHandler(Options{
		Config: cfg,
		Store:  st,
	})

	authHeader := map[string]string{
		"Authorization": "Bearer browser-secret",
		"Origin":        "http://testserver",
	}

	// 1. Preferences GET /api/v1/preferences
	prefResp := request(t, handler, http.MethodGet, "/api/v1/preferences", nil, authHeader)
	if prefResp.Code != http.StatusOK {
		t.Fatalf("preferences GET status = %d: %s", prefResp.Code, prefResp.Body.String())
	}

	// 2. Preferences PATCH /api/v1/preferences
	patchBody := `{"appearance":{"project1":{"color":"blue","icon":"folder"}}}`
	prefPatchResp := request(t, handler, http.MethodPatch, "/api/v1/preferences", strings.NewReader(patchBody), authHeader)
	if prefPatchResp.Code != http.StatusOK {
		t.Fatalf("preferences PATCH status = %d: %s", prefPatchResp.Code, prefPatchResp.Body.String())
	}

	// Verify persistence in store
	prefAfter := request(t, handler, http.MethodGet, "/api/v1/preferences", nil, authHeader)
	if prefAfter.Code != http.StatusOK || !strings.Contains(prefAfter.Body.String(), "project1") {
		t.Fatalf("preferences after patch = %d: %s", prefAfter.Code, prefAfter.Body.String())
	}

	// 3. Files write /api/v1/system/write-file
	testFile := filepath.Join(tempDir, "hello.txt")
	writeReqBody := fmt.Sprintf(`{"path":%q,"content":"hello world"}`, testFile)
	writeFileResp := request(t, handler, http.MethodPost, "/api/v1/system/write-file", strings.NewReader(writeReqBody), authHeader)
	if writeFileResp.Code != http.StatusOK {
		t.Fatalf("files write status = %d: %s", writeFileResp.Code, writeFileResp.Body.String())
	}

	// 4. Files exists /api/v1/files/exists
	existsReqBody := fmt.Sprintf(`{"workspace":%q,"paths":["hello.txt"]}`, tempDir)
	existsResp := request(t, handler, http.MethodPost, "/api/v1/files/exists", strings.NewReader(existsReqBody), authHeader)
	if existsResp.Code != http.StatusOK || !strings.Contains(existsResp.Body.String(), "hello.txt") {
		t.Fatalf("files exists status = %d: %s", existsResp.Code, existsResp.Body.String())
	}

	// 5. Files raw /api/v1/files/raw
	rawURL := "/api/v1/files/raw?path=" + url.QueryEscape(testFile)
	rawResp := request(t, handler, http.MethodGet, rawURL, nil, authHeader)
	if rawResp.Code != http.StatusOK || !strings.Contains(rawResp.Body.String(), "hello world") {
		t.Fatalf("files raw status = %d: %s", rawResp.Code, rawResp.Body.String())
	}

	// 6. Dynamic Origin lookup via store
	if err := st.SetPreference(context.Background(), "network:allowed_origins", []any{"http://custom-origin.com"}, true); err != nil {
		t.Fatal(err)
	}
	customOriginHeader := map[string]string{
		"Origin": "http://custom-origin.com",
	}
	loginResp := request(t, handler, http.MethodPost, "/api/v1/auth/session", strings.NewReader(`{"token":"browser-secret"}`), customOriginHeader)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("custom dynamic origin login = %d: %s", loginResp.Code, loginResp.Body.String())
	}

	// 7. Event Hub WebSocket streaming & replay
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/v1/events?after=0"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer browser-secret"},
			"Origin":        []string{"http://testserver"},
		},
	})
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.CloseNow()

	// Cause a file write that publishes an event
	anotherFile := filepath.Join(tempDir, "event.txt")
	writeEventFile := fmt.Sprintf(`{"path":%q,"content":"testing events"}`, anotherFile)
	_ = request(t, handler, http.MethodPost, "/api/v1/system/write-file", strings.NewReader(writeEventFile), authHeader)

	// Read event from websocket
	readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
	defer readCancel()
	_, msg, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("read websocket event: %v", err)
	}
	if !strings.Contains(string(msg), "files.changed") {
		t.Fatalf("expected files.changed event, got: %s", string(msg))
	}

	// 8. Memory health endpoint (/api/v1/memory/health)
	memConfigService, err := memory.NewConfigService(memory.ConfigServiceOptions{
		Preferences:  st,
		FallbackPath: filepath.Join(tempDir, "missing-ov.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	targetResolver := &application.AppTargetResolver{Store: st}
	syncer, err := memory.NewSyncer(memory.SyncerOptions{
		Resolver: targetResolver,
		FS:       memory.LocalTargetFS{},
		Runner:   application.LocalRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	memSvc, err := memory.NewService(memory.ServiceOptions{
		Config:    memConfigService,
		Client:    memory.NewClientFactory(memConfigService, memory.OpenVikingClientOptions{}),
		Targets:   targetResolver,
		Syncer:    syncer,
		Authorize: func(r *http.Request) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	handlerWithMem := NewHandler(Options{
		Config:        cfg,
		Store:         st,
		MemoryService: memSvc,
	})

	memHealthResp := request(t, handlerWithMem, http.MethodGet, "/api/v1/memory/health", nil, authHeader)
	if memHealthResp.Code == http.StatusNotFound {
		t.Fatalf("memory health route was not mounted: %d %s", memHealthResp.Code, memHealthResp.Body.String())
	}

	// 9. Memory route authentication verification
	memUnauthResp := request(t, handlerWithMem, http.MethodGet, "/api/v1/memory/health", nil, nil)
	if memUnauthResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized for unauthenticated memory request, got %d", memUnauthResp.Code)
	}
}
