package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
	"github.com/coder/websocket"
)

// Known fixtures
const (
	fixtureSessionKey = "agent-1::session-1"
	fixtureShotBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
)

func newEngineStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SetPreference(context.Background(), "services:llm", map[string]any{
		"base_url": "https://model.test/v1", "model": "test", "api_key": "test-key", "reasoning": "low",
	}, true); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestBrowserTaskRenewsSessionMarkers(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	var tags atomic.Int32
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		expression, _ := params["expression"].(string)
		if strings.Contains(expression, "window.name=") {
			tags.Add(1)
			return map[string]any{"result": map[string]any{"value": nil}}, nil
		}
		return nil, fmt.Errorf("use default evaluation")
	})
	manager := NewSessionManager(fake.server.URL, true)
	var atRun int32
	manager.engineRunner = func(_ context.Context, request engineRequest) (engineResult, error) {
		atRun = tags.Load()
		return engineResult{OK: true, Status: "done", TargetID: request.TargetID}, nil
	}
	_, err := manager.RunExternalBrowserTask(context.Background(), BrowserTaskRequest{
		URL: "https://example.com/test", Goal: "read", SessionKey: fixtureSessionKey,
	}, newEngineStore(t))
	if err != nil {
		t.Fatal(err)
	}
	if atRun == 0 || tags.Load() <= atRun {
		t.Fatal("the session marker was not renewed after the upstream task")
	}
}

// FakeCDPServer mocks HTTP discovery and WebSocket CDP target.
type FakeCDPServer struct {
	server       *httptest.Server
	pagesMu      sync.RWMutex
	pages        []PageTarget
	version      VersionInfo
	cdpHandlers  sync.Map // targetID -> func(method string, params map[string]any) (any, error)
	requestCalls atomic.Int64
}

