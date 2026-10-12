package targetfs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAcceptanceSnapshotAllowsDoubleDotInFileName(t *testing.T) {
	repo := snapshotRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "version..notes.txt"), []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	filesystem, err := NewLocal(LocalOptions{Workspace: repo, AllowedRoots: []string{repo}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = filesystem.ExecGit(context.Background(), GitRequest{Operation: GitSnapshotReadonly, Workspace: repo}); err != nil {
		t.Fatalf("valid filename rejected as traversal: %v", err)
	}
}

func TestAcceptanceSnapshotResolvesUntrackedSubdirectoryPaths(t *testing.T) {
	repo := snapshotRepo(t)
	sub := filepath.Join(repo, "subdirectory")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "draft.txt"), []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	filesystem, err := NewLocal(LocalOptions{Workspace: sub, AllowedRoots: []string{repo}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := filesystem.ExecGit(context.Background(), GitRequest{Operation: GitSnapshotReadonly, Workspace: sub})
	if err != nil {
		t.Fatalf("subdirectory snapshot failed to resolve actual untracked file: %v", err)
	}
	first, err := ParseSnapshot(result.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "draft.txt"), []byte("changed private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = filesystem.ExecGit(context.Background(), GitRequest{Operation: GitSnapshotReadonly, Workspace: sub})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseSnapshot(result.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if first.ChangeFingerprint == second.ChangeFingerprint {
		t.Fatal("subdirectory untracked content change was ignored")
	}
}

func TestAcceptanceSnapshotLinkTargetParticipatesInFingerprint(t *testing.T) {
	repo := snapshotRepo(t)
	for _, name := range []string{"first.txt", "other.txt"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(repo, "untracked-link")
	if err := os.Symlink("first.txt", link); err != nil {
		t.Skipf("platform cannot create fixture symbolic link: %v", err)
	}
	first := localSnapshot(t, repo)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.txt", link); err != nil {
		t.Fatal(err)
	}
	second := localSnapshot(t, repo)
	if first.ChangeFingerprint == second.ChangeFingerprint {
		t.Fatal("changed symbolic link target did not change fingerprint")
	}
}

// TestAcceptanceSnapshotBoundedOutputKillsRunawayChild proves the snapshot
// output cap is a hard cap: a child that produces unbounded output is killed
// while the command context is cancelled, instead of buffering the whole
// stream before a post-run length check.
func TestAcceptanceSnapshotBoundedOutputKillsRunawayChild(t *testing.T) {
	repo := snapshotRepo(t)
	binary, args := runawayOutputCommand()
	start := time.Now()
	_, err := snapshotGit(context.Background(), binary, repo, 64, args...)
	if err == nil {
		t.Fatal("runaway snapshot output was accepted instead of failing")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("runaway child was not killed promptly: %s", elapsed)
	}
}

func runawayOutputCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/c", "for /L %i in (1,1,1000000000) do @echo XXXXXXXXXXXXXXXXXXXXXXXXXXXX"}
	}
	return "sh", []string{"-c", "yes XXXXXXXXXXXXXXXXXXXXXXXXXXXX"}
}

// TestAcceptanceSnapshotLinkOutsideWorkspaceNeverReadsTarget proves an
// untracked symbolic link whose target lives outside the workspace is hashed
// by its link text only: changing the outside file's content must not change
// the fingerprint, while changing the link text still must.
func TestAcceptanceSnapshotLinkOutsideWorkspaceNeverReadsTarget(t *testing.T) {
	repo := snapshotRepo(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("outside-A"), 0600); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(repo, secret)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, "outside-link")
	if err := os.Symlink(relative, link); err != nil {
		t.Skipf("platform cannot create fixture symbolic link: %v", err)
	}
	first := localSnapshot(t, repo)
	if err := os.WriteFile(secret, []byte("outside-B"), 0600); err != nil {
		t.Fatal(err)
	}
	second := localSnapshot(t, repo)
	if first.ChangeFingerprint != second.ChangeFingerprint {
		t.Fatal("outside content change changed the fingerprint; the link target was read")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("another-name.txt", link); err != nil {
		t.Fatal(err)
	}
	third := localSnapshot(t, repo)
	if third.ChangeFingerprint == first.ChangeFingerprint {
		t.Fatal("link text change did not change the fingerprint")
	}
}

// TestAcceptanceSnapshotUntrackedOutsideAllowedScopeFailsClosed proves the
// snapshot scoped to a subdirectory never expands its scope to the repository
// root: an untracked file at the repository root is not listed or read from
// that workspace, so its content changes cannot influence the fingerprint,
// while an in-scope untracked change still does.
func TestAcceptanceSnapshotUntrackedOutsideAllowedScopeFailsClosed(t *testing.T) {
	repo := snapshotRepo(t)
	sub := filepath.Join(repo, "subdirectory")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(sub, "draft.txt")
	if err := os.WriteFile(inside, []byte("in scope"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(repo, "root-untracked.txt")
	if err := os.WriteFile(outside, []byte("outside scope A"), 0600); err != nil {
		t.Fatal(err)
	}
	filesystem, err := NewLocal(LocalOptions{Workspace: sub, AllowedRoots: []string{sub}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() SnapshotWire {
		t.Helper()
		result, err := filesystem.ExecGit(context.Background(), GitRequest{Operation: GitSnapshotReadonly, Workspace: sub})
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseSnapshot(result.Stdout)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	first := snapshot()
	if err := os.WriteFile(outside, []byte("outside scope B"), 0600); err != nil {
		t.Fatal(err)
	}
	second := snapshot()
	if first.ChangeFingerprint != second.ChangeFingerprint {
		t.Fatal("repository-root content leaked into a subdirectory-scoped fingerprint")
	}
	if err := os.WriteFile(inside, []byte("in scope changed"), 0600); err != nil {
		t.Fatal(err)
	}
	third := snapshot()
	if third.ChangeFingerprint == first.ChangeFingerprint {
		t.Fatal("in-scope untracked content change was ignored")
	}
}
