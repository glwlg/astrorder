package remote

import (
	"testing"
)

func TestParseSSHConfig(t *testing.T) {
	settings := map[string]any{
		"host": "test.example",
		"user": "operator",
		"port": 2222.0,
	}
	cfg, err := ParseSSHConfig(settings)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "test.example" || cfg.User != "operator" || cfg.Port != 2222 {
		t.Fatalf("unexpected parsed config: %+v", cfg)
	}

	// Host required
	_, err = ParseSSHConfig(map[string]any{"port": 22})
	if err == nil {
		t.Fatalf("expected error for missing host")
	}
}

func TestRemoteFactoriesValidateExecutable(t *testing.T) {
	emit := func(string, string, map[string]any, string) error { return nil }
	codexFactory := NewRemoteCodexFactory(emit)

	// Missing codex_executable
	_, err := codexFactory("conn-1", map[string]any{"host": "test.example"})
	if err == nil {
		t.Fatalf("expected error for missing codex_executable")
	}

	// Relative path
	_, err = codexFactory("conn-1", map[string]any{"host": "test.example", "codex_executable": "bin/codex"})
	if err == nil {
		t.Fatalf("expected error for relative codex_executable")
	}

	grokFactory := NewRemoteGrokFactory(emit)
	_, err = grokFactory("conn-1", map[string]any{"host": "test.example", "grok_executable": "bin/grok"})
	if err == nil {
		t.Fatalf("expected error for relative grok_executable")
	}

	// Hermes factory validates host
	hermesFactory := NewRemoteHermesFactory(emit)
	_, err = hermesFactory("conn-1", map[string]any{"host": ""})
	if err == nil {
		t.Fatalf("expected error for missing host in hermesFactory")
	}

	// Hermes factory constructs valid adapter
	adapter, err := hermesFactory("conn-1", map[string]any{"host": "test.example"})
	if err != nil {
		t.Fatalf("unexpected error creating hermes adapter: %v", err)
	}
	if adapter == nil {
		t.Fatalf("expected adapter, got nil")
	}
}