func newFakeCDPServer(t *testing.T) *FakeCDPServer {
	fake := &FakeCDPServer{}
	mux := http.NewServeMux()

	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fake.version)
	})

	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		fake.pagesMu.RLock()
		defer fake.pagesMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fake.pages)
	})

	mux.HandleFunc("/json/close/", func(w http.ResponseWriter, r *http.Request) {
		targetID := strings.TrimPrefix(r.URL.Path, "/json/close/")
		fake.pagesMu.Lock()
		defer fake.pagesMu.Unlock()
		var kept []PageTarget
		for _, p := range fake.pages {
			if p.ID != targetID {
				kept = append(kept, p)
			}
		}
		fake.pages = kept
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Target is closing"))
	})

	mux.HandleFunc("/devtools/browser", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusInternalError, "server closed")

		for {
			_, msg, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var req cdpRequest
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}

			if req.Method == "Target.createTarget" {
				tid := fmt.Sprintf("target-%d", fake.requestCalls.Add(1))
				fake.pagesMu.Lock()
				p := PageTarget{
					ID:                   tid,
					Type:                 "page",
					Title:                "New Tab",
					URL:                  "about:blank",
					WebSocketDebuggerURL: strings.Replace(fake.server.URL, "http://", "ws://", 1) + "/devtools/page/" + tid,
				}
				fake.pages = append(fake.pages, p)
				fake.pagesMu.Unlock()

				resp := map[string]any{
					"id":     req.ID,
					"result": map[string]any{"targetId": tid},
				}
				data, _ := json.Marshal(resp)
				_ = c.Write(context.Background(), websocket.MessageText, data)
			} else if req.Method == "Target.activateTarget" {
				resp := map[string]any{"id": req.ID, "result": map[string]any{}}
				data, _ := json.Marshal(resp)
				_ = c.Write(context.Background(), websocket.MessageText, data)
			}
		}
	})

	mux.HandleFunc("/devtools/page/", func(w http.ResponseWriter, r *http.Request) {
		targetID := strings.TrimPrefix(r.URL.Path, "/devtools/page/")
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusInternalError, "server closed")

		for {
			_, msg, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var req cdpRequest
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}

			fake.requestCalls.Add(1)

			var res any
			var cdpErr *cdpError

			switch req.Method {
			case "Page.captureScreenshot":
				res = map[string]any{"data": fixtureShotBase64}
			case "Runtime.evaluate":
				expr, _ := req.Params["expression"].(string)
				if customHandler, ok := fake.cdpHandlers.Load(req.Method); ok {
					h := customHandler.(func(string, map[string]any) (any, error))
					r, err := h(req.Method, req.Params)
					if err == nil {
						res = r
						break
					}
				}
				if strings.Contains(expr, "window.name=") {
					res = map[string]any{"result": map[string]any{"value": nil}}
				} else if expr == "window.name" {
					res = map[string]any{"result": map[string]any{"value": SessionMarker(fixtureSessionKey)}}
				} else if strings.Contains(expr, "innerWidth") {
					res = map[string]any{"result": map[string]any{"value": map[string]any{"width": 1280, "height": 800}}}
				} else if strings.Contains(expr, "document.title") || strings.Contains(expr, "location.href") {
					res = map[string]any{"result": map[string]any{"value": map[string]any{
						"ready": "complete",
						"url":   "https://example.com/test",
						"title": "Example Domain",
						"text":  "Example text content",
					}}}
				} else if strings.Contains(expr, "window.__astrorderObserve") {
					res = map[string]any{"result": map[string]any{"value": []any{
						map[string]any{
							"id":    "1",
							"tag":   "BUTTON",
							"label": "Submit",
							"role":  "button",
							"rect": map[string]any{
								"x": 100.0, "y": 200.0, "w": 80.0, "h": 30.0,
							},
						},
					}}}
				} else {
					res = map[string]any{"result": map[string]any{"value": "ok"}}
				}
			case "Page.navigate":
				u, _ := req.Params["url"].(string)
				fake.pagesMu.Lock()
				for i := range fake.pages {
					if fake.pages[i].ID == targetID {
						fake.pages[i].URL = u
						fake.pages[i].Title = "Navigated Domain"
					}
				}
				fake.pagesMu.Unlock()
				res = map[string]any{"frameId": "1"}
			case "Input.dispatchMouseEvent", "Input.insertText", "Runtime.enable", "Log.enable":
				if customHandler, ok := fake.cdpHandlers.Load(req.Method); ok {
					h := customHandler.(func(string, map[string]any) (any, error))
					r, err := h(req.Method, req.Params)
					if err != nil {
						cdpErr = &cdpError{Message: err.Error()}
					} else {
						res = r
					}
				} else {
					res = map[string]any{}
				}
			default:
				if customHandler, ok := fake.cdpHandlers.Load(req.Method); ok {
					h := customHandler.(func(string, map[string]any) (any, error))
					r, err := h(req.Method, req.Params)
					if err != nil {
						cdpErr = &cdpError{Message: err.Error()}
					} else {
						res = r
					}
				} else {
					res = map[string]any{"result": "echo"}
				}
			}

			resp := map[string]any{"id": req.ID}
			if cdpErr != nil {
				resp["error"] = cdpErr
			} else {
				resp["result"] = res
			}
			data, _ := json.Marshal(resp)
			_ = c.Write(context.Background(), websocket.MessageText, data)
		}
	})

	server := httptest.NewServer(mux)
	fake.server = server
	fake.version = VersionInfo{
		Browser:              "Chrome/124.0.0.0",
		WebSocketDebuggerURL: strings.Replace(server.URL, "http://", "ws://", 1) + "/devtools/browser",
	}

	return fake
}

func testAuthenticator() *auth.Authenticator {
	return auth.New(config.Config{
		Listen:        "127.0.0.1:30002",
		BrowserSecret: "browser-secret",
	}, nil)
}

type fakePublisher struct {
	events []map[string]any
	mu     sync.Mutex
}

func (p *fakePublisher) Publish(ctx context.Context, name string, data map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, map[string]any{"name": name, "data": data})
	return nil
}

func TestBrowserRegistrationContract(t *testing.T) {
	registrations := Registrations()
	if len(registrations) != 11 {
		t.Fatalf("expected 11 browser registrations, got %d", len(registrations))
	}

	foundScreenshot := false
	foundNavigate := false
	foundProxy := false

	for _, r := range registrations {
		if r.Kind != "http" || r.Auth != "browser" || r.Owner != "go-app" {
			t.Errorf("registration invalid: %#v", r)
		}
		if r.Path == "/api/v1/browser/screenshot" && r.Method == http.MethodGet {
			foundScreenshot = true
		}
		if r.Path == "/api/v1/browser/navigate" && r.Method == http.MethodPost {
			foundNavigate = true
		}
		if r.Path == "/api/v1/browser/proxy" && r.Method == http.MethodPost {
			foundProxy = true
		}
	}

	if !foundScreenshot || !foundNavigate || !foundProxy {
		t.Errorf("missing core browser registrations")
	}
}

