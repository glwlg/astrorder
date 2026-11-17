package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionPagePreservesTargetsWhenDiscoveryIsCanceled(t *testing.T) {
	manager := NewSessionManager("http://127.0.0.1:1", true)
	manager.sessionTargets[fixtureSessionKey] = []string{"existing-target"}
	manager.sessionActive[fixtureSessionKey] = "existing-target"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := manager.SessionPage(ctx, fixtureSessionKey, false, false, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled discovery, got %v", err)
	}
	if !ownsTarget(manager.sessionTargets[fixtureSessionKey], "existing-target") ||
		manager.sessionActive[fixtureSessionKey] != "existing-target" {
		t.Fatal("canceled discovery changed existing tab ownership")
	}
}

func TestSlowRecoveryDoesNotBlockAnotherSession(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	base := strings.Replace(fake.server.URL, "http://", "ws://", 1)
	fake.pagesMu.Lock()
	fake.pages = []PageTarget{
		{ID: "healthy", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/healthy"},
		{ID: "slow", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/slow"},
	}
	fake.pagesMu.Unlock()

	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		if params["expression"] != "window.name" {
			return nil, fmt.Errorf("use default evaluation")
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": SessionMarker("slow::session")}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	manager.sessionTargets[fixtureSessionKey] = []string{"healthy"}
	manager.sessionActive[fixtureSessionKey] = "healthy"
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _, _ = manager.SessionPage(firstCtx, "slow::session", false, false, "")
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("slow recovery did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	type result struct {
		page *PageTarget
		err  error
	}
	secondDone := make(chan result, 1)
	go func() {
		page, _, err := manager.SessionPage(ctx, fixtureSessionKey, false, false, "")
		secondDone <- result{page, err}
	}()
	select {
	case got := <-secondDone:
		if got.err != nil || got.page == nil || got.page.ID != "healthy" {
			t.Fatalf("healthy session failed while another recovered: page=%v error=%v", got.page, got.err)
		}
	case <-time.After(600 * time.Millisecond):
		t.Fatal("another session's recovery blocked the healthy session")
	}
	release.Do(func() { close(gate) })
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("slow recovery did not finish")
	}
}

func TestCanceledRecoveryStopsWithoutChangingOtherSessions(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	base := strings.Replace(fake.server.URL, "http://", "ws://", 1)
	fake.pagesMu.Lock()
	fake.pages = []PageTarget{{ID: "slow", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/slow"}}
	fake.pagesMu.Unlock()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		if params["expression"] != "window.name" {
			return nil, fmt.Errorf("use default evaluation")
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": SessionMarker(fixtureSessionKey)}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	manager.sessionTargets["other::session"] = []string{"other-target"}
	manager.sessionActive["other::session"] = "other-target"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := manager.SessionPage(ctx, fixtureSessionKey, false, false, "")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("recovery did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled recovery, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled recovery did not stop promptly")
	}
	if !ownsTarget(manager.sessionTargets["other::session"], "other-target") {
		t.Fatal("recovery changed another session's targets")
	}
	release.Do(func() { close(gate) })
}

func TestUnresponsiveMarkerProbeTimesOut(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	base := strings.Replace(fake.server.URL, "http://", "ws://", 1)
	fake.pagesMu.Lock()
	fake.pages = []PageTarget{{ID: "slow", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/slow"}}
	fake.pagesMu.Unlock()
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		if params["expression"] != "window.name" {
			return nil, fmt.Errorf("use default evaluation")
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": SessionMarker(fixtureSessionKey)}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := manager.SessionPage(ctx, fixtureSessionKey, false, false, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected marker probe timeout, got %v", err)
	}
	if time.Since(start) >= 3500*time.Millisecond {
		t.Fatal("unresponsive marker probe was not bounded")
	}
	release.Do(func() { close(gate) })
}

func TestSessionPageWaitingForSameSessionCanBeCanceled(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		expression, _ := params["expression"].(string)
		if !strings.Contains(expression, "window.name=") {
			return nil, fmt.Errorf("use default evaluation")
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": nil}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _, _ = manager.SessionPage(context.Background(), fixtureSessionKey, false, true, "")
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first page creation did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	secondDone := make(chan error, 1)
	go func() {
		_, _, err := manager.SessionPage(ctx, fixtureSessionKey, false, true, "")
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
	case <-time.After(600 * time.Millisecond):
		t.Fatal("same-session wait ignored its deadline")
	}
	release.Do(func() { close(gate) })
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first page creation did not finish")
	}
}

func TestSlowPageCreationDoesNotBlockAnotherSession(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	base := strings.Replace(fake.server.URL, "http://", "ws://", 1)
	fake.pagesMu.Lock()
	fake.pages = []PageTarget{{ID: "healthy", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/healthy"}}
	fake.pagesMu.Unlock()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		expression, _ := params["expression"].(string)
		if !strings.Contains(expression, "window.name=") {
			return nil, fmt.Errorf("use default evaluation")
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": nil}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	manager.sessionTargets[fixtureSessionKey] = []string{"healthy"}
	manager.sessionActive[fixtureSessionKey] = "healthy"
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _, _ = manager.SessionPage(context.Background(), "new::session", false, true, "")
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("slow page creation did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	page, _, err := manager.SessionPage(ctx, fixtureSessionKey, false, false, "")
	if err != nil || page == nil || page.ID != "healthy" {
		t.Fatalf("healthy session was blocked by page creation: page=%v error=%v", page, err)
	}
	release.Do(func() { close(gate) })
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("slow page creation did not finish")
	}
}

func TestConcurrentSessionPageCreationUsesOneTab(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		expression, _ := params["expression"].(string)
		if !strings.Contains(expression, "window.name=") {
			return nil, fmt.Errorf("use default evaluation")
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": nil}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	type result struct {
		page    *PageTarget
		created bool
		err     error
	}
	results := make(chan result, 2)
	call := func() {
		page, created, err := manager.SessionPage(context.Background(), fixtureSessionKey, false, true, "")
		results <- result{page, created, err}
	}
	go call()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first page creation did not start")
	}
	go call()
	release.Do(func() { close(gate) })
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.page == nil || second.page == nil {
		t.Fatalf("concurrent creation failed: first=%+v second=%+v", first, second)
	}
	if first.page.ID != second.page.ID || first.created == second.created {
		t.Fatalf("concurrent calls did not share one tab: first=%+v second=%+v", first, second)
	}
	pages, err := fakePages(fake, context.Background())
	if err != nil || len(pages) != 1 {
		t.Fatalf("expected one browser tab, got %d: %v", len(pages), err)
	}
}

func TestRecoveryRestoresMultipleTabsInBrowserOrder(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	base := strings.Replace(fake.server.URL, "http://", "ws://", 1)
	fake.pagesMu.Lock()
	fake.pages = []PageTarget{
		{ID: "first", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/first"},
		{ID: "second", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/second"},
	}
	fake.pagesMu.Unlock()
	manager := NewSessionManager(fake.server.URL, true)
	page, _, err := manager.SessionPage(context.Background(), fixtureSessionKey, false, false, "")
	if err != nil || page == nil || page.ID != "first" {
		t.Fatalf("first recovered tab is wrong: page=%v error=%v", page, err)
	}
	tabs, err := manager.ListTabs(context.Background(), fixtureSessionKey)
	if err != nil || len(tabs.Tabs) != 2 || tabs.Tabs[0].ID != "first" || tabs.Tabs[1].ID != "second" {
		t.Fatalf("recovered tab order is wrong: tabs=%+v error=%v", tabs, err)
	}
}

func TestBrowserOperationsPreserveOwnershipOnDiscoveryFailure(t *testing.T) {
	var closes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = w.Write([]byte(`{"Browser":"fixture","webSocketDebuggerUrl":"ws://fixture"}`))
		case "/json/list":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		default:
			closes.Add(1)
			http.Error(w, "unexpected operation", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	tests := map[string]func(*SessionManager) error{
		"page": func(m *SessionManager) error {
			_, _, err := m.SessionPage(context.Background(), fixtureSessionKey, false, false, "")
			return err
		},
		"list": func(m *SessionManager) error {
			_, err := m.ListTabs(context.Background(), fixtureSessionKey)
			return err
		},
		"select": func(m *SessionManager) error {
			_, err := m.SelectTab(context.Background(), fixtureSessionKey, "existing-target")
			return err
		},
		"new": func(m *SessionManager) error {
			_, err := m.NewTab(context.Background(), fixtureSessionKey, "")
			return err
		},
		"close": func(m *SessionManager) error {
			_, err := m.CloseTab(context.Background(), fixtureSessionKey, "existing-target")
			return err
		},
		"close session": func(m *SessionManager) error {
			return m.CloseSession(context.Background(), fixtureSessionKey)
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			manager := NewSessionManager(server.URL, true)
			manager.sessionTargets[fixtureSessionKey] = []string{"existing-target"}
			manager.sessionActive[fixtureSessionKey] = "existing-target"
			if err := run(manager); err == nil {
				t.Fatal("expected discovery error")
			}
			if !ownsTarget(manager.sessionTargets[fixtureSessionKey], "existing-target") ||
				manager.sessionActive[fixtureSessionKey] != "existing-target" {
				t.Fatal("failed discovery changed tab ownership")
			}
		})
	}
	if closes.Load() != 0 {
		t.Fatalf("failed discovery attempted %d unexpected browser operations", closes.Load())
	}
}

func TestBrowserStartupRechecksBeforeLaunching(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if requests.Add(1) == 1 {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"Browser":"fixture","webSocketDebuggerUrl":"ws://fixture"}`))
	}))
	defer server.Close()
	manager := NewSessionManager(server.URL, false)
	if err := manager.EnsureBrowser(context.Background()); err != nil {
		t.Fatalf("browser that became available was not reused: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected two browser availability checks, got %d", requests.Load())
	}
}

func TestInvalidTargetListDoesNotClearOwnership(t *testing.T) {
	for _, body := range []string{`null`, `[{"type":"page"}]`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			manager := NewSessionManager(server.URL, true)
			manager.sessionTargets[fixtureSessionKey] = []string{"existing-target"}
			manager.sessionActive[fixtureSessionKey] = "existing-target"
			_, _, err := manager.SessionPage(context.Background(), fixtureSessionKey, false, false, "")
			if err == nil {
				t.Fatal("invalid target list was accepted")
			}
			if !ownsTarget(manager.sessionTargets[fixtureSessionKey], "existing-target") ||
				manager.sessionActive[fixtureSessionKey] != "existing-target" {
				t.Fatal("invalid target list changed tab ownership")
			}
		})
	}
}

func TestUninspectableTargetDoesNotLookLikeAnEmptySession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"unavailable","type":"page"}]`))
	}))
	defer server.Close()
	manager := NewSessionManager(server.URL, true)
	_, _, err := manager.SessionPage(context.Background(), fixtureSessionKey, false, false, "")
	if err == nil || !strings.Contains(err.Error(), "缺少调试连接") {
		t.Fatalf("expected an inspection error, got %v", err)
	}
}

func TestNavigateWaitingForSessionCanBeCanceled(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		expression, _ := params["expression"].(string)
		if !strings.Contains(expression, "window.name=") {
			return nil, fmt.Errorf("use default evaluation")
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": nil}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.NewTab(context.Background(), fixtureSessionKey, "")
		firstDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first session operation did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := manager.Navigate(ctx, fixtureSessionKey, "https://example.com", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected canceled session wait, got %v", err)
	}
	release.Do(func() { close(gate) })
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first operation failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first operation did not finish")
	}
}

func TestCanceledPageBindingClosesOnlyTheNewTab(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	base := strings.Replace(fake.server.URL, "http://", "ws://", 1)
	fake.pagesMu.Lock()
	fake.pages = []PageTarget{{ID: "foreign", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/foreign"}}
	fake.pagesMu.Unlock()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		expression, _ := params["expression"].(string)
		if expression == "window.name" {
			return map[string]any{"result": map[string]any{"value": SessionMarker("other::session")}}, nil
		}
		if !strings.Contains(expression, "window.name=") {
			return nil, fmt.Errorf("use default evaluation")
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": nil}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := manager.SessionPage(ctx, fixtureSessionKey, false, true, "")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("new page binding did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled binding, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled binding did not stop promptly")
	}
	pages, err := fakePages(fake, context.Background())
	if err != nil || len(pages) != 1 || pages[0].ID != "foreign" {
		t.Fatalf("new page cleanup changed other tabs: pages=%v error=%v", pages, err)
	}
	release.Do(func() { close(gate) })
}

func TestRecoveryDoesNotStealConcurrentlyOwnedTarget(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	base := strings.Replace(fake.server.URL, "http://", "ws://", 1)
	fake.pagesMu.Lock()
	fake.pages = []PageTarget{{ID: "shared", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/shared"}}
	fake.pagesMu.Unlock()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		expression, _ := params["expression"].(string)
		if expression != "window.name" {
			return map[string]any{"result": map[string]any{"value": nil}}, nil
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": SessionMarker("recovering::session")}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	done := make(chan error, 1)
	go func() {
		_, _, err := manager.SessionPage(context.Background(), "recovering::session", false, false, "")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("target recovery did not start")
	}
	manager.rememberEngineTarget(context.Background(), "owner::session", "shared")
	release.Do(func() { close(gate) })
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("recovery accepted another session's target")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target recovery did not finish")
	}
	if !ownsTarget(manager.sessionTargets["owner::session"], "shared") ||
		ownsTarget(manager.sessionTargets["recovering::session"], "shared") {
		t.Fatal("target ownership was stolen during concurrent recovery")
	}
}
