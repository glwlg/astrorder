package bots

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

type allowBrowserAuth struct{}

func (allowBrowserAuth) AuthorizeBrowser(*http.Request) *auth.Error { return nil }

func setupTestService(t *testing.T, spawner SessionSpawner, history SessionHistoryReader) (*Service, *store.Store, string) {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	botWS := filepath.Join(tmp, "video-studio")
	if err := os.MkdirAll(botWS, 0o755); err != nil {
		t.Fatal(err)
	}
	resolver := targetfs.NewResolver(targetfs.ResolverOptions{
		Store:            st,
		DefaultWorkspace: botWS,
		AllowTempDirs:    true,
		AllowedRoots:     []string{tmp},
	})
	svc := NewService(Options{
		Store:         st,
		Authorizer:    allowBrowserAuth{},
		Resolver:      resolver,
		HistoryReader: history,
		Spawner:       spawner,
	})
	return svc, st, tmp
}

func TestBotCRUDDocsAndSkills(t *testing.T) {
	svc, st, tmp := setupTestService(t, nil, nil)
	ctx := context.Background()
	botWS := filepath.Join(tmp, "video-studio")

	// Seed an existing session inside the Bot workspace so stats reflect it.
	_, err := st.UpsertSession(ctx, map[string]any{
		"id":         "sess-1",
		"agent_id":   "antigravity",
		"title":      "宣传片制作",
		"workspace":  botWS,
		"status":     "running",
		"updated_at": "2026-11-26T04:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create Bot via HTTP with AGENTS.md & initial Skill.
	createPayload := map[string]any{
		"name":             "视频制作师",
		"slug":             "video-studio",
		"description":      "2K 宣传片与解说视频全链路制作",
		"icon":             "video",
		"workspace":        botWS,
		"default_agent_id": "antigravity",
		"office_mode":      true,
		"tags":             []string{"视频制作", "宣传片"},
		"write_agents_md":  true,
		"agents_md":        "# 视频制作师\n\n优先消费 inbox 素材，必要时可回溯源项目。",
		"write_skill":      true,
		"skill_slug":       "promo-video-production",
		"skill_md":         "---\nname: promo-video-production\ndescription: End-to-end 2K promo video workflow.\n---\n\n# Steps\n1. Script\n",
	}
	body, _ := json.Marshal(createPayload)
	req := httptest.NewRequest(http.MethodPost, BasePath, bytes.NewReader(body))
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}

	var createResp struct {
		Bot BotSummary `json:"bot"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatal(err)
	}
	if createResp.Bot.Slug != "video-studio" || !createResp.Bot.HasAgentsMD {
		t.Fatalf("unexpected bot summary: %+v", createResp.Bot)
	}
	if createResp.Bot.DefaultApprovalMode != ApprovalModeFullAccess {
		t.Fatalf("expected default approval mode full_access, got %q", createResp.Bot.DefaultApprovalMode)
	}
	if len(createResp.Bot.Skills) != 1 || createResp.Bot.Skills[0].Slug != "promo-video-production" {
		t.Fatalf("expected 1 skill, got %+v", createResp.Bot.Skills)
	}
	if createResp.Bot.SessionCount != 1 || createResp.Bot.RunningCount != 1 {
		t.Fatalf("expected session_count=1 running_count=1, got %d/%d", createResp.Bot.SessionCount, createResp.Bot.RunningCount)
	}

	// Verify files physically written in workspace.
	agentsBytes, err := os.ReadFile(filepath.Join(botWS, "AGENTS.md"))
	if err != nil || !strings.Contains(string(agentsBytes), "视频制作师") {
		t.Fatalf("AGENTS.md missing or unexpected: %v %q", err, string(agentsBytes))
	}
	skillBytes, err := os.ReadFile(filepath.Join(botWS, ".agents", "skills", "promo-video-production", "SKILL.md"))
	if err != nil || !strings.Contains(string(skillBytes), "End-to-end 2K promo video workflow.") {
		t.Fatalf("SKILL.md missing or unexpected: %v %q", err, string(skillBytes))
	}

	// Read docs endpoint.
	reqDocs := httptest.NewRequest(http.MethodGet, BasePath+"/"+createResp.Bot.ID+"/docs", nil)
	wDocs := httptest.NewRecorder()
	svc.ServeHTTP(wDocs, reqDocs)
	if wDocs.Code != http.StatusOK {
		t.Fatalf("docs status = %d, body = %s", wDocs.Code, wDocs.Body.String())
	}
	var docsResp BotDocsResponse
	if err := json.Unmarshal(wDocs.Body.Bytes(), &docsResp); err != nil {
		t.Fatal(err)
	}
	if !docsResp.AgentsMDExists || len(docsResp.Skills) != 1 || !strings.Contains(docsResp.Skills[0].Content, "# Steps") {
		t.Fatalf("unexpected docs response: %+v", docsResp)
	}

	// Delete Bot unregisters only; workspace files MUST remain intact.
	reqDel := httptest.NewRequest(http.MethodDelete, BasePath+"/"+createResp.Bot.ID, nil)
	wDel := httptest.NewRecorder()
	svc.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", wDel.Code, wDel.Body.String())
	}
	if _, err := os.Stat(filepath.Join(botWS, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md must NOT be deleted when Bot is unregistered: %v", err)
	}
}

func TestBotOptimizeAndDistillTemplateFallback(t *testing.T) {
	history := func(_ context.Context, _, _ string, _ int) ([]map[string]any, error) {
		return []map[string]any{
			{"role": "user", "kind": "message", "text": "帮我做一个智能运维平台 2K 宣传片，配音用 Gemini TTS"},
			{"role": "assistant", "kind": "tool", "text": "read_file /home/luwei/.gemini/config/skills/astrorder-browser/SKILL.md"},
			{"role": "assistant", "kind": "message", "text": "已按 promo-video-production 流程完成脚本、旁白生成、GPU 渲染与 -14 LUFS 混音交付。"},
		}, nil
	}
	svc, st, tmp := setupTestService(t, nil, history)
	ctx := context.Background()
	botWS := filepath.Join(tmp, "video-studio")
	skillDir := filepath.Join(botWS, ".agents", "skills", "promo-video-production")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: promo-video-production\ndescription: End-to-end 2K promo video workflow.\n---\n\n# Steps\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := st.UpsertSession(ctx, map[string]any{
		"id":         "s-promo",
		"agent_id":   "antigravity",
		"title":      "智能运维平台宣传片制作",
		"workspace":  botWS,
		"status":     "idle",
		"updated_at": "2026-11-26T04:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Optimize without LLM key -> clean structured template.
	optRes, err := svc.Optimize(ctx, OptimizeRequest{
		Name:         "视频制作师",
		Description:  "专业宣传片制作",
		Instructions: "负责脚本、配音、配乐、2K 渲染与封面",
		Workspace:    botWS,
	})
	if err != nil {
		t.Fatalf("optimize error: %v", err)
	}
	if !optRes.OK || optRes.Mode != "template" || !strings.Contains(optRes.AgentsMD, "inbox/<job-id>/") || !strings.Contains(optRes.AgentsMD, "按需回溯补充") {
		t.Fatalf("unexpected optimize output: %+v", optRes)
	}

	// 2a. Default distill (GenerateSkill omitted/false) -> generates AGENTS.md guiding session/workspace skills, WITHOUT generating SKILL.md.
	distillDefault, err := svc.Distill(ctx, DistillRequest{
		SessionKey: "antigravity::s-promo",
		Hint:       "沉淀成长期可复用的宣传片制作 Bot",
	})
	if err != nil {
		t.Fatalf("distill default error: %v", err)
	}
	if !distillDefault.OK || distillDefault.MessageCount != 3 || distillDefault.Workspace != botWS {
		t.Fatalf("unexpected distill default response: %+v", distillDefault)
	}
	if distillDefault.SkillGenerated || distillDefault.SkillMD != "" {
		t.Fatalf("expected default distill NOT to generate SKILL.md, got generated=%v skill=%q", distillDefault.SkillGenerated, distillDefault.SkillMD)
	}
	if !strings.Contains(distillDefault.AgentsMD, "astrorder-browser") || !strings.Contains(distillDefault.AgentsMD, "promo-video-production") {
		t.Fatalf("expected AGENTS.md to guide session/workspace skills, got:\n%s", distillDefault.AgentsMD)
	}
	if len(distillDefault.UsedSkills) < 2 {
		t.Fatalf("expected detected used skills to include astrorder-browser and promo-video-production, got %+v", distillDefault.UsedSkills)
	}

	// 2b. Opt-in distill (GenerateSkill=true) -> generates AGENTS.md + SKILL.md with frontmatter.
	genSkill := true
	distillWithSkill, err := svc.Distill(ctx, DistillRequest{
		SessionKey:    "antigravity::s-promo",
		Hint:          "沉淀成长期可复用的宣传片制作 Bot",
		GenerateSkill: &genSkill,
	})
	if err != nil {
		t.Fatalf("distill with skill error: %v", err)
	}
	if !distillWithSkill.SkillGenerated || !strings.HasPrefix(distillWithSkill.SkillMD, "---\nname: ") {
		t.Fatalf("distilled skill missing YAML frontmatter: %q", distillWithSkill.SkillMD)
	}
	if !strings.Contains(distillWithSkill.SkillMD, "Gemini TTS") {
		t.Fatalf("expected transcript excerpt in fallback skill: %q", distillWithSkill.SkillMD)
	}
}

func TestBotInvokeStagesAttachmentsAndComposesTraceback(t *testing.T) {
	var captured SpawnRequest
	spawner := func(_ context.Context, req SpawnRequest) (*SpawnResult, error) {
		captured = req
		return &SpawnResult{
			OperationID: req.OperationID,
			Stage:       "completed",
			Session: map[string]any{
				"key":       req.AgentID + "::job-sess-1",
				"id":        "job-sess-1",
				"agent_id":  req.AgentID,
				"title":     req.Title,
				"workspace": req.Workspace,
			},
		}, nil
	}

	svc, _, tmp := setupTestService(t, spawner, nil)
	ctx := context.Background()
	botWS := filepath.Join(tmp, "video-studio")
	callerWS := filepath.Join(tmp, "opscore-project")
	if err := os.MkdirAll(callerWS, 0o755); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(callerWS, "brief.md")
	if err := os.WriteFile(srcFile, []byte("# OpsCore Highlights\n99.99% uptime"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := svc.registry.Create(ctx, CreateBotRequest{
		Name:           "视频制作师",
		Slug:           "video-studio",
		Workspace:      botWS,
		DefaultAgentID: "antigravity",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.Invoke(ctx, InvokeRequest{
		Bot:                "video-studio",
		OperationID:        "op-video-1",
		Title:              "OpsCore v2 宣传片",
		Prompt:             "请基于推送的亮点文档制作 90 秒 2K 宣传片。",
		AttachPaths:        []string{"brief.md"},
		CallerSessionKey:   "antigravity::caller-1",
		CallerWorkspace:    callerWS,
		CallerConnectionID: "local",
	})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if !res.OK || res.Bot.ID != created.ID || len(res.StagedFiles) != 1 {
		t.Fatalf("unexpected invoke result: %+v", res)
	}

	// Verify file copied into <botWS>/inbox/<jobID>/brief.md.
	stagedBytes, err := os.ReadFile(res.StagedFiles[0].TargetPath)
	if err != nil || string(stagedBytes) != "# OpsCore Highlights\n99.99% uptime" {
		t.Fatalf("staged file mismatch: err=%v content=%q", err, string(stagedBytes))
	}
	if !strings.Contains(captured.Prompt, res.StagedFiles[0].TargetPath) {
		t.Fatalf("prompt should list staged file path, got:\n%s", captured.Prompt)
	}
	if !strings.Contains(captured.Prompt, "antigravity::caller-1") || !strings.Contains(captured.Prompt, callerWS) {
		t.Fatalf("prompt should include optional caller traceback context, got:\n%s", captured.Prompt)
	}
	if captured.Workspace != botWS || captured.AgentID != "antigravity" {
		t.Fatalf("unexpected spawn target: workspace=%q agent=%q", captured.Workspace, captured.AgentID)
	}
	if captured.ApprovalMode != ApprovalModeFullAccess {
		t.Fatalf("expected default invoke approval_mode=full_access, got %q", captured.ApprovalMode)
	}
}

func TestParseSkillFrontmatterMultiline(t *testing.T) {
	content := "---\nname: promo-video-production\ndescription: >-\n  End-to-end 2K keynote promo video production workflow\n  with voiceover and broadcast loudness mastering.\ntags: [video]\n---\n\n# Promo Video\n"
	name, desc := parseSkillFrontmatter(content, "promo-video-production")
	if name != "promo-video-production" {
		t.Fatalf("name = %q, want promo-video-production", name)
	}
	want := "End-to-end 2K keynote promo video production workflow with voiceover and broadcast loudness mastering."
	if desc != want {
		t.Fatalf("desc = %q, want %q", desc, want)
	}
}

func TestBotUpdateAndDeletePersistAcrossReads(t *testing.T) {
	svc, _, tmp := setupTestService(t, nil, nil)
	ctx := context.Background()

	// Create Bot #1 and Bot #2 to verify subsequent writes overwrite the registry preference.
	bot1, err := svc.registry.Create(ctx, CreateBotRequest{
		Name:           "视频制作",
		Slug:           "video-bot",
		Workspace:      filepath.Join(tmp, "video"),
		DefaultAgentID: "antigravity",
	})
	if err != nil {
		t.Fatalf("create bot1: %v", err)
	}
	bot2, err := svc.registry.Create(ctx, CreateBotRequest{
		Name:           "文档助理",
		Slug:           "doc-bot",
		Workspace:      filepath.Join(tmp, "docs"),
		DefaultAgentID: "antigravity",
	})
	if err != nil {
		t.Fatalf("create bot2: %v", err)
	}

	// Update Bot #1 via HTTP PATCH (simulating Settings form save).
	patchBody := `{
		"description": "2K 宣传片全链路制作",
		"icon": "video",
		"default_provider": "antigravity",
		"default_model": "gemini-4-neon-xhigh",
		"default_effort": "xhigh",
		"default_approval_mode": "auto",
		"office_mode": true,
		"tags": ["视频", "宣传片"]
	}`
	req := httptest.NewRequest(http.MethodPatch, BasePath+"/"+bot1.ID, strings.NewReader(patchBody))
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", w.Code, w.Body.String())
	}

	// Re-read fresh from SQLite via GET /api/v1/bots/:id (simulating browser refresh).
	getReq := httptest.NewRequest(http.MethodGet, BasePath+"/"+bot1.ID, nil)
	getW := httptest.NewRecorder()
	svc.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getW.Code, getW.Body.String())
	}
	var got struct {
		Bot BotSummary `json:"bot"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Bot.Description != "2K 宣传片全链路制作" ||
		got.Bot.Icon != "video" ||
		got.Bot.DefaultProvider != "antigravity" ||
		got.Bot.DefaultModel != "gemini-4-neon-xhigh" ||
		got.Bot.DefaultEffort != "xhigh" ||
		got.Bot.DefaultApprovalMode != ApprovalModeAuto ||
		!got.Bot.OfficeMode ||
		len(got.Bot.Tags) != 2 {
		t.Fatalf("bot update did not persist across fresh read: %+v", got.Bot)
	}

	// Verify both bots exist, then delete Bot #2 and confirm deletion persists.
	list, err := svc.registry.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 persisted bots, got len=%d err=%v", len(list), err)
	}
	if _, err := svc.registry.Delete(ctx, bot2.ID); err != nil {
		t.Fatalf("delete bot2: %v", err)
	}
	listAfter, err := svc.registry.List(ctx)
	if err != nil || len(listAfter) != 1 || listAfter[0].ID != bot1.ID {
		t.Fatalf("expected deletion to persist, got %+v err=%v", listAfter, err)
	}
}

func TestBotUploadInboxAndCallbackControl(t *testing.T) {
	var captured []SpawnRequest
	spawner := func(_ context.Context, req SpawnRequest) (*SpawnResult, error) {
		captured = append(captured, req)
		return &SpawnResult{
			OperationID: req.OperationID,
			Stage:       "completed",
			Session: map[string]any{
				"key":       req.AgentID + "::job-upload-1",
				"id":        "job-upload-1",
				"agent_id":  req.AgentID,
				"title":     req.Title,
				"workspace": req.Workspace,
			},
		}, nil
	}

	svc, _, tmp := setupTestService(t, spawner, nil)
	ctx := context.Background()
	botWS := filepath.Join(tmp, "video-studio")

	bot, err := svc.registry.Create(ctx, CreateBotRequest{
		Name:           "视频制作",
		Slug:           "video-studio",
		Workspace:      botWS,
		DefaultAgentID: "antigravity",
	})
	if err != nil {
		t.Fatalf("create bot: %v", err)
	}

	// Upload a file via multipart POST /api/v1/bots/{id}/upload
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("files", "product-brief.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("# Product Brief\n2K launch keynote")); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()

	upReq := httptest.NewRequest(http.MethodPost, BasePath+"/"+bot.ID+"/upload", &body)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upW := httptest.NewRecorder()
	svc.ServeHTTP(upW, upReq)
	if upW.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", upW.Code, upW.Body.String())
	}
	var upResp struct {
		OK    bool                `json:"ok"`
		Files []UploadedInboxFile `json:"files"`
	}
	if err := json.Unmarshal(upW.Body.Bytes(), &upResp); err != nil {
		t.Fatal(err)
	}
	if !upResp.OK || len(upResp.Files) != 1 || upResp.Files[0].Name != "product-brief.md" {
		t.Fatalf("unexpected upload response: %+v", upResp)
	}

	// Invoke referencing the uploaded inbox file with caller session (notify_caller defaults to true)
	invRes, err := svc.Invoke(ctx, InvokeRequest{
		Bot:              bot.ID,
		Title:            "上传素材派单",
		Prompt:           "请基于上传的简报制作宣传片",
		AttachPaths:      []string{upResp.Files[0].Path},
		CallerSessionKey: "antigravity::caller-main",
		CallerWorkspace:  filepath.Join(tmp, "other-ws"),
	})
	if err != nil {
		t.Fatalf("invoke with uploaded file failed: %v", err)
	}
	if len(invRes.StagedFiles) != 1 || invRes.StagedFiles[0].Name != "product-brief.md" {
		t.Fatalf("expected uploaded file staged into job inbox, got %+v", invRes.StagedFiles)
	}
	if len(captured) != 1 || !strings.Contains(captured[0].Prompt, "【任务完成自动回告约定】") || !strings.Contains(captured[0].Prompt, "antigravity::caller-main") {
		t.Fatalf("expected auto-callback instruction in prompt, got:\n%s", captured[0].Prompt)
	}

	// Invoke with notify_caller = false -> omits auto-callback instruction
	disableCallback := false
	_, err = svc.Invoke(ctx, InvokeRequest{
		Bot:              bot.ID,
		Prompt:           "静默执行不回告",
		CallerSessionKey: "antigravity::caller-main",
		NotifyCaller:     &disableCallback,
	})
	if err != nil {
		t.Fatalf("invoke silent failed: %v", err)
	}
	if len(captured) != 2 || strings.Contains(captured[1].Prompt, "【任务完成自动回告约定】") {
		t.Fatalf("expected callback block omitted when notify_caller=false, got:\n%s", captured[1].Prompt)
	}
}

func TestBotPresetsNotifySettingAndDocsMetadata(t *testing.T) {
	svc, _, tmp := setupTestService(t, nil, nil)
	ctx := context.Background()
	botWS := filepath.Join(tmp, "video-studio")

	// Seed AGENTS.md referencing both local and external skills, plus auxiliary files inside a local skill folder.
	agentsMD := "# 视频制作 Bot\n\n## 技能调用指引\n- 优先使用 `promo-video-production` 完成 2K 制作\n- 需要浏览器查重时使用 `astrorder-browser` 技能\n- 参考 `/home/luwei/.gemini/config/skills/agently-mail/SKILL.md` 发送通知\n"
	if err := os.WriteFile(filepath.Join(botWS, "AGENTS.md"), []byte(agentsMD), 0o644); err != nil {
		t.Fatal(err)
	}
	skillRoot := filepath.Join(botWS, ".agents", "skills", "promo-video-production")
	if err := os.MkdirAll(filepath.Join(skillRoot, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillRoot, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("---\nname: promo-video-production\ndescription: 2K promo pipeline\n---\n# Steps\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "scripts", "render.py"), []byte("print('ok')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "references", "style.md"), []byte("# Style"), 0o644); err != nil {
		t.Fatal(err)
	}

	bot, err := svc.registry.Create(ctx, CreateBotRequest{
		Name:           "视频制作",
		Slug:           "video-studio",
		Workspace:      botWS,
		DefaultAgentID: "antigravity",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Patch presets and default_notify_caller=false
	patchBody := `{
		"default_notify_caller": false,
		"presets": [
			{"label": "90 秒 2K 宣传片", "title": "2K 宣传片", "prompt": "制作 90 秒 2K 宣传片并附带封面"}
		]
	}`
	pReq := httptest.NewRequest(http.MethodPatch, BasePath+"/"+bot.ID, strings.NewReader(patchBody))
	pW := httptest.NewRecorder()
	svc.ServeHTTP(pW, pReq)
	if pW.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", pW.Code, pW.Body.String())
	}

	got, err := svc.GetSummary(ctx, bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultNotifyCaller == nil || *got.DefaultNotifyCaller != false {
		t.Fatalf("expected DefaultNotifyCaller=false, got %v", got.DefaultNotifyCaller)
	}
	if len(got.Presets) != 1 || got.Presets[0].Label != "90 秒 2K 宣传片" || got.Presets[0].ID == "" {
		t.Fatalf("expected 1 persisted preset, got %+v", got.Presets)
	}

	// Fetch docs and check auxiliary skill files + referenced external skills
	dReq := httptest.NewRequest(http.MethodGet, BasePath+"/"+bot.ID+"/docs", nil)
	dW := httptest.NewRecorder()
	svc.ServeHTTP(dW, dReq)
	if dW.Code != http.StatusOK {
		t.Fatalf("docs status = %d, body = %s", dW.Code, dW.Body.String())
	}
	var docs BotDocsResponse
	if err := json.Unmarshal(dW.Body.Bytes(), &docs); err != nil {
		t.Fatal(err)
	}
	if len(docs.Skills) != 1 || len(docs.Skills[0].Files) != 2 {
		t.Fatalf("expected 2 auxiliary files on skill, got %+v", docs.Skills)
	}
	joinedRefs := strings.Join(docs.ReferencedSkills, ",")
	if !strings.Contains(joinedRefs, "astrorder-browser") || !strings.Contains(joinedRefs, "agently-mail") || strings.Contains(joinedRefs, "promo-video-production") {
		t.Fatalf("unexpected referenced external skills: %+v", docs.ReferencedSkills)
	}
}
