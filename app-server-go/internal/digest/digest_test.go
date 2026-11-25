package digest

import (
	"bytes"
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
)

type allowBrowser struct{}

func (allowBrowser) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func initTempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Tester",
			"GIT_AUTHOR_EMAIL=tester@example.com",
			"GIT_COMMITTER_NAME=Tester",
			"GIT_COMMITTER_EMAIL=tester@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Demo\nhello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "feat: add office suite export")
	return dir
}

func TestCollectAggregatesMultiSourceEvidence(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	repoDir := initTempGitRepo(t)
	auditDir := t.TempDir()

	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)

	// Seed project, session, messages, task, checkpoint
	if _, err := st.UpsertProject(ctx, map[string]any{
		"source_id":     "local",
		"project_id":    "proj-1",
		"project_name":  "Astrorder",
		"workspace":     repoDir,
		"session_count": 1,
		"updated_at":    ts,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO sessions (id, agent_id, title, workspace, status, project_id, project_name, history_state, control_state, updated_at)
		VALUES ('sess-1', 'codex', '实现星序办公套件', ?, 'idle', 'proj-1', 'Astrorder', 'local', 'attached', ?)`,
		repoDir, ts)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	_, err = st.DB().ExecContext(ctx, `
		INSERT INTO messages (id, session_id, agent_id, role, kind, text, attachments, created_at)
		VALUES ('m1', 'sess-1', 'codex', 'user', 'text', '帮我实现 Word/Excel 导出和周报工作台', '[]', ?),
		       ('m2', 'sess-1', 'codex', 'assistant', 'text', '已完成纯 Go OOXML 导出引擎并通过单测验证。', '[]', ?)`,
		ts, ts)
	if err != nil {
		t.Fatalf("insert messages: %v", err)
	}
	if _, err := st.UpsertTask(ctx, map[string]any{
		"id":         "task-tool",
		"agent_id":   "codex",
		"session_id": "sess-1",
		"kind":       "tool",
		"title":      "git -C /tmp add -A",
		"status":     "completed",
		"created_at": ts,
		"updated_at": ts,
	}); err != nil {
		t.Fatalf("upsert tool task: %v", err)
	}
	if _, err := st.UpsertTask(ctx, map[string]any{
		"id":         "task-subagent",
		"agent_id":   "codex",
		"session_id": "sess-1",
		"kind":       "subagent",
		"title":      "子代理排查任务",
		"status":     "completed",
		"created_at": ts,
		"updated_at": ts,
	}); err != nil {
		t.Fatalf("upsert subagent task: %v", err)
	}
	if _, err := st.InsertSessionCheckpoint(ctx, store.SessionCheckpoint{
		ID:           "cp-1",
		AgentID:      "codex",
		SessionID:    "sess-1",
		Kind:         "auto_after",
		Label:        "完成 office 导出引擎",
		Workspace:    repoDir,
		RepoRoot:     repoDir,
		Branch:       "master",
		FilesChanged: 6,
		Insertions:   420,
		Deletions:    12,
		CreatedAt:    ts,
	}); err != nil {
		t.Fatalf("insert checkpoint: %v", err)
	}

	// Write SQL audit line
	auditEntry := map[string]any{
		"timestamp":       ts,
		"session_id":      "sess-1",
		"connection_id":   "conn-1",
		"connection_name": "ops@prod",
		"database":        "ops_metadata",
		"is_prod":         true,
		"source_name":     "007_audit.sql",
		"risk_level":      "read_only",
		"statement_count": 1,
		"ok":              true,
		"elapsed_ms":      38,
		"sql_hash":        "abc123",
	}
	auditLine, _ := json.Marshal(auditEntry)
	if err := os.WriteFile(filepath.Join(auditDir, "dbx-sql-audit.jsonl"), append(auditLine, '\n'), 0o600); err != nil {
		t.Fatalf("write audit: %v", err)
	}

	h := NewHandler(Options{
		Store:      st,
		Authorizer: allowBrowser{},
		AuditDir:   auditDir,
		HistoryReader: func(_ context.Context, agentID, sessionID string, _ int) ([]map[string]any, error) {
			if agentID == "codex" && sessionID == "sess-1" {
				return []map[string]any{
					{"role": "user", "kind": "message", "text": "帮我实现 Word/Excel 导出和周报工作台", "created_at": ts},
					{"role": "tool", "kind": "tool", "text": "noisy tool output", "created_at": ts},
					{"role": "assistant", "kind": "message", "text": "已完成纯 Go OOXML 导出引擎并通过单测验证。", "created_at": ts},
				}, nil
			}
			return nil, nil
		},
	})

	reqBody, _ := json.Marshal(CollectRequest{
		From: now.Add(-1 * time.Hour).Format(time.RFC3339),
		To:   now.Add(1 * time.Hour).Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, BasePath+"/collect", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("collect failed (%d): %s", rec.Code, rec.Body.String())
	}

	var bundle DigestBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if bundle.Stats.ProjectCount != 1 {
		t.Fatalf("expected 1 project, got %d (%+v)", bundle.Stats.ProjectCount, bundle.Projects)
	}
	// Internal tool / subagent tasks are excluded by default so sessions stay clean
	if bundle.Stats.SessionCount != 1 || bundle.Stats.CommitCount != 1 || bundle.Stats.TaskCount != 0 || bundle.Stats.SQLAuditCount != 1 {
		t.Fatalf("expected session=1 commit=1 task=0 sql=1, got %+v", bundle.Stats)
	}
	foundSummary := false
	for _, it := range bundle.Projects[0].Items {
		if it.Kind == "session" && strings.Contains(it.Summary, "纯 Go OOXML 导出引擎") {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatalf("expected native history summary on session item, got %+v", bundle.Projects[0].Items)
	}
	if bundle.Stats.FilesChanged != 6 || bundle.Stats.Insertions != 420 {
		t.Fatalf("expected checkpoint stats 6/+420, got %+v", bundle.Stats)
	}

	// Add an out-of-range session (updated 3 days ago) and a stale-message session whose updated_at
	// was bumped today without any message in range; neither should appear in today's collect window.
	oldTS := now.Add(-72 * time.Hour).Format(time.RFC3339)
	_, err = st.DB().ExecContext(ctx, `
		INSERT INTO sessions (id, agent_id, title, workspace, status, project_id, project_name, history_state, control_state, updated_at)
		VALUES ('sess-old', 'codex', '旧会话', ?, 'idle', 'proj-old', 'OldProj', 'local', 'attached', ?),
		       ('sess-bumped', 'codex', '仅元信息刷新的会话', ?, 'idle', 'proj-bumped', 'BumpedProj', 'local', 'attached', ?)`,
		t.TempDir(), oldTS, t.TempDir(), ts)
	if err != nil {
		t.Fatalf("insert extra sessions: %v", err)
	}
	_, err = st.DB().ExecContext(ctx, `
		INSERT INTO messages (id, session_id, agent_id, role, kind, text, attachments, created_at)
		VALUES ('m-old', 'sess-bumped', 'codex', 'user', 'message', '三天前的历史消息', '[]', ?)`, oldTS)
	if err != nil {
		t.Fatalf("insert old message: %v", err)
	}

	recFilter := httptest.NewRecorder()
	h.ServeHTTP(recFilter, httptest.NewRequest(http.MethodPost, BasePath+"/collect", bytes.NewReader(reqBody)))
	var filteredBundle DigestBundle
	_ = json.Unmarshal(recFilter.Body.Bytes(), &filteredBundle)
	if filteredBundle.Stats.ProjectCount != 1 || filteredBundle.Stats.SessionCount != 1 {
		t.Fatalf("out-of-range and metadata-only sessions should be excluded, got %+v", filteredBundle.Stats)
	}
}

func TestGenerateLLMAndPromptOnlyFallbackAndCRUD(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "mock-gpt",
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "# 本周工作周报\n\n## 核心成果\n- 完成了办公套件与复盘工作台。",
					},
				},
			},
		})
	}))
	defer mockLLM.Close()

	h := NewHandler(Options{
		Store:      st,
		Authorizer: allowBrowser{},
		HTTPClient: mockLLM.Client(),
	})

	items := []DigestItem{
		{
			ItemID:      "i1",
			Kind:        "session",
			ProjectKey:  "astrorder",
			ProjectName: "Astrorder",
			Title:       "办公能力建设",
			Summary:     "完成导出与复盘功能",
			Selected:    true,
		},
	}

	// 1. Without API key -> falls back cleanly to prompt_only
	genReqBody, _ := json.Marshal(GenerateRequest{
		TemplateID:  "weekly_exec",
		Items:       items,
		ManualNotes: "参加了周三架构评审会",
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, BasePath+"/generate", bytes.NewReader(genReqBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback generate failed (%d): %s", rec.Code, rec.Body.String())
	}
	var fallbackResp GenerateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &fallbackResp)
	if fallbackResp.Mode != "prompt_only" || fallbackResp.LLMConfigured || !strings.Contains(fallbackResp.AssembledPrompt, "周三架构评审会") {
		t.Fatalf("unexpected fallback response: %+v", fallbackResp)
	}

	// 2. Configure helper LLM -> uses mock LLM
	if err := st.SetPreference(ctx, "services:llm", map[string]string{
		"base_url":  mockLLM.URL + "/v1",
		"api_key":   "sk-test-123",
		"model":     "mock-gpt",
		"reasoning": "low",
	}, true); err != nil {
		t.Fatalf("set llm pref: %v", err)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, BasePath+"/generate", bytes.NewReader(genReqBody)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("llm generate failed (%d): %s", rec2.Code, rec2.Body.String())
	}
	var llmResp GenerateResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &llmResp)
	if llmResp.Mode != "llm" || !llmResp.LLMConfigured || !strings.Contains(llmResp.Markdown, "本周工作周报") {
		t.Fatalf("unexpected llm response: %+v", llmResp)
	}

	// 3. History save -> list -> delete
	histBody, _ := json.Marshal(DigestHistoryRecord{
		Title:        "第48周周报",
		TemplateID:   "weekly_exec",
		TemplateName: "周报 · 汇报版",
		Markdown:     llmResp.Markdown,
		ItemCount:    1,
	})
	recSave := httptest.NewRecorder()
	h.ServeHTTP(recSave, httptest.NewRequest(http.MethodPost, BasePath+"/history", bytes.NewReader(histBody)))
	if recSave.Code != http.StatusOK {
		t.Fatalf("save history failed: %d %s", recSave.Code, recSave.Body.String())
	}
	var savedHist DigestHistoryRecord
	_ = json.Unmarshal(recSave.Body.Bytes(), &savedHist)
	if savedHist.ID == "" {
		t.Fatalf("expected generated history ID")
	}

	recList := httptest.NewRecorder()
	h.ServeHTTP(recList, httptest.NewRequest(http.MethodGet, BasePath+"/history", nil))
	if !strings.Contains(recList.Body.String(), savedHist.ID) {
		t.Fatalf("expected saved history in list: %s", recList.Body.String())
	}

	recDel := httptest.NewRecorder()
	h.ServeHTTP(recDel, httptest.NewRequest(http.MethodDelete, BasePath+"/history?id="+savedHist.ID, nil))
	if recDel.Code != http.StatusOK {
		t.Fatalf("delete history failed: %d", recDel.Code)
	}

	// 4. Custom template put -> list -> delete (and reject deleting built-in)
	tplBody, _ := json.Marshal(DigestTemplate{
		Name:         "管理层月报",
		Description:  "月度 KPI 总结",
		Audience:     "总监",
		SystemPrompt: "按 OKR 维度汇总产出",
	})
	recTpl := httptest.NewRecorder()
	h.ServeHTTP(recTpl, httptest.NewRequest(http.MethodPut, BasePath+"/templates", bytes.NewReader(tplBody)))
	if recTpl.Code != http.StatusOK {
		t.Fatalf("put template failed: %d %s", recTpl.Code, recTpl.Body.String())
	}
	var savedTpl DigestTemplate
	_ = json.Unmarshal(recTpl.Body.Bytes(), &savedTpl)
	if savedTpl.ID == "" || savedTpl.BuiltIn {
		t.Fatalf("unexpected saved template: %+v", savedTpl)
	}

	recDelBuiltin := httptest.NewRecorder()
	h.ServeHTTP(recDelBuiltin, httptest.NewRequest(http.MethodDelete, BasePath+"/templates?id=weekly_exec", nil))
	if recDelBuiltin.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when deleting builtin template, got %d", recDelBuiltin.Code)
	}

	recDelCustom := httptest.NewRecorder()
	h.ServeHTTP(recDelCustom, httptest.NewRequest(http.MethodDelete, BasePath+"/templates?id="+savedTpl.ID, nil))
	if recDelCustom.Code != http.StatusOK {
		t.Fatalf("delete custom template failed: %d", recDelCustom.Code)
	}
}
