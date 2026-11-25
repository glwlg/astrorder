package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/targetfs"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func initSessionWorktreeRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "sample-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Astrorder Test")
	runGit("config", "user.email", "test@astrorder.local")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".gitignore", "README.md")
	runGit("commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("KEY=val\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestCreateSessionWithWorktreeReuseRollbackAndDeleteCleanup(t *testing.T) {
	repo := initSessionWorktreeRepo(t)
	var lastCwd string
	shouldFailCreate := false
	seq := 0
	srv := &mockDaemonServer{
		createCb: func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
			if shouldFailCreate {
				return nil, errors.New("simulated daemon failure")
			}
			seq++
			if agy := r.GetAntigravity(); agy != nil {
				lastCwd = agy.GetCwd()
			} else if codex := r.GetCodex(); codex != nil {
				lastCwd = codex.GetCwd()
			}
			return &pb.CommandReceipt{
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Session: &pb.SessionRef{
					SessionId:    "wt-sess-" + string(rune('0'+seq)),
					AgentId:      r.Target.AgentId,
					ConnectionId: r.Target.ConnectionId,
				},
				Status:  pb.SessionStatus_SESSION_STATUS_IDLE,
				Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
			}, nil
		},
		deleteCb: func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
			return &pb.CommandReceipt{
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Session:        r.Session,
				Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
			}, nil
		},
	}

	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	h.resolver = targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            st,
		DefaultWorkspace: repo,
		AllowedRoots:     []string{repo},
	})

	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agy", "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "codex", "kind": "codex", "name": "Codex", "status": "ready"})

	// 1. Daemon failure rolls back newly created worktree.
	shouldFailCreate = true
	failPayload, _ := json.Marshal(map[string]any{
		"agent_id":  "agy",
		"workspace": repo,
		"title":     "Will Fail",
		"worktree": map[string]any{
			"mode":       "create",
			"branch":     "feat/rollback-me",
			"copy_files": []string{".env"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(string(failPayload)))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected failure when daemon errors")
	}
	rolledBackPath := filepath.Join(repo, ".worktrees", "feat", "rollback-me")
	if _, err := os.Stat(rolledBackPath); !os.IsNotExist(err) {
		t.Fatalf("expected failed create to rollback worktree %s, err=%v", rolledBackPath, err)
	}

	// 2. Create session with new sibling worktree + .env copy.
	shouldFailCreate = false
	createPayload, _ := json.Marshal(map[string]any{
		"agent_id":  "agy",
		"workspace": repo,
		"title":     "Parallel Task",
		"worktree": map[string]any{
			"mode":       "create",
			"branch":     "feat/session-wt",
			"layout":     "sibling",
			"copy_files": []string{".env"},
		},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(string(createPayload)))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create with worktree failed: %d %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	wtPath, _ := created["workspace"].(string)
	if wtPath == "" || wtPath == repo || lastCwd != wtPath {
		t.Fatalf("expected session cwd set to worktree path, got workspace=%q lastCwd=%q", wtPath, lastCwd)
	}
	if created["worktree_branch"] != "feat/session-wt" || created["worktree_managed"] != true {
		t.Fatalf("expected worktree metadata persisted, got %+v", created)
	}
	if _, err := os.Stat(filepath.Join(wtPath, ".env")); err != nil {
		t.Fatalf("expected .env copied into session worktree: %v", err)
	}

	// 3. Create a second session reusing the existing worktree.
	reusePayload, _ := json.Marshal(map[string]any{
		"agent_id":  "codex",
		"workspace": repo,
		"title":     "Reuse WT",
		"worktree": map[string]any{
			"mode": "existing",
			"path": wtPath,
		},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(string(reusePayload)))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reuse worktree failed: %d %s", rec.Code, rec.Body.String())
	}
	var reused map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &reused)
	if reused["worktree_managed"] != false || reused["worktree_branch"] != "feat/session-wt" {
		t.Fatalf("expected reused worktree metadata, got %+v", reused)
	}

	// 4. Deleting first session with remove_worktree=1 must keep worktree while second session still uses it.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+created["id"].(string)+"?agent_id=agy&remove_worktree=1", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete first session: %d %s", rec.Code, rec.Body.String())
	}
	var del1 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &del1)
	if del1["worktree_removed"] != false {
		t.Fatalf("expected worktree preserved while another session references it: %+v", del1)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree should still exist on disk: %v", err)
	}

	// 5. Deleting last session with remove_worktree=1&delete_worktree_branch=1 cleans up disk and branch.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+reused["id"].(string)+"?agent_id=codex&remove_worktree=1&delete_worktree_branch=1", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete second session: %d %s", rec.Code, rec.Body.String())
	}
	var del2 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &del2)
	if del2["worktree_removed"] != true || del2["worktree_branch_deleted"] != true {
		t.Fatalf("expected worktree & branch removed on last session delete: %+v", del2)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("worktree directory should be removed: err=%v", err)
	}
}

