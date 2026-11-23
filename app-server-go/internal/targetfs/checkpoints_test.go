package targetfs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = checkpointGitEnv("")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n\nfunc version() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".gitignore", "app.go")
	run("commit", "-m", "initial")
	return root
}

func TestTurnCheckpointCaptureDiffRestorePreservesIgnoredFilesAndCreatesBackup(t *testing.T) {
	repo := initTestRepo(t)
	fsys, err := NewLocal(LocalOptions{Workspace: repo, AllowedRoots: []string{repo}})
	if err != nil {
		t.Fatal(err)
	}

	// Secret gitignored file must NEVER be touched by capture or restore.
	envFile := filepath.Join(repo, ".env")
	if err := os.WriteFile(envFile, []byte("SECRET=keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Turn 1: modify app.go
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package main\n\nfunc version() int { return 2 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res1, err := fsys.ExecGit(context.Background(), GitRequest{
		Operation: GitCheckpointCapture,
		Workspace: repo,
		Branch:    "refs/astrorder/checkpoints/test-session/turn-1",
	})
	if err != nil {
		t.Fatalf("capture turn 1: %v", err)
	}
	cp1, err := ParseCheckpointCapture(res1.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if cp1.FilesChanged != 1 || cp1.Insertions != 1 || cp1.Deletions != 1 {
		t.Fatalf("unexpected turn 1 stats: %+v", cp1)
	}

	// Verify real .git/index was NOT polluted (no staged changes)
	stagedCheck, err := fsys.ExecGit(context.Background(), GitRequest{Operation: GitNumstat, Workspace: repo, Cached: true})
	if err != nil || len(stagedCheck.Stdout) != 0 {
		t.Fatalf("checkpoint capture polluted real git index: err=%v out=%q", err, string(stagedCheck.Stdout))
	}

	// Turn 2: edit app.go again and add a brand-new untracked file in a new subdir
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package main\n\nfunc version() int { return 3 }\n// extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	extraFile := filepath.Join(repo, "sub", "helper.go")
	if err := os.MkdirAll(filepath.Dir(extraFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extraFile, []byte("package sub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res2, err := fsys.ExecGit(context.Background(), GitRequest{
		Operation:  GitCheckpointCapture,
		Workspace:  repo,
		Branch:     "refs/astrorder/checkpoints/test-session/turn-2",
		BaseBranch: cp1.TreeSHA,
	})
	if err != nil {
		t.Fatalf("capture turn 2: %v", err)
	}
	cp2, err := ParseCheckpointCapture(res2.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if cp2.FilesChanged != 2 {
		t.Fatalf("expected 2 changed files between turn 1 and turn 2, got %+v", cp2)
	}

	// Diff between Turn 1 and Turn 2
	diffRes, err := fsys.ExecGit(context.Background(), GitRequest{
		Operation:  GitCheckpointDiff,
		Workspace:  repo,
		BaseBranch: cp1.TreeSHA,
		Branch:     cp2.TreeSHA,
	})
	if err != nil {
		t.Fatalf("diff checkpoints: %v", err)
	}
	diffWire, err := ParseCheckpointDiff(diffRes.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if diffWire.FilesChanged != 2 || !strings.Contains(diffWire.Diff, "return 3") || !strings.Contains(diffWire.Diff, "package sub") {
		t.Fatalf("unexpected diff: %+v", diffWire)
	}

	// Restore back to Turn 1 with automatic safety backup
	restoreRes, err := fsys.ExecGit(context.Background(), GitRequest{
		Operation:  GitCheckpointRestore,
		Workspace:  repo,
		BaseBranch: cp1.TreeSHA,
		Branch:     "refs/astrorder/checkpoints/test-session/pre-restore",
	})
	if err != nil {
		t.Fatalf("restore turn 1: %v", err)
	}
	restoreWire, err := ParseCheckpointRestore(restoreRes.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if restoreWire.RemovedFiles != 1 || restoreWire.Backup == nil || restoreWire.Backup.TreeSHA != cp2.TreeSHA {
		t.Fatalf("unexpected restore result: %+v", restoreWire)
	}

	// Verify app.go reverted to version 2, sub/helper.go was removed, and .env stayed untouched!
	gotApp, err := os.ReadFile(filepath.Join(repo, "app.go"))
	if err != nil || !strings.Contains(string(gotApp), "return 2") {
		t.Fatalf("app.go not restored to turn 1: %q err=%v", string(gotApp), err)
	}
	if _, statErr := os.Stat(extraFile); !os.IsNotExist(statErr) {
		t.Fatalf("turn 2 untracked file should have been removed: %v", statErr)
	}
	gotEnv, err := os.ReadFile(envFile)
	if err != nil || string(gotEnv) != "SECRET=keep-me\n" {
		t.Fatalf(".gitignore file was harmed by restore: %q err=%v", string(gotEnv), err)
	}

	// Roll forward using the automatic safety backup checkpoint!
	redoRes, err := fsys.ExecGit(context.Background(), GitRequest{
		Operation:  GitCheckpointRestore,
		Workspace:  repo,
		BaseBranch: restoreWire.Backup.TreeSHA,
	})
	if err != nil {
		t.Fatalf("redo via backup checkpoint: %v", err)
	}
	if _, err := ParseCheckpointRestore(redoRes.Stdout); err != nil {
		t.Fatal(err)
	}
	gotApp2, _ := os.ReadFile(filepath.Join(repo, "app.go"))
	gotHelper2, _ := os.ReadFile(extraFile)
	if !strings.Contains(string(gotApp2), "return 3") || string(gotHelper2) != "package sub\n" {
		t.Fatalf("roll forward failed: app=%q helper=%q", string(gotApp2), string(gotHelper2))
	}

	// Delete checkpoint ref cleanly
	if _, err := fsys.ExecGit(context.Background(), GitRequest{
		Operation: GitCheckpointDelete,
		Workspace: repo,
		Branch:    cp1.RefName,
	}); err != nil {
		t.Fatalf("delete checkpoint ref: %v", err)
	}
}
