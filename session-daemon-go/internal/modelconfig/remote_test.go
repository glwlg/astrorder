package modelconfig

import (
	"context"
	"testing"
)

func TestModelConfigRemoteRejectsInvalidTarget(t *testing.T) {
	svc, err := New(Config{AllowedRoots: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 1. Missing target kind
	var callErr error
	_, callErr = svc.Execute(ctx, "model_config.plan", map[string]any{
		"target": map[string]any{"kind": "unknown_cluster"},
	})
	if callErr == nil {
		t.Fatal("expected error for invalid target kind")
	}

	// 2. WSL without distro
	_, callErr = svc.Execute(ctx, "model_config.plan", map[string]any{
		"target": map[string]any{"kind": "wsl", "distro": ""},
	})
	if callErr == nil {
		t.Fatal("expected error for missing wsl distro")
	}

	// 3. SSH without settings
	_, callErr = svc.Execute(ctx, "model_config.plan", map[string]any{
		"target": map[string]any{"kind": "ssh"},
	})
	if callErr == nil {
		t.Fatal("expected error for missing ssh settings")
	}

	// 4. SSH with invalid port
	_, callErr = svc.Execute(ctx, "model_config.plan", map[string]any{
		"target": map[string]any{
			"kind": "ssh",
			"settings": map[string]any{
				"host": "remote.dev",
				"port": 99999,
			},
		},
	})
	if callErr == nil {
		t.Fatal("expected error for invalid ssh port")
	}
}
