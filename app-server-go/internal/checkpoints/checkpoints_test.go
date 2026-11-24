package checkpoints

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

type allowAllBrowser struct{}

func (allowAllBrowser) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

type memSessionStore struct {
	*store.Store
	session map[string]any
}

func (m memSessionStore) FindSessionByID(context.Context, string) (map[string]any, error) {
	return m.session, nil
}
func (m memSessionStore) GetSSHConnection(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func initGitWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@local",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@local",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n// v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "main.go")
	run("commit", "-m", "init")
	return dir
}

func TestCheckpointsHTTPCreateDiffRestoreDelete(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	repo := initGitWorkspace(t)
	sessionRecord := map[string]any{
		"id":            "sess-1",
		"agent_id":      "agent-1",
		"title":         "测试会话",
		"workspace":     repo,
		"connection_id": "local",
	}
	adapter := memSessionStore{Store: st, session: sessionRecord}
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            adapter,
		DefaultWorkspace: repo,
		AllowedRoots:     []string{repo},
	})
	svc := NewService(Options{
		Store:      adapter,
		Authorizer: allowAllBrowser{},
		Resolver:   resolver,
	})
	defer svc.Close()

	// Create checkpoint #1 (v1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/checkpoints", strings.NewReader(`{"agent_id":"agent-1","label":"第1轮完成"}`))
	svc.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("create #1 failed: %d %s", rec.Code, rec.Body.String())
	}
	var create1 struct {
		Checkpoint store.SessionCheckpoint `json:"checkpoint"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &create1); err != nil {
		t.Fatal(err)
	}

	// Modify file and create checkpoint #2 (v2)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n// v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/checkpoints", strings.NewReader(`{"agent_id":"agent-1","label":"第2轮完成"}`))
	svc.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("create #2 failed: %d %s", rec.Code, rec.Body.String())
	}
	var create2 struct {
		Checkpoint store.SessionCheckpoint `json:"checkpoint"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &create2); err != nil {
		t.Fatal(err)
	}
	if create2.Checkpoint.FilesChanged != 1 || create2.Checkpoint.TurnNumber != 2 {
		t.Fatalf("unexpected cp2: %+v", create2.Checkpoint)
	}

	// Diff checkpoint #1 -> #2
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/checkpoints/diff?agent_id=agent-1&from="+create1.Checkpoint.TreeSHA+"&to="+create2.Checkpoint.TreeSHA, nil)
	svc.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "// v2") {
		t.Fatalf("diff failed: %d %s", rec.Code, rec.Body.String())
	}

	// Restore back to checkpoint #1 -> auto-creates pre_restore backup #3
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/checkpoints/"+create1.Checkpoint.ID+"/restore", strings.NewReader(`{"agent_id":"agent-1"}`))
	svc.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("restore failed: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	if !strings.Contains(string(got), "// v1") {
		t.Fatalf("file not restored to v1: %q", string(got))
	}

	// List checkpoints -> should now contain 3 items (newest first: pre_restore, turn 2, turn 1)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/checkpoints?agent_id=agent-1", nil)
	svc.ServeHTTP(rec, req)
	var listResp struct {
		Items []store.SessionCheckpoint `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil || len(listResp.Items) != 3 || listResp.Items[0].Kind != "pre_restore" {
		t.Fatalf("unexpected list: %d %s", rec.Code, rec.Body.String())
	}

	// Delete checkpoint #2
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/sess-1/checkpoints/"+create2.Checkpoint.ID+"?agent_id=agent-1", nil)
	svc.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("delete failed: %d %s", rec.Code, rec.Body.String())
	}
}

type fakeSubscriber struct {
	ch chan *store.Event
}

func (f *fakeSubscriber) Subscribe() (<-chan *store.Event, func()) {
	return f.ch, func() {}
}

func TestCheckpointsAutoCaptureOnSessionUpsertAndCodexTurnCompleted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "auto.sqlite3")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	repo := initGitWorkspace(t)
	adapter := memSessionStore{
		Store: st,
		session: map[string]any{
			"id":            "sess-auto",
			"agent_id":      "agent-auto",
			"title":         "自动捕获测试",
			"workspace":     repo,
			"connection_id": "local",
		},
	}
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            adapter,
		DefaultWorkspace: repo,
		AllowedRoots:     []string{repo},
	})
	sub := &fakeSubscriber{ch: make(chan *store.Event, 8)}
	svc := NewService(Options{
		Store:      adapter,
		Authorizer: allowAllBrowser{},
		Resolver:   resolver,
		Subscriber: sub,
	})
	defer svc.Close()

	waitForCount := func(want int) []store.SessionCheckpoint {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			items, err := st.ListSessionCheckpoints(context.Background(), "agent-auto", "sess-auto", 20)
			if err == nil && len(items) >= want {
				return items
			}
			time.Sleep(30 * time.Millisecond)
		}
		items, _ := st.ListSessionCheckpoints(context.Background(), "agent-auto", "sess-auto", 20)
		t.Fatalf("timed out waiting for %d checkpoints, got %d", want, len(items))
		return nil
	}

	// Native session starts running via session.upsert -> pending baseline is held
	// in memory (not persisted to DB until this turn actually modifies files).
	sub.ch <- &store.Event{Type: "session.upsert", AgentID: "agent-auto", SessionID: "sess-auto", Data: map[string]any{"status": "running"}}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		ready := svc.pendingBaselines["agent-auto::sess-auto"] != nil
		svc.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	beforeItems, _ := st.ListSessionCheckpoints(context.Background(), "agent-auto", "sess-auto", 20)
	if len(beforeItems) != 0 {
		t.Fatalf("expected 0 DB checkpoints before turn mutates workspace, got %d", len(beforeItems))
	}

	// Modify file, then complete via codex.notification turn/completed -> both
	// clean auto_before anchor (0 diff) and auto_after (1 file) are persisted.
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n// codex turn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub.ch <- &store.Event{
		Type:      "codex.notification",
		AgentID:   "agent-auto",
		SessionID: "sess-auto",
		Data:      map[string]any{"frame": map[string]any{"method": "turn/completed"}},
	}
	items := waitForCount(2)
	if items[0].Kind != "auto_after" || items[0].FilesChanged != 1 {
		t.Fatalf("expected auto_after with 1 changed file, got %+v", items[0])
	}
	if items[1].Kind != "auto_before" || items[1].FilesChanged != 0 || items[1].PrevTreeSHA != "" {
		t.Fatalf("expected clean auto_before anchor with 0 diff, got %+v", items[1])
	}
}

func TestCheckpointsIgnoreExternalChangesAndOtherProjectTurns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "isolation.sqlite3")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	repo := initGitWorkspace(t)
	otherProject := t.TempDir()
	adapter := memSessionStore{
		Store: st,
		session: map[string]any{
			"id":            "sess-chat",
			"agent_id":      "agent-agy",
			"title":         "宣传片制作",
			"workspace":     repo,
			"connection_id": "local",
		},
	}
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            adapter,
		DefaultWorkspace: repo,
		AllowedRoots:     []string{repo},
	})
	sub := &fakeSubscriber{ch: make(chan *store.Event, 16)}
	svc := NewService(Options{
		Store:      adapter,
		Authorizer: allowAllBrowser{},
		Resolver:   resolver,
		Subscriber: sub,
	})
	defer svc.Close()

	waitBaseline := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			svc.mu.Lock()
			ready := svc.pendingBaselines["agent-agy::sess-chat"] != nil
			svc.mu.Unlock()
			if ready {
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
		t.Fatal("timed out waiting for pending baseline")
	}
	waitTurnSettled := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			svc.mu.Lock()
			running := svc.wasRunning["agent-agy::sess-chat"]
			pending := svc.pendingBaselines["agent-agy::sess-chat"] != nil
			svc.mu.Unlock()
			if !running && !pending {
				time.Sleep(120 * time.Millisecond)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Seed an old manual checkpoint from "13 hours ago"
	if _, _, err := svc.Capture(context.Background(), "agent-agy", "sess-chat", "manual", "", "旧快照", false); err != nil {
		t.Fatal(err)
	}

	// Another session modifies many files in repo between turns!
	if err := os.WriteFile(filepath.Join(repo, "external_1.go"), []byte("package main\n// from other session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "external_2.go"), []byte("package main\n// from other session\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Turn 1: pure discussion turn (assistant message only, zero tools) while
	// ANOTHER session ALSO edits repo concurrently during the turn!
	sub.ch <- &store.Event{Type: "command.upsert", AgentID: "agent-agy", SessionID: "sess-chat", Data: map[string]any{"id": "cmd-1", "action": "send", "state": "running", "text": "讨论方案"}}
	waitBaseline()
	sub.ch <- &store.Event{Type: "message.upsert", AgentID: "agent-agy", SessionID: "sess-chat", Data: map[string]any{"id": "m1", "role": "assistant", "kind": "message", "text": "方案一和方案二"}}
	// Concurrent edit from another session during Turn 1
	if err := os.WriteFile(filepath.Join(repo, "concurrent_other.go"), []byte("package main\n// concurrent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub.ch <- &store.Event{Type: "command.upsert", AgentID: "agent-agy", SessionID: "sess-chat", Data: map[string]any{"id": "cmd-1", "action": "send", "state": "completed", "text": "讨论方案"}}
	waitTurnSettled()

	items, err := st.ListSessionCheckpoints(context.Background(), "agent-agy", "sess-chat", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected pure discussion turn to create 0 checkpoints even with external/concurrent edits, got %d: %+v", len(items), items)
	}

	// Turn 2: edits a file in ANOTHER project directory outside repo while another
	// session concurrently edits repo -> must STILL create 0 checkpoints!
	sub.ch <- &store.Event{Type: "command.upsert", AgentID: "agent-agy", SessionID: "sess-chat", Data: map[string]any{"id": "cmd-2", "action": "send", "state": "running", "text": "改另一个项目"}}
	waitBaseline()
	sub.ch <- &store.Event{Type: "message.upsert", AgentID: "agent-agy", SessionID: "sess-chat", Data: map[string]any{
		"id":   "tool-other-proj",
		"role": "tool",
		"kind": "tool",
		"tool": map[string]any{
			"name":      "write_file",
			"arguments": map[string]any{"Path": filepath.Join(otherProject, "video.py")},
		},
	}}
	if err := os.WriteFile(filepath.Join(repo, "concurrent_other_2.go"), []byte("package main\n// concurrent 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub.ch <- &store.Event{Type: "command.upsert", AgentID: "agent-agy", SessionID: "sess-chat", Data: map[string]any{"id": "cmd-2", "action": "send", "state": "completed", "text": "改另一个项目"}}
	waitTurnSettled()

	items, err = st.ListSessionCheckpoints(context.Background(), "agent-agy", "sess-chat", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected out-of-workspace edit turn to create 0 checkpoints, got %d: %+v", len(items), items)
	}

	// Turn 3: genuinely edits a file INSIDE repo -> creates clean auto_before (0 diff)
	// and auto_after with ONLY the 1 file edited in Turn 3 (NOT the earlier external files)!
	sub.ch <- &store.Event{Type: "command.upsert", AgentID: "agent-agy", SessionID: "sess-chat", Data: map[string]any{"id": "cmd-3", "action": "send", "state": "running", "text": "修改本仓 main.go"}}
	waitBaseline()
	sub.ch <- &store.Event{Type: "message.upsert", AgentID: "agent-agy", SessionID: "sess-chat", Data: map[string]any{
		"id":   "tool-same-repo",
		"role": "tool",
		"kind": "tool",
		"tool": map[string]any{
			"name":      "edit_file",
			"arguments": map[string]any{"Path": filepath.Join(repo, "main.go")},
		},
	}}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n// turn 3 own edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub.ch <- &store.Event{Type: "command.upsert", AgentID: "agent-agy", SessionID: "sess-chat", Data: map[string]any{"id": "cmd-3", "action": "send", "state": "completed", "text": "修改本仓 main.go"}}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		items, _ = st.ListSessionCheckpoints(context.Background(), "agent-agy", "sess-chat", 20)
		if len(items) >= 3 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(items) != 3 {
		t.Fatalf("expected 1 old manual + 1 auto_before + 1 auto_after = 3, got %d: %+v", len(items), items)
	}
	if items[0].Kind != "auto_after" || items[0].FilesChanged != 1 || len(items[0].Files) != 1 || items[0].Files[0].Path != "main.go" {
		t.Fatalf("expected auto_after with ONLY main.go changed (not external files), got %+v", items[0])
	}
	if items[1].Kind != "auto_before" || items[1].FilesChanged != 0 || items[1].PrevTreeSHA != "" {
		t.Fatalf("expected clean auto_before anchor with 0 files_changed, got %+v", items[1])
	}
}

func TestCheckpointsSingleFileRestoreAndAutoRetentionGC(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gc.sqlite3")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	repo := initGitWorkspace(t)
	if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("keep-v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := memSessionStore{
		Store: st,
		session: map[string]any{
			"id":            "sess-gc",
			"agent_id":      "agent-gc",
			"title":         "单文件回滚与清理测试",
			"workspace":     repo,
			"connection_id": "local",
		},
	}
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            adapter,
		DefaultWorkspace: repo,
		AllowedRoots:     []string{repo},
	})
	svc := NewService(Options{
		Store:             adapter,
		Authorizer:        allowAllBrowser{},
		Resolver:          resolver,
		MaxAutoPerSession: 2,
	})
	defer svc.Close()

	// Manual snapshot #1 (must NEVER be pruned!)
	cpManual, _, err := svc.Capture(context.Background(), "agent-gc", "sess-gc", "manual", "", "手动里程碑", false)
	if err != nil {
		t.Fatal(err)
	}

	// Capture 3 auto snapshots -> with MaxAutoPerSession=2, oldest auto must be pruned while manual stays!
	for i, ver := range []string{"v2", "v3", "v4"} {
		if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n// "+ver+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("keep-v4\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "extra.txt"), []byte("extra\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := svc.Capture(context.Background(), "agent-gc", "sess-gc", "auto_after", "", ver, false); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := st.ListSessionCheckpoints(context.Background(), "agent-gc", "sess-gc", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("expected 1 manual + 2 retained auto = 3 checkpoints, got %d: %+v", len(listed), listed)
	}
	hasManual := false
	for _, item := range listed {
		if item.ID == cpManual.ID {
			hasManual = true
		}
	}
	if !hasManual {
		t.Fatalf("manual checkpoint was erroneously pruned: %+v", listed)
	}

	// Single-file restore: revert ONLY main.go back to cpManual (v1), keeping keep.txt at keep-v4!
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-gc/checkpoints/"+cpManual.ID+"/restore", strings.NewReader(`{"agent_id":"agent-gc","path":"main.go"}`))
	svc.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("single-file restore failed: %d %s", rec.Code, rec.Body.String())
	}
	mainContent, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	keepContent, _ := os.ReadFile(filepath.Join(repo, "keep.txt"))
	if !strings.Contains(string(mainContent), "// v1") {
		t.Fatalf("main.go was not restored to v1: %q", string(mainContent))
	}
	if strings.TrimSpace(string(keepContent)) != "keep-v4" {
		t.Fatalf("keep.txt should stay keep-v4 during single-file restore, got %q", string(keepContent))
	}

	// Single-file restore of newly added extra.txt (absent in cpManual) -> removes ONLY extra.txt!
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-gc/checkpoints/"+cpManual.ID+"/restore", strings.NewReader(`{"agent_id":"agent-gc","path":"extra.txt"}`))
	svc.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("single-file remove restore failed: %d %s", rec.Code, rec.Body.String())
	}
	if _, statErr := os.Stat(filepath.Join(repo, "extra.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("extra.txt should have been removed by single-file restore, err=%v", statErr)
	}
	keepContent2, _ := os.ReadFile(filepath.Join(repo, "keep.txt"))
	if strings.TrimSpace(string(keepContent2)) != "keep-v4" {
		t.Fatalf("keep.txt should still stay keep-v4, got %q", string(keepContent2))
	}
}

func TestCheckpointsPluginPolicyAndConfigurableRetention(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "plugin-policy.sqlite3")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	repo := initGitWorkspace(t)
	adapter := memSessionStore{
		Store: st,
		session: map[string]any{
			"id":            "sess-policy",
			"agent_id":      "agent-policy",
			"title":         "插件策略测试",
			"workspace":     repo,
			"connection_id": "local",
		},
	}
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            adapter,
		DefaultWorkspace: repo,
		AllowedRoots:     []string{repo},
	})
	svc := NewService(Options{
		Store:      adapter,
		Authorizer: allowAllBrowser{},
		Resolver:   resolver,
	})
	defer svc.Close()

	// Default policy: enabled=true, autoCapture=true, maxAuto=20
	enabled, autoCapture, maxAuto := svc.resolveRuntimePolicy(context.Background())
	if !enabled || !autoCapture || maxAuto != 20 {
		t.Fatalf("unexpected default policy: enabled=%v autoCapture=%v maxAuto=%d", enabled, autoCapture, maxAuto)
	}

	// Configure plugin maxAutoSnapshots=3 dynamically in workspace_preferences
	if err := st.SetPreference(context.Background(), prefPluginsConfig, map[string]any{
		pluginID: map[string]any{
			configKeyMaxAuto:     3,
			configKeyAutoCapture: true,
		},
	}, true); err != nil {
		t.Fatal(err)
	}
	for _, ver := range []string{"p1", "p2", "p3", "p4", "p5"} {
		if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n// "+ver+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.Capture(context.Background(), "agent-policy", "sess-policy", "auto_after", "", ver, false); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := st.ListSessionCheckpoints(context.Background(), "agent-policy", "sess-policy", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("expected dynamic plugin retention of 3 auto checkpoints, got %d", len(listed))
	}

	// Disable plugin -> captureBackground must skip capturing new checkpoints
	if err := st.SetPreference(context.Background(), prefPluginsEnabled, map[string]any{
		pluginID: false,
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n// disabled-turn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.captureBackground("agent-policy", "sess-policy", "auto_after", "", "should-skip")
	listedAfterDisable, err := st.ListSessionCheckpoints(context.Background(), "agent-policy", "sess-policy", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedAfterDisable) != 3 || listedAfterDisable[0].Label == "should-skip" {
		t.Fatalf("expected disabled plugin to skip auto capture, got %+v", listedAfterDisable)
	}

	// Re-enable plugin but disable autoCapture -> captureBackground still skips
	if err := st.SetPreference(context.Background(), prefPluginsEnabled, map[string]any{
		pluginID: true,
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(context.Background(), prefPluginsConfig, map[string]any{
		pluginID: map[string]any{
			configKeyMaxAuto:     3,
			configKeyAutoCapture: false,
		},
	}, true); err != nil {
		t.Fatal(err)
	}
	svc.captureBackground("agent-policy", "sess-policy", "auto_after", "", "auto-off-skip")
	listedAfterAutoOff, err := st.ListSessionCheckpoints(context.Background(), "agent-policy", "sess-policy", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedAfterAutoOff) != 3 || listedAfterAutoOff[0].Label == "auto-off-skip" {
		t.Fatalf("expected autoCapture=false to skip auto capture, got %+v", listedAfterAutoOff)
	}
}
