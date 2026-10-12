//go:build !windows

package modelconfig

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemoteModelHelperInstallsBundledBinary(t *testing.T) {
	binary := buildModelConfigHelper(t)
	assets, home := t.TempDir(), t.TempDir()
	platform := "linux-" + runtime.GOARCH
	assetDir := filepath.Join(assets, platform)
	if err := os.MkdirAll(assetDir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(assetDir, "astrorder-model-helper")
	if err := os.WriteFile(asset, data, 0700); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if err := os.WriteFile(filepath.Join(assetDir, "SHA256SUMS"), []byte(digest+"  astrorder-model-helper\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := []byte("model = \"bundle-original\"\n")
	path := filepath.Join(home, RelCodexConfig)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc, err := New(Config{
		AllowedRoots: []string{t.TempDir()}, HelperAssetsDir: assets,
		RemoteCommandRunner: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			calls++
			if name != "ssh" || len(args) < 2 || args[0] == name {
				t.Errorf("incorrect SSH argv: %q %v", name, args)
			}
			// Execute exactly the generated remote shell command. No model output
			// or receipt is substituted by this transport fixture.
			return exec.CommandContext(ctx, "env", "HOME="+home, "sh", "-c", args[len(args)-1])
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Execute(context.Background(), "model_config.plan", map[string]any{
		"target": map[string]any{"kind": "ssh", "settings": map[string]any{"host": "fixture.invalid"}},
		"files":  map[string]any{FileCodexConfig: "model = \"bundle-updated\"\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changes, ok := res["changes"].([]any); !ok || len(changes) != 1 {
		t.Fatal("bundled helper did not execute preview")
	}
	installed := filepath.Join(home, ".local", "lib", "astrorder", "model-helper", digest, "astrorder-model-helper")
	installedData, err := os.ReadFile(installed)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(installedData)) != digest {
		t.Fatalf("installed helper bytes differ from bundle: %v", err)
	}
	if info, err := os.Stat(installed); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("installed helper permissions: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(before) {
		t.Fatalf("helper installation changed native config: %v", err)
	}
	if calls < 3 {
		t.Fatal("missing platform discovery, install or helper execution")
	}
	// A tampered bundled binary must fail before executing the helper.
	if err := os.WriteFile(asset, []byte("corrupt-asset"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Execute(context.Background(), "model_config.plan", map[string]any{
		"target": map[string]any{"kind": "ssh", "settings": map[string]any{"host": "fixture.invalid"}},
	})
	if err == nil || !strings.Contains(err.Error(), "helper") {
		t.Fatalf("invalid bundled helper accepted: %v", err)
	}
}
