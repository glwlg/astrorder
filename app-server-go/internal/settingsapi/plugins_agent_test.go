package settingsapi

import (
	"context"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestBootstrapAgentPluginDefaults(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	enabled, err := st.BootstrapAgentPluginDefaults(ctx, func(kind string) bool {
		return kind == "codex" || kind == "antigravity"
	})
	if err != nil {
		t.Fatal(err)
	}
	if !enabled["agent-codex"] || !enabled["agent-antigravity"] {
		t.Fatalf("expected installed agents enabled, got %+v", enabled)
	}
	if enabled["agent-hermes"] || enabled["agent-grok"] {
		t.Fatalf("expected uninstalled agents disabled, got %+v", enabled)
	}

	// Explicit user changes must not be overwritten on subsequent bootstrap calls
	enabled["agent-codex"] = false
	enabled["agent-hermes"] = true
	if err := st.SetPreference(ctx, prefPluginsEnabled, enabled, true); err != nil {
		t.Fatal(err)
	}
	after, err := st.BootstrapAgentPluginDefaults(ctx, func(kind string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if after["agent-codex"] || !after["agent-hermes"] {
		t.Fatalf("bootstrap overwrote user choices: %+v", after)
	}

	kinds := st.ReadEnabledAgentKinds(ctx)
	if kinds["codex"] || !kinds["hermes"] || !kinds["antigravity"] || kinds["grok"] {
		t.Fatalf("unexpected enabled kinds: %+v", kinds)
	}
}
