package targetfs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	repo := filepath.Join(parent, "demo-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitCmd := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	runGitCmd(repo, "init", "-b", "main")
	runGitCmd(repo, "config", "user.name", "Test User")
	runGitCmd(repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.env.local\nsecret.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(repo, "add", ".gitignore", "README.md")
	runGitCmd(repo, "commit", "-m", "init")
	return repo
}

func TestWorktreeFullLifecycleAndHelpers(t *testing.T) {
	ctx := context.Background()
	repo := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("API_KEY=local-dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "secret.json"), []byte(`{"token":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".worktreeinclude"), []byte("secret.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".worktreeinclude"}, {"commit", "-m", "add worktreeinclude"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, cErr := cmd.CombinedOutput(); cErr != nil {
			t.Fatalf("git %v: %v %s", args, cErr, string(out))
		}
	}

	fsys, err := NewLocal(LocalOptions{Workspace: repo, AllowedRoots: []string{repo}})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	// 1. List initial worktrees & copyable ignored files.
	listRes, err := fsys.ExecGit(ctx, GitRequest{
		Operation:       GitWorktreeList,
		Workspace:       repo,
		IncludeStatuses: true,
	})
	if err != nil {
		t.Fatalf("GitWorktreeList: %v", err)
	}
	var initial WorktreeListWire
	if err := json.Unmarshal(listRes.Stdout, &initial); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if !initial.IsGitRepo || initial.IsWorktree || initial.DefaultBranch != "main" {
		t.Fatalf("unexpected initial list: %+v", initial)
	}
	if len(initial.Worktrees) != 1 || !initial.Worktrees[0].IsMain {
		t.Fatalf("expected 1 main worktree, got %+v", initial.Worktrees)
	}
	hasEnv, hasSecret := false, false
	for _, c := range initial.CopyableFiles {
		if c.Path == ".env" {
			hasEnv = true
		}
		if c.Path == "secret.json" && c.Source == "worktreeinclude" {
			hasSecret = true
		}
	}
	if !hasEnv || !hasSecret {
		t.Fatalf("expected .env and secret.json in copyable files, got %+v", initial.CopyableFiles)
	}

	// 2. Reject out-of-scope target path.
	outside := filepath.Join(t.TempDir(), "escape")
	_, err = fsys.ExecGit(ctx, GitRequest{
		Operation:  GitWorktreeAdd,
		Workspace:  repo,
		Branch:     "feat/escape",
		Create:     true,
		TargetPath: outside,
	})
	var targetErr *Error
	if !errors.As(err, &targetErr) || targetErr.Status != 403 {
		t.Fatalf("expected 403 out-of-scope error, got %v", err)
	}

	// 3. Create sibling worktree with env copy.
	addRes, err := fsys.ExecGit(ctx, GitRequest{
		Operation:  GitWorktreeAdd,
		Workspace:  repo,
		Branch:     "feat/alpha",
		BaseBranch: "main",
		Create:     true,
		Layout:     "sibling",
		CopyFiles:  []string{".env", "secret.json", "../escape"},
	})
	if err != nil {
		t.Fatalf("GitWorktreeAdd sibling: %v", err)
	}
	var added WorktreeAddWire
	if err := json.Unmarshal(addRes.Stdout, &added); err != nil {
		t.Fatalf("unmarshal add: %v", err)
	}
	if added.Branch != "feat/alpha" || !strings.Contains(added.Path, "demo-repo-worktrees") {
		t.Fatalf("unexpected added worktree: %+v", added)
	}
	if len(added.CopiedFiles) != 2 {
		t.Fatalf("expected 2 copied files, got %+v", added.CopiedFiles)
	}
	envBytes, err := os.ReadFile(filepath.Join(added.Path, ".env"))
	if err != nil || string(envBytes) != "API_KEY=local-dev\n" {
		t.Fatalf("expected .env copied into worktree, got err=%v content=%q", err, string(envBytes))
	}

	// 4. Duplicate branch already occupied should return 409.
	_, err = fsys.ExecGit(ctx, GitRequest{
		Operation: GitWorktreeAdd,
		Workspace: repo,
		Branch:    "feat/alpha",
		Create:    false,
	})
	if !errors.As(err, &targetErr) || targetErr.Status != 409 {
		t.Fatalf("expected 409 occupied branch error, got %v", err)
	}

	// 5. Create inside worktree (.worktrees) and verify .git/info/exclude keeps main clean.
	insideRes, err := fsys.ExecGit(ctx, GitRequest{
		Operation: GitWorktreeAdd,
		Workspace: repo,
		Branch:    "feat/inside",
		Create:    true,
		Layout:    "inside",
	})
	if err != nil {
		t.Fatalf("GitWorktreeAdd inside: %v", err)
	}
	var insideAdded WorktreeAddWire
	if err := json.Unmarshal(insideRes.Stdout, &insideAdded); err != nil {
		t.Fatalf("unmarshal inside: %v", err)
	}
	if !strings.Contains(insideAdded.Path, ".worktrees") {
		t.Fatalf("expected .worktrees path, got %s", insideAdded.Path)
	}
	excludeData, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(excludeData), ".worktrees/") {
		t.Fatalf("expected .worktrees/ in .git/info/exclude, got err=%v data=%q", err, string(excludeData))
	}

	// 6. Commit on feat/alpha and safely merge back to main.
	if err := os.WriteFile(filepath.Join(added.Path, "feature.txt"), []byte("new feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "feature.txt"}, {"commit", "-m", "add feature"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = added.Path
		if out, cErr := cmd.CombinedOutput(); cErr != nil {
			t.Fatalf("git %v: %v %s", args, cErr, string(out))
		}
	}
	mergeRes, err := fsys.ExecGit(ctx, GitRequest{
		Operation:     GitWorktreeMerge,
		Workspace:     repo,
		Branch:        "feat/alpha",
		BaseBranch:    "main",
		MergeStrategy: "merge",
	})
	if err != nil {
		t.Fatalf("GitWorktreeMerge: %v", err)
	}
	var merged WorktreeMergeWire
	if err := json.Unmarshal(mergeRes.Stdout, &merged); err != nil {
		t.Fatalf("unmarshal merge: %v", err)
	}
	if !merged.Merged || merged.Conflicted {
		t.Fatalf("expected clean merge, got %+v", merged)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("expected feature.txt merged into main: %v", err)
	}

	// 7. Dirty worktree removal guard & force removal + branch delete.
	if err := os.WriteFile(filepath.Join(added.Path, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = fsys.ExecGit(ctx, GitRequest{
		Operation:  GitWorktreeRemove,
		Workspace:  repo,
		TargetPath: added.Path,
		Force:      false,
	})
	if !errors.As(err, &targetErr) || targetErr.Status != 409 {
		t.Fatalf("expected 409 dirty worktree guard, got %v", err)
	}
	rmRes, err := fsys.ExecGit(ctx, GitRequest{
		Operation:    GitWorktreeRemove,
		Workspace:    repo,
		TargetPath:   added.Path,
		Force:        true,
		DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("GitWorktreeRemove force: %v", err)
	}
	var removed WorktreeRemoveWire
	if err := json.Unmarshal(rmRes.Stdout, &removed); err != nil {
		t.Fatalf("unmarshal remove: %v", err)
	}
	if !removed.Removed || !removed.BranchDeleted {
		t.Fatalf("expected worktree and branch deleted, got %+v", removed)
	}

	// 8. Refuse removing main worktree.
	_, err = fsys.ExecGit(ctx, GitRequest{
		Operation:  GitWorktreeRemove,
		Workspace:  repo,
		TargetPath: repo,
		Force:      true,
	})
	if !errors.As(err, &targetErr) || targetErr.Status != 403 {
		t.Fatalf("expected 403 main worktree guard, got %v", err)
	}

	// 9. Conflict merge auto-aborts and keeps main clean.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("main change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "main edit"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, cErr := cmd.CombinedOutput(); cErr != nil {
			t.Fatalf("git %v: %v %s", args, cErr, string(out))
		}
	}
	if err := os.WriteFile(filepath.Join(insideAdded.Path, "README.md"), []byte("inside conflict\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "inside edit"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = insideAdded.Path
		if out, cErr := cmd.CombinedOutput(); cErr != nil {
			t.Fatalf("git %v: %v %s", args, cErr, string(out))
		}
	}
	conflictRes, err := fsys.ExecGit(ctx, GitRequest{
		Operation:     GitWorktreeMerge,
		Workspace:     repo,
		Branch:        "feat/inside",
		BaseBranch:    "main",
		MergeStrategy: "merge",
	})
	if err != nil {
		t.Fatalf("conflict merge should return structured json, got err: %v", err)
	}
	var conflictWire WorktreeMergeWire
	if err := json.Unmarshal(conflictRes.Stdout, &conflictWire); err != nil {
		t.Fatalf("unmarshal conflict: %v", err)
	}
	if conflictWire.Merged || !conflictWire.Conflicted || !conflictWire.Aborted || len(conflictWire.ConflictFiles) == 0 {
		t.Fatalf("expected auto-aborted conflict report, got %+v", conflictWire)
	}

	// 10. Non-git directory returns is_git_repo=false without error.
	nonGit := t.TempDir()
	nonGitFS, err := NewLocal(LocalOptions{Workspace: nonGit, AllowedRoots: []string{nonGit}})
	if err != nil {
		t.Fatal(err)
	}
	ngRes, err := nonGitFS.ExecGit(ctx, GitRequest{Operation: GitWorktreeList, Workspace: nonGit})
	if err != nil {
		t.Fatalf("non-git worktree list should not fail: %v", err)
	}
	var ng WireCheck
	if err := json.Unmarshal(ngRes.Stdout, &ng); err != nil || ng.IsGitRepo {
		t.Fatalf("expected is_git_repo=false, got %s", string(ngRes.Stdout))
	}
}

type WireCheck struct {
	IsGitRepo bool `json:"is_git_repo"`
}
