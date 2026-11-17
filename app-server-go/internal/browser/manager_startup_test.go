package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowserOperationsStartBrowserAndRecoverAfterClosure(t *testing.T) {
	operations := map[string]func(context.Context, *SessionManager) (string, error){
		"cdp": func(ctx context.Context, manager *SessionManager) (string, error) {
			result, err := manager.CallCDP(ctx, fixtureSessionKey, "Runtime.evaluate", map[string]any{"expression": "1+1"}, "")
			if err != nil {
				return "", err
			}
			return result["target_id"].(string), nil
		},
		"screenshot": func(ctx context.Context, manager *SessionManager) (string, error) {
			handler := &Handler{manager: manager}
			result, err := handler.Invoke(ctx, "browser.screenshot", map[string]any{"session_key": fixtureSessionKey})
			if err != nil {
				return "", err
			}
			snapshot := result.(*Snapshot)
			if snapshot.Screenshot != fixtureShotBase64 {
				t.Errorf("screenshot did not complete after startup: %+v", snapshot)
			}
			return snapshot.TargetID, nil
		},
		"content": func(ctx context.Context, manager *SessionManager) (string, error) {
			content, err := manager.ExtractPageContent(ctx, fixtureSessionKey, "")
			if err != nil {
				return "", err
			}
			if content.Text != "Example text content" {
				t.Errorf("content extraction did not complete after startup: %+v", content)
			}
			return content.TargetID, nil
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			fake := newFakeCDPServer(t)
			defer fake.server.Close()
			fake.available.Store(false)
			manager := NewSessionManager(fake.server.URL, false)
			endpoint, err := url.Parse(fake.server.URL)
			if err != nil {
				t.Fatal(err)
			}
			expectedPort, err := strconv.Atoi(endpoint.Port())
			if err != nil {
				t.Fatal(err)
			}
			var starts int
			manager.launchBrowser = func(port int) error {
				starts++
				if port != expectedPort {
					t.Errorf("started browser on port %d, want %d", port, expectedPort)
				}
				fake.available.Store(true)
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			firstTarget, err := operation(ctx, manager)
			if err != nil || firstTarget == "" {
				t.Fatalf("first operation failed: target=%q error=%v", firstTarget, err)
			}
			reusedTarget, err := operation(ctx, manager)
			if err != nil || reusedTarget != firstTarget || starts != 1 {
				t.Fatalf("running browser was not reused: target=%q starts=%d error=%v", reusedTarget, starts, err)
			}

			// Simulate closing the browser, including the loss of its old tabs.
			fake.available.Store(false)
			fake.pagesMu.Lock()
			fake.pages = []PageTarget{}
			fake.pagesMu.Unlock()
			newTarget, err := operation(ctx, manager)
			if err != nil || newTarget == "" || newTarget == firstTarget || starts != 2 {
				t.Fatalf("operation after closure failed: target=%q starts=%d error=%v", newTarget, starts, err)
			}
			manager.targetMu.Lock()
			targets := append([]string(nil), manager.sessionTargets[fixtureSessionKey]...)
			manager.targetMu.Unlock()
			if len(targets) != 1 || targets[0] != newTarget {
				t.Fatalf("stale tab ownership after restarting: %v", targets)
			}
		})
	}
}

func TestConcurrentBrowserOperationsLaunchOnce(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	fake.available.Store(false)
	manager := NewSessionManager(fake.server.URL, false)
	var starts atomic.Int32
	launching := make(chan struct{})
	manager.launchBrowser = func(int) error {
		if starts.Add(1) == 1 {
			close(launching)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const count = 8
	results := make(chan error, count)
	var workers sync.WaitGroup
	for i := range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := manager.CallCDP(ctx, fmt.Sprintf("agent::session-%d", i), "Runtime.evaluate", map[string]any{"expression": "1+1"}, "")
			results <- err
		}()
	}
	select {
	case <-launching:
	case <-ctx.Done():
		t.Fatal("browser startup was not requested")
	}
	fake.available.Store(true)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent browser operation failed: %v", err)
		}
	}
	if starts.Load() != 1 {
		t.Fatalf("started %d browser processes, want one", starts.Load())
	}
	manager.targetMu.Lock()
	defer manager.targetMu.Unlock()
	owners := make(map[string]string)
	for session, targets := range manager.sessionTargets {
		if len(targets) != 1 {
			t.Errorf("session %s owns %v, want one tab", session, targets)
		}
		for _, target := range targets {
			if owner := owners[target]; owner != "" {
				t.Errorf("tab %s shared between %s and %s", target, owner, session)
			}
			owners[target] = session
		}
	}
	if len(owners) != count {
		t.Errorf("got %d owned tabs, want %d", len(owners), count)
	}
}

func TestBrowserOperationStartupErrors(t *testing.T) {
	for _, scenario := range []string{"launch failure", "canceled startup", "automatic startup disabled"} {
		t.Run(scenario, func(t *testing.T) {
			fake := newFakeCDPServer(t)
			defer fake.server.Close()
			fake.available.Store(false)
			manager := NewSessionManager(fake.server.URL, scenario == "automatic startup disabled")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			launchErr := errors.New("browser executable missing")
			starts := 0
			manager.launchBrowser = func(int) error {
				starts++
				if scenario == "canceled startup" {
					cancel()
					return nil
				}
				return launchErr
			}
			_, err := manager.CallCDP(ctx, fixtureSessionKey, "Runtime.evaluate", nil, "")
			switch scenario {
			case "launch failure":
				if !errors.Is(err, launchErr) || !strings.Contains(err.Error(), "启动浏览器失败") || starts != 1 {
					t.Fatalf("unexpected startup failure: starts=%d error=%v", starts, err)
				}
			case "canceled startup":
				if !errors.Is(err, context.Canceled) || starts != 1 {
					t.Fatalf("startup did not respect cancellation: starts=%d error=%v", starts, err)
				}
			case "automatic startup disabled":
				if err == nil || !strings.Contains(err.Error(), "未允许自动启动") || starts != 0 {
					t.Fatalf("disabled startup was not respected: starts=%d error=%v", starts, err)
				}
			}
		})
	}
}

func TestPassiveBrowserOperationsDoNotLaunchBrowser(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	fake.available.Store(false)
	manager := NewSessionManager(fake.server.URL, false)
	starts := 0
	manager.launchBrowser = func(int) error {
		starts++
		return errors.New("unexpected browser startup")
	}
	ctx := context.Background()
	if _, err := manager.CaptureScreenshot(ctx, fixtureSessionKey, true, ""); err == nil {
		t.Error("mirror refresh should report the unavailable browser")
	}
	if _, err := manager.ListTabs(ctx, fixtureSessionKey); err == nil {
		t.Error("tab listing should report the unavailable browser")
	}
	if err := manager.CloseSession(ctx, fixtureSessionKey); err == nil {
		t.Error("session cleanup should report the unavailable browser")
	}
	if _, err := manager.CallCDP(ctx, fixtureSessionKey, "Emulation.setDeviceMetricsOverride", nil, ""); err == nil ||
		!strings.Contains(err.Error(), "不允许调用 CDP 方法") {
		t.Errorf("restricted CDP method was allowed: %v", err)
	}
	if starts != 0 {
		t.Errorf("passive or rejected operations started the browser %d times", starts)
	}
}
