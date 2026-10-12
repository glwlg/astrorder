package targetfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolverUsesConfiguredDefaultAndNeverFallsBackForUnknownRemote(t *testing.T) {
	root := t.TempDir()
	resolver := NewResolver(ResolverOptions{Store: resolverStore{}, DefaultWorkspace: root, AllowedRoots: []string{root}})
	resolved, err := resolver.Resolve(context.Background(), ResolveRequest{})
	if err != nil || resolved.Kind != "local" || resolved.ConnectionID != LocalConnectionID {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	_, err = resolver.Resolve(context.Background(), ResolveRequest{Workspace: root, ConnectionID: "missing"})
	var target *Error
	if !errors.As(err, &target) || target.Status != 404 || target.Code != "connection_not_found" {
		t.Fatalf("unknown remote error=%#v", err)
	}
}

func TestResolverPropagatesSessionLookupFailure(t *testing.T) {
	root := t.TempDir()
	resolver := NewResolver(ResolverOptions{Store: resolverStore{sessionErr: errors.New("db unavailable")}, DefaultWorkspace: root, AllowedRoots: []string{root}})
	_, err := resolver.Resolve(context.Background(), ResolveRequest{SessionID: "session"})
	var target *Error
	if !errors.As(err, &target) || target.Status != 500 || target.Code != "session_lookup_failed" {
		t.Fatalf("lookup error=%#v", err)
	}
}

func TestValidateExportPathsRejectsSymlinkEscapeAndNameCollision(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err == nil {
		if err := validateExportPaths(context.Background(), []string{root}, []string{root}); err == nil {
			t.Fatal("symlink export escape was accepted")
		}
	}
	left := filepath.Join(root, "a", "same.txt")
	right := filepath.Join(root, "b", "same.txt")
	if err := os.MkdirAll(filepath.Dir(left), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(right), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(left, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExportPaths(context.Background(), []string{left, right}, []string{root}); err == nil {
		t.Fatal("duplicate export basename was accepted")
	}
}

type resolverStore struct {
	session    map[string]any
	connection map[string]any
	sessionErr error
}

func (s resolverStore) FindSessionByID(context.Context, string) (map[string]any, error) {
	return s.session, s.sessionErr
}

func (s resolverStore) GetSSHConnection(context.Context, string) (map[string]any, error) {
	return s.connection, nil
}
