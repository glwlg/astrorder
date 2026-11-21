package targetfs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestSSHResolverAllowsRemoteAntigravityBrainRootOutsideProjectWorkspace(t *testing.T) {
	store := resolverStore{
		connection: map[string]any{
			"id": "wsl-main",
			"settings": map[string]any{
				"host":      "127.0.0.1",
				"user":      "fixture",
				"workspace": "/home/fixture/workspace/demo",
			},
		},
	}
	resolver := NewResolver(ResolverOptions{
		Store:      store,
		HelperPath: "/opt/astrorder/targetfs-helper",
	})
	resolved, err := resolver.Resolve(context.Background(), ResolveRequest{
		ConnectionID: "wsl-main",
		Workspace:    "/home/fixture/.gemini/antigravity-cli/brain/52794208-5f2e-4120-a6b1-99e5ee29c7f1",
	})
	if err != nil || resolved.Kind != "ssh" {
		t.Fatalf("resolve ssh brain workspace: resolved=%+v err=%v", resolved, err)
	}
	rfs, ok := resolved.FileSystem.(*remoteFileSystem)
	if !ok {
		t.Fatalf("expected *remoteFileSystem, got %T", resolved.FileSystem)
	}
	foundGemini := false
	for _, root := range rfs.allowedRoots {
		if root == "/home/fixture/.gemini" {
			foundGemini = true
			break
		}
	}
	if !foundGemini {
		t.Fatalf("expected /home/fixture/.gemini in allowedRoots, got %v", rfs.allowedRoots)
	}
}

func TestResolverAllowTempDirsSupportsLocalAndRemoteTempRoots(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	optDir := filepath.Join(base, "opt")
	outsideDir := filepath.Join(base, "outside")
	for _, dir := range []string{root, optDir, outsideDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// NewLocal silently skips non-existent OptionalRoots while strictly rejecting non-existent AllowedRoots.
	missingDir := filepath.Join(base, "does-not-exist")
	if _, err := NewLocal(LocalOptions{Workspace: root, AllowedRoots: []string{missingDir}}); err == nil {
		t.Fatal("expected 422 error for missing AllowedRoots")
	}
	fsys, err := NewLocal(LocalOptions{
		Workspace:     root,
		AllowedRoots:  []string{root},
		OptionalRoots: []string{optDir, missingDir},
	})
	if err != nil {
		t.Fatalf("NewLocal with OptionalRoots failed: %v", err)
	}
	optFile := filepath.Join(optDir, "artifact.txt")
	if err := os.WriteFile(optFile, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Stat(context.Background(), optFile); err != nil {
		t.Fatalf("expected file inside OptionalRoots to be readable, got %v", err)
	}
	link := filepath.Join(root, "escape-link")
	if err := os.Symlink(outsideFile, link); err == nil {
		if _, statErr := fsys.Stat(context.Background(), link); !IsForbidden(statErr) {
			t.Fatalf("expected symlink escape outside AllowedRoots+OptionalRoots to return 403, got %v", statErr)
		}
	}

	// Local Resolver with AllowTempDirs=true opens files in os.TempDir(), while AllowTempDirs=false blocks them.
	tempFile, err := os.CreateTemp("", "astrorder-resolver-temp-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()
	t.Cleanup(func() { _ = os.Remove(tempPath) })

	strictResolver := NewResolver(ResolverOptions{
		Store:            resolverStore{},
		DefaultWorkspace: root,
		AllowedRoots:     []string{root},
		AllowTempDirs:    false,
	})
	strictResolved, err := strictResolver.Resolve(context.Background(), ResolveRequest{Workspace: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strictResolved.FileSystem.Stat(context.Background(), tempPath); !IsForbidden(err) {
		t.Fatalf("expected strict resolver to forbid temp file %s, got %v", tempPath, err)
	}

	tempResolver := NewResolver(ResolverOptions{
		Store:            resolverStore{},
		DefaultWorkspace: root,
		AllowedRoots:     []string{root},
		AllowTempDirs:    true,
	})
	tempResolved, err := tempResolver.Resolve(context.Background(), ResolveRequest{Workspace: filepath.Dir(tempPath)})
	if err != nil {
		t.Fatalf("expected temp-enabled local resolve to succeed, got %v", err)
	}
	if _, err := tempResolved.FileSystem.Stat(context.Background(), tempPath); err != nil {
		t.Fatalf("expected temp-enabled local stat on %s to succeed, got %v", tempPath, err)
	}

	// SSH and WSL include /tmp, /var/tmp, and session workspace in optional_roots when AllowTempDirs=true.
	store := resolverStore{
		session: map[string]any{
			"id":            "sess-1",
			"workspace":     "/home/fixture/session-ws",
			"connection_id": "ssh-1",
		},
		connection: map[string]any{
			"id": "ssh-1",
			"settings": map[string]any{
				"host":      "127.0.0.1",
				"user":      "fixture",
				"workspace": "/home/fixture/workspace",
			},
		},
	}
	sshResolver := NewResolver(ResolverOptions{
		Store:         store,
		HelperPath:    "/opt/astrorder/astrorder-helper",
		WSLHelperPath: "/opt/astrorder/astrorder-helper",
		AllowTempDirs: true,
	})
	sshResolved, err := sshResolver.Resolve(context.Background(), ResolveRequest{
		SessionID:    "sess-1",
		ConnectionID: "ssh-1",
		Workspace:    "/tmp",
	})
	if err != nil {
		t.Fatalf("SSH resolve failed: %v", err)
	}
	sshFS := sshResolved.FileSystem.(*remoteFileSystem)
	rawEnvelope, err := sshFS.envelope(map[string]any{"path": "/tmp/report.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	var envObj scopeRequest
	if err := json.Unmarshal(rawEnvelope, &envObj); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(envObj.OptionalRoots, ",")
	for _, want := range []string{"/home/fixture/session-ws", "/tmp", "/var/tmp"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in SSH optional_roots %v", want, envObj.OptionalRoots)
		}
	}

	wslResolved, err := sshResolver.Resolve(context.Background(), ResolveRequest{
		ConnectionID: "wsl:Debian",
		Workspace:    "/home/fixture/workspace",
	})
	if err != nil {
		t.Fatalf("WSL resolve failed: %v", err)
	}
	wslFS := wslResolved.FileSystem.(*remoteFileSystem)
	if strings.Join(wslFS.optionalRoots, ",") != "/tmp,/var/tmp" {
		t.Fatalf("unexpected WSL optionalRoots: %v", wslFS.optionalRoots)
	}
}
