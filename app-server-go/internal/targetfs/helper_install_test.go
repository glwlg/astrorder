package targetfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolverRemoteHelperReportsMissingBundledAssets(t *testing.T) {
	for _, connectionID := range []string{"wsl:Ubuntu", "ssh-test"} {
		t.Run(connectionID, func(t *testing.T) {
			resolver := NewResolver(ResolverOptions{
				Store:           resolverStore{connection: map[string]any{"host": "example.test"}},
				HelperAssetsDir: filepath.Join(t.TempDir(), "missing"),
			})
			_, err := resolver.Resolve(context.Background(), ResolveRequest{Workspace: "/home/dev/project", ConnectionID: connectionID})
			var target *Error
			if !errors.As(err, &target) || target.Status != 503 || target.Code != "helper_asset_missing" {
				t.Fatalf("missing bundled helper error=%#v", err)
			}
			_, err = resolver.RunHelper(context.Background(), connectionID, "install-hooks")
			if !errors.As(err, &target) || target.Status != 503 || target.Code != "helper_asset_missing" {
				t.Fatalf("missing bundled helper runner error=%#v", err)
			}
		})
	}
}

func TestResolverRemoteHelperPreservesInstallationFailure(t *testing.T) {
	for _, connectionID := range []string{"wsl:Ubuntu", "ssh-test"} {
		t.Run(connectionID, func(t *testing.T) {
			root := t.TempDir()
			resolver := NewResolver(ResolverOptions{
				Store:           resolverStore{connection: map[string]any{"host": "example.test"}},
				HelperAssetsDir: root,
				SSHBinary:       filepath.Join(root, "missing-ssh"),
				WSLBinary:       filepath.Join(root, "missing-wsl"),
			})
			_, err := resolver.Resolve(context.Background(), ResolveRequest{Workspace: "/home/dev/project", ConnectionID: connectionID})
			var target *Error
			if !errors.As(err, &target) || target.Status != 503 || target.Code != "helper_install_failed" {
				t.Fatalf("helper installation error=%#v", err)
			}
			_, err = resolver.RunHelper(context.Background(), connectionID, "install-hooks")
			if !errors.As(err, &target) || target.Status != 503 || target.Code != "helper_install_failed" {
				t.Fatalf("helper runner installation error=%#v", err)
			}
		})
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
