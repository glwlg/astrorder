package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type extensionFixture struct {
	enabled, installed bool
	loads              int
}

type developerModeFixture struct {
	cleanup chan error
}

func (f *developerModeFixture) Call(ctx context.Context, method string, _ map[string]any) (json.RawMessage, error) {
	switch method {
	case "Target.createTarget":
		return json.RawMessage(`{"targetId":"settings"}`), nil
	case "Target.closeTarget":
		f.cleanup <- ctx.Err()
		return json.RawMessage(`{}`), nil
	default:
		return nil, fmt.Errorf("unexpected CDP method: %s", method)
	}
}

func (f *extensionFixture) Call(_ context.Context, method string, _ map[string]any) (json.RawMessage, error) {
	switch method {
	case "Extensions.getExtensions":
		items := []map[string]any{}
		if f.installed {
			items = append(items, map[string]any{"id": "session-extension", "path": ExtensionPath(), "enabled": f.enabled})
		}
		return json.Marshal(map[string]any{"extensions": items})
	case "Extensions.loadUnpacked":
		f.installed = true
		f.loads++
		return json.RawMessage(`{"id":"session-extension"}`), nil
	}
	return nil, errors.New("unexpected CDP call")
}
func TestSessionExtensionEnablesDisabledInstallation(t *testing.T) {
	f := &extensionFixture{installed: true}
	calls := 0
	err := ensureSessionExtension(context.Background(), f, func(context.Context) error { calls++; f.enabled = true; return nil })
	if err != nil || calls != 1 || f.loads != 0 {
		t.Fatalf("err=%v enable=%d loads=%d", err, calls, f.loads)
	}
}
func TestSessionExtensionLoadsMissingInstallation(t *testing.T) {
	f := &extensionFixture{}
	err := ensureSessionExtension(context.Background(), f, func(context.Context) error { f.enabled = true; return nil })
	if err != nil || f.loads != 1 {
		t.Fatalf("err=%v loads=%d", err, f.loads)
	}
}
func TestSessionExtensionRejectsDisabledReadback(t *testing.T) {
	f := &extensionFixture{installed: true}
	if err := ensureSessionExtension(context.Background(), f, func(context.Context) error { return nil }); err == nil {
		t.Fatal("disabled extension reported ready")
	}
}

func TestConcurrentExtensionSetupCanBeCanceled(t *testing.T) {
	manager := NewSessionManager("", true)
	fixture := &extensionFixture{}
	entered := make(chan struct{})
	gate := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- manager.ensureSessionExtension(context.Background(), fixture, func(context.Context) error {
			close(entered)
			<-gate
			fixture.enabled = true
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("extension setup did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := manager.ensureSessionExtension(ctx, fixture, func(context.Context) error {
		t.Error("second setup entered while the first setup was active")
		return nil
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a canceled setup wait, got %v", err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("first setup failed: %v", err)
	}
	if err := manager.ensureSessionExtension(context.Background(), fixture, func(context.Context) error {
		t.Error("installed extension was initialized again")
		return nil
	}); err != nil || fixture.loads != 1 {
		t.Fatalf("extension setup repeated: err=%v loads=%d", err, fixture.loads)
	}
}

func TestCanceledExtensionSetupClosesItsSettingsTab(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	base := strings.Replace(fake.server.URL, "http://", "ws://", 1)
	fake.pagesMu.Lock()
	fake.pages = []PageTarget{{ID: "settings", Type: "page", WebSocketDebuggerURL: base + "/devtools/page/settings"}}
	fake.pagesMu.Unlock()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, _ map[string]any) (any, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return map[string]any{"result": map[string]any{"value": true}}, nil
	})
	manager := NewSessionManager(fake.server.URL, true)
	client := &developerModeFixture{cleanup: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.enableExtensionDeveloperMode(ctx, client) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("extension setup did not reach its settings tab")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled extension setup, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled extension setup did not stop promptly")
	}
	select {
	case err := <-client.cleanup:
		if err != nil {
			t.Fatalf("settings tab cleanup used a canceled context: %v", err)
		}
	default:
		t.Fatal("settings tab was not closed")
	}
	release.Do(func() { close(gate) })
}