func TestCodexForkAndAntigravityContextBranchWithWorktree(t *testing.T) {
	repo := initSessionWorktreeRepo(t)
	var codexForkCwd, agyBranchCwd string
	srv := &mockDaemonServer{
		createCb: func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
			if c := r.GetCodex(); c != nil {
				codexForkCwd = c.GetCwd()
				return &pb.CommandReceipt{
					OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
					Session:        &pb.SessionRef{SessionId: "codex-child", AgentId: r.Target.AgentId},
					Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
					Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
				}, nil
			}
			if a := r.GetAntigravity(); a != nil {
				agyBranchCwd = a.GetCwd()
				return &pb.CommandReceipt{
					OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
					Session:        &pb.SessionRef{SessionId: "agy-child", AgentId: r.Target.AgentId},
					Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
					Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
				}, nil
			}
			return nil, errors.New("unexpected runtime")
		},
	}
	rt := &contextBranchMockRuntime{
		pages: []*pb.HistoryPageResponse{{
			Session: &pb.SessionRef{SessionId: "agy-parent", AgentId: optString("agy")},
			Items: []*pb.NativeJson{
				{Schema: "antigravity.message.v1", Utf8Json: []byte(`{"id":"antigravity:agy-parent:1","role":"user","kind":"message","text":"hello"}`)},
			},
		}},
	}

	h, st, _, cleanup := setupTestServerWithCustomRuntime(t, srv, rt)
	defer cleanup()
	h.resolver = targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            st,
		DefaultWorkspace: repo,
		AllowedRoots:     []string{repo},
	})
	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "codex", "kind": "codex", "name": "Codex", "status": "ready"})
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agy", "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "codex-parent", "agent_id": "codex", "title": "Codex P", "workspace": repo, "status": "idle", "control_state": "owned", "history_state": "available", "updated_at": "2026-11-25T00:00:00Z"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "agy-parent", "agent_id": "agy", "title": "AGY P", "workspace": repo, "status": "idle", "control_state": "owned", "history_state": "available", "updated_at": "2026-11-25T00:00:00Z"})

	// 1. Codex native fork with worktree
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/codex-parent/fork", strings.NewReader(`{"agent_id":"codex","create_worktree":true,"worktree_branch":"feat/codex-fork"}`))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("codex fork worktree failed: %d %s", rec.Code, rec.Body.String())
	}
	if codexForkCwd == "" || codexForkCwd == repo {
		t.Fatalf("expected codex fork cwd in worktree, got %q", codexForkCwd)
	}

	// 2. Antigravity context-branch with worktree
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/agy-parent/context-branch", strings.NewReader(`{"agent_id":"agy","create_worktree":true,"worktree_branch":"feat/agy-wt"}`))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("agy context-branch worktree failed: %d %s", rec.Code, rec.Body.String())
	}
	if agyBranchCwd == "" || agyBranchCwd == repo {
		t.Fatalf("expected agy context-branch cwd in worktree, got %q", agyBranchCwd)
	}
}
