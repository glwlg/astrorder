package targetfs

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func snapshotRepo(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runSnapshotGit(t, repository, "init", "-q")
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runSnapshotGit(t, repository, "add", "base.txt")
	runSnapshotGit(t, repository, "-c", "user.name=Astrorder Test", "-c", "user.email=test@invalid", "commit", "-qm", "base")
	return repository
}

func runSnapshotGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}

func localSnapshot(t *testing.T, repository string) SnapshotWire {
	t.Helper()
	filesystem, err := NewLocal(LocalOptions{Workspace: repository})
	if err != nil {
		t.Fatal(err)
	}
	result, err := filesystem.ExecGit(context.Background(), GitRequest{Operation: GitSnapshotReadonly, Workspace: repository})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("snapshot exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	snapshot, err := ParseSnapshot(result.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSnapshotReadonlyIdentityAndUntrackedContentFingerprint(t *testing.T) {
	repository := snapshotRepo(t)
	first := localSnapshot(t, repository)
	branch := strings.TrimSpace(runSnapshotGit(t, repository, "rev-parse", "--abbrev-ref", "HEAD"))
	head := strings.TrimSpace(runSnapshotGit(t, repository, "rev-parse", "HEAD"))
	if first.Branch != branch || first.Head != head {
		t.Fatalf("identity = %+v want branch=%q head=%q", first, branch, head)
	}
	if first.RepoRoot == "" || first.ChangeFingerprint == "" {
		t.Fatalf("snapshot incomplete = %+v", first)
	}

	// A clean tree still has a stable fingerprint.
	if repeat := localSnapshot(t, repository); repeat.ChangeFingerprint != first.ChangeFingerprint {
		t.Fatalf("clean fingerprint drifted: %q vs %q", repeat.ChangeFingerprint, first.ChangeFingerprint)
	}

	// A content-only change to an untracked file must change the fingerprint.
	untracked := filepath.Join(repository, "notes 空格.txt")
	if err := os.WriteFile(untracked, []byte("first content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withUntracked := localSnapshot(t, repository)
	if withUntracked.ChangeFingerprint == first.ChangeFingerprint {
		t.Fatal("untracked file did not change the fingerprint")
	}
	if err := os.WriteFile(untracked, []byte("different content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := localSnapshot(t, repository)
	if modified.ChangeFingerprint == withUntracked.ChangeFingerprint {
		t.Fatal("untracked content change did not change the fingerprint")
	}
	if modified.Head != first.Head {
		t.Fatal("snapshot mutated HEAD")
	}

	// Tracked working-tree changes also change the fingerprint.
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tracked := localSnapshot(t, repository)
	if tracked.ChangeFingerprint == modified.ChangeFingerprint {
		t.Fatal("tracked change did not change the fingerprint")
	}
	if !SnapshotsMatch(tracked, localSnapshot(t, repository)) {
		t.Fatal("identical worktrees did not match")
	}
	if SnapshotsMatch(tracked, first) {
		t.Fatal("different worktrees matched")
	}
}

func TestSnapshotDetachedHeadFallsBackToCommit(t *testing.T) {
	repository := snapshotRepo(t)
	head := strings.TrimSpace(runSnapshotGit(t, repository, "rev-parse", "HEAD"))
	runSnapshotGit(t, repository, "checkout", "-q", "--detach", "HEAD")
	snapshot := localSnapshot(t, repository)
	if snapshot.Head != head {
		t.Fatalf("detached head = %q want %q", snapshot.Head, head)
	}
	if snapshot.Branch == "" || snapshot.Branch == "HEAD" {
		t.Fatalf("detached branch fallback = %q", snapshot.Branch)
	}
	if !strings.HasPrefix(head, snapshot.Branch) {
		t.Fatalf("detached branch %q is not a short commit of %q", snapshot.Branch, head)
	}
}

func TestSnapshotNonRepositoryFailsExplicitly(t *testing.T) {
	root := t.TempDir()
	// GOTMPDIR may be inside a checkout; stop discovery at the fixture parent.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	filesystem, err := NewLocal(LocalOptions{Workspace: root})
	if err != nil {
		t.Fatal(err)
	}
	_, err = filesystem.ExecGit(context.Background(), GitRequest{Operation: GitSnapshotReadonly, Workspace: root})
	if err == nil {
		t.Fatal("non-repository snapshot did not fail")
	}
	var target *Error
	if !asTargetError(err, &target) || target.Status != 422 {
		t.Fatalf("non-repository error = %v", err)
	}
}

func TestSnapshotHelperProtocolCarriesTypedOperation(t *testing.T) {
	repository := snapshotRepo(t)
	scope := scopeRequest{
		Workspace:    repository,
		AllowedRoots: []string{repository},
		Request:      mustJSON(t, GitRequest{Operation: GitSnapshotReadonly, Workspace: repository}),
	}
	envelope := Request{Version: ProtocolVersion, Action: "git", Data: mustJSON(t, scope)}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := ServeHelper(context.Background(), bytes.NewReader(append(encoded, '\n')), &output); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("helper response = %#v", response)
	}
	var result GitResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	remote, err := ParseSnapshot(result.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	direct := localSnapshot(t, repository)
	if remote != direct {
		t.Fatalf("helper snapshot = %+v direct = %+v", remote, direct)
	}
}

func TestSnapshotRemoteUsesHelperNeverLocalPath(t *testing.T) {
	repository := snapshotRepo(t)
	missing := filepath.Join(t.TempDir(), "missing-helper")
	filesystem, err := NewRemote(RemoteOptions{
		Workspace: repository, AllowedRoots: []string{repository},
		Command: []string{missing},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.ExecGit(context.Background(), GitRequest{Operation: GitSnapshotReadonly, Workspace: repository}); err == nil {
		t.Fatal("remote snapshot silently fell back to a local path")
	}
}

func TestSnapshotCancelledContextFails(t *testing.T) {
	repository := snapshotRepo(t)
	filesystem, err := NewLocal(LocalOptions{Workspace: repository})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := filesystem.ExecGit(ctx, GitRequest{Operation: GitSnapshotReadonly, Workspace: repository}); err == nil {
		t.Fatal("cancelled snapshot succeeded")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func asTargetError(err error, target **Error) bool {
	for err != nil {
		if typed, ok := err.(*Error); ok {
			*target = typed
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}
