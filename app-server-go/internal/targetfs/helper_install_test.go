package targetfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolverRemoteHelperFallsBackToPathWithoutBundledAssets(t *testing.T) {
	root := t.TempDir()
	resolver := NewResolver(ResolverOptions{DefaultWorkspace: root, AllowedRoots: []string{root}, HelperAssetsDir: filepath.Join(root, "missing")})
	resolved, err := resolver.Resolve(context.Background(), ResolveRequest{Workspace: "/home/dev/project", ConnectionID: "wsl:Ubuntu"})
	if err != nil || resolved.Kind != "wsl" {
		t.Fatalf("resolved=%+v err=%v (remote targets must not fail with helper_not_configured)", resolved, err)
	}
}

func TestResolverRemoteHelperUsesInstallerWhenAssetsExist(t *testing.T) {
	root := t.TempDir()
	assets := filepath.Join(root, "helpers")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(ResolverOptions{DefaultWorkspace: root, AllowedRoots: []string{root}, HelperAssetsDir: assets})
	if resolver.installer == nil {
		t.Fatal("installer should be enabled when the assets directory exists")
	}
}

func TestLastLineReturnsFinalOutputLine(t *testing.T) {
	if got := lastLine([]byte("noise\nlinux-amd64\n")); got != "linux-amd64" {
		t.Fatalf("lastLine=%q", got)
	}
}
