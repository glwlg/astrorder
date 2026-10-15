package browser

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type extensionFixture struct {
	enabled, installed bool
	loads              int
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
