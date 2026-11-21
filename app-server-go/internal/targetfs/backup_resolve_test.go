package targetfs

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveScopedRemoteValidatesInputsAndBuildsScopedFS(t *testing.T) {
	ctx := context.Background()
	resolver := NewResolver(ResolverOptions{
		Store: resolverStore{
			connection: map[string]any{
				"id": "ssh-demo",
				"settings": map[string]any{
					"host": "10.0.0.2",
					"user": "deploy",
					"port": 2222,
				},
			},
		},
		HelperPath:    "/usr/local/bin/astrorder-helper",
		WSLHelperPath: "/usr/local/bin/astrorder-helper",
	})

	if _, err := resolver.ResolveScopedRemote(ctx, LocalConnectionID, "/data", nil); err == nil {
		t.Fatal("expected local connection rejection")
	} else {
		var target *Error
		if !errors.As(err, &target) || target.Status != 422 || target.Code != "remote_connection_required" {
			t.Fatalf("unexpected local error: %#v", err)
		}
	}

	if _, err := resolver.ResolveScopedRemote(ctx, "ssh-demo", "", nil); err == nil {
		t.Fatal("expected empty rootDir rejection")
	} else {
		var target *Error
		if !errors.As(err, &target) || target.Status != 422 || target.Code != "workspace_required" {
			t.Fatalf("unexpected empty root error: %#v", err)
		}
	}

	nilResolver := NewResolver(ResolverOptions{})
	if _, err := nilResolver.ResolveScopedRemote(ctx, "ssh-demo", "/var/data", nil); err == nil {
		t.Fatal("expected nil store rejection")
	} else {
		var target *Error
		if !errors.As(err, &target) || target.Status != 503 || target.Code != "connection_store_unavailable" {
			t.Fatalf("unexpected nil store error: %#v", err)
		}
	}

	emptyStoreResolver := NewResolver(ResolverOptions{Store: resolverStore{}})
	if _, err := emptyStoreResolver.ResolveScopedRemote(ctx, "missing-id", "/var/data", nil); err == nil {
		t.Fatal("expected unknown connection rejection")
	} else {
		var target *Error
		if !errors.As(err, &target) || target.Status != 404 || target.Code != "connection_not_found" {
			t.Fatalf("unexpected missing connection error: %#v", err)
		}
	}

	stageRoot := filepath.Join(t.TempDir(), "stage")
	wslResolved, err := resolver.ResolveScopedRemote(ctx, "wsl:Debian", "/srv/app", []string{stageRoot})
	if err != nil {
		t.Fatalf("wsl resolve failed: %v", err)
	}
	if wslResolved.Kind != "wsl" || wslResolved.Workspace != "/srv/app" {
		t.Fatalf("unexpected wsl resolved: %+v", wslResolved)
	}
	wslRemote, ok := wslResolved.FileSystem.(*remoteFileSystem)
	if !ok || len(wslRemote.allowedRoots) != 1 || wslRemote.allowedRoots[0] != "/srv/app" || len(wslRemote.stageRoots) != 1 || wslRemote.stageRoots[0] != stageRoot {
		t.Fatalf("unexpected wsl remote config: %#v", wslRemote)
	}

	sshResolved, err := resolver.ResolveScopedRemote(ctx, "ssh-demo", "/srv/project", []string{stageRoot})
	if err != nil {
		t.Fatalf("ssh resolve failed: %v", err)
	}
	if sshResolved.Kind != "ssh" || sshResolved.Workspace != "/srv/project" {
		t.Fatalf("unexpected ssh resolved: %+v", sshResolved)
	}
	sshRemote, ok := sshResolved.FileSystem.(*remoteFileSystem)
	if !ok || len(sshRemote.allowedRoots) != 1 || sshRemote.allowedRoots[0] != "/srv/project" {
		t.Fatalf("unexpected ssh remote config: %#v", sshRemote)
	}
	joined := strings.Join(sshRemote.command, " ")
	if !strings.Contains(joined, "10.0.0.2") || !strings.Contains(joined, "2222") {
		t.Fatalf("unexpected ssh command: %q", joined)
	}
}