func TestMatchesRoute(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/api/v1/browser/screenshot", true},
		{http.MethodPost, "/api/v1/browser/screenshot", false},
		{http.MethodPost, "/api/v1/browser/navigate", true},
		{http.MethodGet, "/api/v1/browser/navigate", false},
		{http.MethodGet, "/api/v1/browser/proxy", true},
		{http.MethodPost, "/api/v1/browser/proxy", true},
		{http.MethodPost, "/api/v1/system/open-browser", true},
		{http.MethodGet, "/api/v1/system/open-browser", false},
		{http.MethodGet, "/api/v1/other", false},
	}

	for _, c := range cases {
		if got := Matches(c.method, c.path); got != c.want {
			t.Errorf("Matches(%s, %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestSessionTabIsolationAndWindowNameRecovery(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()

	// 1. Create tab for session A
	pageA, created, err := mgr.SessionPage(ctx, "agent::session-a", false, true, "")
	if err != nil || !created {
		t.Fatalf("SessionPage A failed: %v", err)
	}

	// 2. Create tab for session B
	pageB, created, err := mgr.SessionPage(ctx, "agent::session-b", false, true, "")
	if err != nil || !created {
		t.Fatalf("SessionPage B failed: %v", err)
	}

	if pageA.ID == pageB.ID {
		t.Fatalf("Tabs for session A and B must be isolated: got same id %s", pageA.ID)
	}

	// 3. Recover session targets
	tabsA := mgr.ListTabs(ctx, "agent::session-a")
	if len(tabsA.Tabs) != 1 || tabsA.Tabs[0].ID != pageA.ID {
		t.Fatalf("session A tabs mismatch: %#v", tabsA)
	}

	tabsB := mgr.ListTabs(ctx, "agent::session-b")
	if len(tabsB.Tabs) != 1 || tabsB.Tabs[0].ID != pageB.ID {
		t.Fatalf("session B tabs mismatch: %#v", tabsB)
	}
}

func TestNavigateScreenshotKnownFixture(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()

	snap, err := mgr.Navigate(ctx, fixtureSessionKey, "https://example.com/test", "")
	if err != nil {
		t.Fatalf("Navigate failed: %v", err)
	}

	if snap.Screenshot != fixtureShotBase64 {
		t.Fatalf("Screenshot data mismatch: got %s, want %s", snap.Screenshot, fixtureShotBase64)
	}
	if snap.MimeType != "image/jpeg" {
		t.Fatalf("MimeType mismatch: %s", snap.MimeType)
	}
	if snap.SessionKey != fixtureSessionKey {
		t.Fatalf("SessionKey mismatch: %s", snap.SessionKey)
	}
	if snap.Title != "Example Domain" {
		t.Fatalf("Title mismatch: %s", snap.Title)
	}
}

func TestMultiTabOperations(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()
	sKey := "agent::multi-tab"

	// 1. NewTab
	shot1, err := mgr.NewTab(ctx, sKey, "")
	if err != nil {
		t.Fatalf("NewTab 1 failed: %v", err)
	}
	tid1 := shot1.ActiveTargetID
	if len(shot1.Tabs) != 1 {
		t.Fatalf("expected 1 tab, got %d", len(shot1.Tabs))
	}

	// 2. New second tab
	shot2, err := mgr.NewTab(ctx, sKey, "")
	if err != nil {
		t.Fatalf("NewTab 2 failed: %v", err)
	}
	tid2 := shot2.ActiveTargetID
	if len(shot2.Tabs) != 2 || tid2 == tid1 {
		t.Fatalf("expected 2 tabs and new active target, got %#v", shot2)
	}

	// 3. Select tab 1
	shot3, err := mgr.SelectTab(ctx, sKey, tid1)
	if err != nil {
		t.Fatalf("SelectTab failed: %v", err)
	}
	if shot3.ActiveTargetID != tid1 {
		t.Fatalf("active target ID mismatch: %s != %s", shot3.ActiveTargetID, tid1)
	}

	// 4. Close tab 1
	shot4, err := mgr.CloseTab(ctx, sKey, tid1)
	if err != nil {
		t.Fatalf("CloseTab 1 failed: %v", err)
	}
	if shot4.ActiveTargetID != tid2 || len(shot4.Tabs) != 1 {
		t.Fatalf("expected fallback to tab 2: %#v", shot4)
	}

	// 5. Close tab 2
	shot5, err := mgr.CloseTab(ctx, sKey, tid2)
	if err != nil {
		t.Fatalf("CloseTab 2 failed: %v", err)
	}
	if shot5.ActiveTargetID != "" || len(shot5.Tabs) != 0 {
		t.Fatalf("expected empty tabs: %#v", shot5)
	}
}

func TestCDPDomainRestrictionsAndErrors(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()
	sKey := "agent::cdp-test"

	// Ensure tab exists
	_, err := mgr.NewTab(ctx, sKey, "")
	if err != nil {
		t.Fatal(err)
	}

	// Allowed domain
	res, err := mgr.CallCDP(ctx, sKey, "Runtime.evaluate", map[string]any{"expression": "1+1"}, "")
	if err != nil {
		t.Fatalf("Runtime.evaluate should be allowed: %v", err)
	}
	if res["method"] != "Runtime.evaluate" {
		t.Fatalf("method mismatch: %#v", res)
	}

	// Blocked domain: Target.getTargets
	_, err = mgr.CallCDP(ctx, sKey, "Target.getTargets", nil, "")
	if err == nil || !strings.Contains(err.Error(), "不允许调用 CDP 方法") {
		t.Fatalf("Target domain must be blocked: got %v", err)
	}

	// Top level error visible
	fake.cdpHandlers.Store("Page.crash", func(method string, params map[string]any) (any, error) {
		return nil, fmt.Errorf("synthetic crash simulation")
	})
	_, err = mgr.CallCDP(ctx, sKey, "Page.crash", nil, "")
	if err == nil || !strings.Contains(err.Error(), "synthetic crash simulation") {
		t.Fatalf("expected top-level error: got %v", err)
	}
}

func TestInteractAndPageContent(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	mgr := NewSessionManager(fake.server.URL, true)
	ctx := context.Background()
	sKey := "agent::interact"

	rx, ry := 0.5, 0.5
	shot, err := mgr.Interact(ctx, sKey, "click", "", nil, nil, &rx, &ry, nil, nil)
	if err != nil {
		t.Fatalf("Interact click failed: %v", err)
	}
	if shot.Screenshot != fixtureShotBase64 {
		t.Fatalf("shot mismatch: %s", shot.Screenshot)
	}

	content, err := mgr.ExtractPageContent(ctx, sKey, "")
	if err != nil {
		t.Fatalf("ExtractPageContent failed: %v", err)
	}
	if content.Title != "Example Domain" || !strings.Contains(content.Text, "Example text content") {
		t.Fatalf("page content mismatch: %#v", content)
	}
}

func TestHTTPHandlerEndpointsAndAuth(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	pub := &fakePublisher{}
	handler, err := NewHandler(Options{
		Authorizer: testAuthenticator(),
		Publisher:  pub,
		CDPURL:     fake.server.URL,
		NoAutoExec: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Unauthenticated request -> 401
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser/screenshot?agent_id=a&session_id=s", nil)
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthRec.Code)
	}

	// 2. Navigate via HTTP
	body := strings.NewReader(`{"agent_id":"agent-1","session_id":"session-1","url":"https://example.com/test"}`)
	navReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser/navigate", body)
	navReq.Header.Set("Authorization", "Bearer browser-secret")
	navRec := httptest.NewRecorder()
	handler.ServeHTTP(navRec, navReq)
	if navRec.Code != http.StatusOK {
		t.Fatalf("navigate status %d: %s", navRec.Code, navRec.Body.String())
	}

	if len(pub.events) == 0 || pub.events[0]["name"] != "browser.mirror.updated" {
		t.Fatalf("mirror updated event was not published: %#v", pub.events)
	}

	// 3. Screenshot via HTTP
	shotReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser/screenshot?agent_id=agent-1&session_id=session-1", nil)
	shotReq.Header.Set("Authorization", "Bearer browser-secret")
	shotRec := httptest.NewRecorder()
	handler.ServeHTTP(shotRec, shotReq)
	if shotRec.Code != http.StatusOK {
		t.Fatalf("screenshot status %d: %s", shotRec.Code, shotRec.Body.String())
	}

	// 4. Page content via HTTP
	contReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser/page-content?agent_id=agent-1&session_id=session-1", nil)
	contReq.Header.Set("Authorization", "Bearer browser-secret")
	contRec := httptest.NewRecorder()
	handler.ServeHTTP(contRec, contReq)
	if contRec.Code != http.StatusOK {
		t.Fatalf("page-content status %d: %s", contRec.Code, contRec.Body.String())
	}
}

func TestAgentAPIInvokeSupport(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()

	pub := &fakePublisher{}
	rawHandler, err := NewHandler(Options{
		Authorizer: testAuthenticator(),
		Publisher:  pub,
		CDPURL:     fake.server.URL,
		NoAutoExec: true,
		Store:      newEngineStore(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := rawHandler.(*Handler)
	h.manager.engineRunner = func(_ context.Context, request engineRequest) (engineResult, error) {
		return engineResult{OK: true, Status: "done", TargetID: request.TargetID}, nil
	}
	ctx := context.Background()

	// Ensure tab exists first (as browser.cdp has create=false semantics)
	if _, err := h.manager.NewTab(ctx, fixtureSessionKey, ""); err != nil {
		t.Fatalf("setup tab failed: %v", err)
	}

	// browser.cdp invoke
	cdpRes, err := h.Invoke(ctx, "browser.cdp", map[string]any{
		"session_key": fixtureSessionKey,
		"method":      "Runtime.evaluate",
		"params":      map[string]any{"expression": "document.title"},
	})
	if err != nil {
		t.Fatalf("Invoke browser.cdp failed: %v", err)
	}
	m, ok := cdpRes.(map[string]any)
	if !ok || m["method"] != "Runtime.evaluate" {
		t.Fatalf("Invoke result mismatch: %#v", cdpRes)
	}

	// browser.screenshot invoke
	shotRes, err := h.Invoke(ctx, "browser.screenshot", map[string]any{
		"session_key": fixtureSessionKey,
	})
	if err != nil {
		t.Fatalf("Invoke browser.screenshot failed: %v", err)
	}
	snap, ok := shotRes.(*Snapshot)
	if !ok || snap.Screenshot != fixtureShotBase64 {
		t.Fatalf("Invoke screenshot mismatch: %#v", shotRes)
	}

	// browser.run invoke with injected mock Jev key in env for test
	t.Setenv("JEV_API_KEY", "mock-jev-key")
	taskRes, err := h.Invoke(ctx, "browser.run", map[string]any{
		"session_key":        fixtureSessionKey,
		"url":                "https://example.com/test",
		"goal":               "Open test page",
		"include_screenshot": true,
	})
	if err != nil {
		t.Fatalf("Invoke browser.run failed: %v", err)
	}
	task, ok := taskRes.(*BrowserTaskResult)
	if !ok || !task.OK || task.Screenshot != fixtureShotBase64 {
		t.Fatalf("Invoke browser.run mismatch: %#v", taskRes)
	}
}

func TestExtensionProviderWebSocket(t *testing.T) {
	provider := NewExtensionProvider()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = provider.HandleExtensionWebSocket(w, r)
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + "?session_id=sess-123"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial extension ws failed: %v", err)
	}
	defer conn.CloseNow()

	// Send message
	msg := ExtensionMessage{
		ID:     "msg-1",
		Action: "ping",
	}
	data, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, respBytes, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var resp ExtensionMessage
	if err := json.Unmarshal(respBytes, &resp); err != nil || resp.ID != "msg-1" {
		t.Fatalf("extension response mismatch: %s", string(respBytes))
	}
}

func TestProxyBrowserStripsHeadersAndInjectsBase(t *testing.T) {
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title>Test</title></head><body>Hello</body></html>"))
	}))
	defer targetServer.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser/proxy?url="+targetServer.URL, nil)
	rec := httptest.NewRecorder()

	ProxyBrowser(rec, req, targetServer.URL)

	if rec.Code != http.StatusOK {
		t.Fatalf("proxy code %d", rec.Code)
	}
	if rec.Header().Get("X-Frame-Options") != "" {
		t.Fatalf("X-Frame-Options was not stripped")
	}
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("Content-Security-Policy was not stripped")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<base href=") {
		t.Fatalf("<base href> was not injected into HTML: %s", body)
	}
}
