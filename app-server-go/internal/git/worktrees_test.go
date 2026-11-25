package git

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/targetfs"
)

func TestGitWorktreeEndpointsEndToEnd(t *testing.T) {
	root := initRepository(t)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, root, "add", ".gitignore")
	runGitCommand(t, root, "-c", "user.name=Astrorder Test", "-c", "user.email=test@invalid", "commit", "-qm", "ignore env")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pub := &eventRecorder{}
	handler := testGitHandler(root, pub)

	// 1. GET /api/v1/git/worktrees
	resp := gitCall(t, handler, http.MethodGet, "/api/v1/git/worktrees?workspace="+queryEscape(root), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list worktrees: %d %s", resp.Code, resp.Body.String())
	}
	var listed targetfs.WorktreeListWire
	if err := json.Unmarshal(resp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if !listed.IsGitRepo || len(listed.Worktrees) != 1 || len(listed.CopyableFiles) == 0 {
		t.Fatalf("unexpected list response: %+v", listed)
	}

	// 2. POST /api/v1/git/worktrees
	createBody := `{"workspace":` + quote(root) + `,"branch":"feat/http-wt","layout":"sibling","copy_files":[".env"]}`
	resp = gitCall(t, handler, http.MethodPost, "/api/v1/git/worktrees", strings.NewReader(createBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("create worktree: %d %s", resp.Code, resp.Body.String())
	}
	var created targetfs.WorktreeAddWire
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Branch != "feat/http-wt" || len(created.CopiedFiles) != 1 || created.CopiedFiles[0] != ".env" {
		t.Fatalf("unexpected create result: %+v", created)
	}

	// 3. POST /api/v1/git/worktrees/bootstrap
	bootBody := `{"workspace":` + quote(root) + `,"target_path":` + quote(created.Path) + `,"copy_files":[".env"]}`
	resp = gitCall(t, handler, http.MethodPost, "/api/v1/git/worktrees/bootstrap", strings.NewReader(bootBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("bootstrap worktree: %d %s", resp.Code, resp.Body.String())
	}

	// 4. Commit and POST /api/v1/git/worktrees/merge
	if err := os.WriteFile(filepath.Join(created.Path, "hello.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, created.Path, "add", "hello.txt")
	runGitCommand(t, created.Path, "-c", "user.name=Astrorder Test", "-c", "user.email=test@invalid", "commit", "-qm", "add hello")

	mergeBody := `{"workspace":` + quote(root) + `,"source_branch":"feat/http-wt","strategy":"merge"}`
	resp = gitCall(t, handler, http.MethodPost, "/api/v1/git/worktrees/merge", strings.NewReader(mergeBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("merge worktree: %d %s", resp.Code, resp.Body.String())
	}
	var merged targetfs.WorktreeMergeWire
	if err := json.Unmarshal(resp.Body.Bytes(), &merged); err != nil || !merged.Merged {
		t.Fatalf("expected merged=true, got err=%v wire=%+v", err, merged)
	}

	// 5. POST /api/v1/git/worktrees/remove
	rmBody := `{"workspace":` + quote(root) + `,"target_path":` + quote(created.Path) + `,"delete_branch":true}`
	resp = gitCall(t, handler, http.MethodPost, "/api/v1/git/worktrees/remove", strings.NewReader(rmBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("remove worktree: %d %s", resp.Code, resp.Body.String())
	}
	var removed targetfs.WorktreeRemoveWire
	if err := json.Unmarshal(resp.Body.Bytes(), &removed); err != nil || !removed.Removed || !removed.BranchDeleted {
		t.Fatalf("expected removed & branch deleted, got %+v", removed)
	}

	// 6. POST /api/v1/git/worktrees/prune
	pruneBody := `{"workspace":` + quote(root) + `}`
	resp = gitCall(t, handler, http.MethodPost, "/api/v1/git/worktrees/prune", strings.NewReader(pruneBody))
	if resp.Code != http.StatusOK {
		t.Fatalf("prune worktree: %d %s", resp.Code, resp.Body.String())
	}
}
