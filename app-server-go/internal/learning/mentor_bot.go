package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/bots"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

// BootstrapMentorBot creates (or reuses) the official Learning Mentor Bot and binds it to config.
func (s *Service) BootstrapMentorBot(ctx context.Context, req BootstrapMentorBotRequest) (*BootstrapMentorBotResponse, error) {
	if s.options.Bots == nil {
		return nil, fmt.Errorf("bots service is not configured")
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = defaultMentorBotSlug
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = defaultMentorBotName
	}

	// If a Bot with this slug already exists, reuse & bind it (and optionally upgrade/seed its 3-skill mentor suite).
	if existing, err := s.options.Bots.GetSummary(ctx, slug); err == nil && existing != nil {
		var warnings []string
		if req.UpgradeTemplates {
			if seedErr := s.seedMentorWorkspaceDocs(ctx, existing.ConnectionID, existing.Workspace, true); seedErr != nil {
				warnings = append(warnings, "升级官方导师技能模板失败："+seedErr.Error())
			}
		} else {
			// Ensure newly introduced companion skills exist without overwriting user-customized AGENTS.md.
			_ = s.seedMentorWorkspaceDocs(ctx, existing.ConnectionID, existing.Workspace, false)
		}
		if refreshed, rErr := s.options.Bots.GetSummary(ctx, slug); rErr == nil && refreshed != nil {
			existing = refreshed
		}
		cfg, _ := s.repo.GetConfig(ctx)
		cfg.MentorBotID = existing.ID
		savedCfg, saveErr := s.repo.SaveConfig(ctx, cfg)
		if saveErr != nil {
			return nil, saveErr
		}
		return &BootstrapMentorBotResponse{
			OK:       true,
			Created:  false,
			Bot:      *existing,
			Config:   savedCfg,
			Warnings: warnings,
		}, nil
	}

	agentID := strings.TrimSpace(req.AgentID)
	connID := strings.TrimSpace(req.ConnectionID)
	if agentID == "" && s.options.Store != nil {
		agentID, connID = s.pickDefaultReadyAgent(ctx, connID)
	}
	if agentID == "" {
		return nil, fmt.Errorf("%w: 未找到可用的 Agent 引擎，请先在「设置 → 连接与运行环境」配置至少一个 Agent", ErrInvalid)
	}

	workspace := strings.TrimSpace(req.Workspace)
	if workspace == "" {
		workspace = s.defaultMentorWorkspace(connID)
	}

	// Ensure workspace directory exists before CreateBot writes AGENTS.md & SKILL.md.
	if s.options.Resolver != nil && workspace != "" {
		if err := s.ensureWorkspaceExists(ctx, connID, workspace); err != nil {
			s.logger.Warn("prepare mentor bot workspace directory", "workspace", workspace, "error", err)
		}
	}

	notifyCaller := false
	created, warnings, err := s.options.Bots.CreateBot(ctx, bots.CreateBotRequest{
		Name:                name,
		Slug:                slug,
		Description:         "驱动星序成长雷达的首席实战技术导师：深度侦察真实代码/会话/CI 盲区，并以费曼教学法 + 图文并茂（Mermaid / SVG / Sidecar 交互动画）+ 真实代码剖析带你真正学会。",
		Icon:                "radar",
		Workspace:           workspace,
		ConnectionID:        connID,
		DefaultAgentID:      agentID,
		DefaultProvider:     strings.TrimSpace(req.Provider),
		DefaultModel:        strings.TrimSpace(req.Model),
		DefaultEffort:       strings.TrimSpace(req.Effort),
		DefaultApprovalMode: bots.ApprovalModeFullAccess,
		DefaultNotifyCaller: &notifyCaller,
		Tags:                []string{"learning", "mentor", "radar", "visual-tutor"},
		AgentsMD:            mentorAgentsMDTemplate,
		WriteAgentsMD:       true,
		WriteSkill:          true,
		SkillSlug:           defaultMentorSkillSlug,
		SkillMD:             mentorSkillMDTemplate,
	})
	if err != nil {
		return nil, err
	}

	if seedErr := s.seedMentorWorkspaceDocs(ctx, created.ConnectionID, created.Workspace, false); seedErr != nil {
		warnings = append(warnings, "写入配套教学与图解技能时遇到警告："+seedErr.Error())
	} else if refreshed, rErr := s.options.Bots.GetSummary(ctx, created.ID); rErr == nil && refreshed != nil {
		created = refreshed
	}

	cfg, _ := s.repo.GetConfig(ctx)
	cfg.MentorBotID = created.ID
	savedCfg, err := s.repo.SaveConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return &BootstrapMentorBotResponse{
		OK:       true,
		Created:  true,
		Bot:      *created,
		Config:   savedCfg,
		Warnings: warnings,
	}, nil
}

// seedMentorWorkspaceDocs writes the extra modular teaching/visual skills (and optionally overwrites AGENTS.md + core skill).
func (s *Service) seedMentorWorkspaceDocs(ctx context.Context, connID, workspace string, overwriteAll bool) error {
	if s.options.Resolver == nil || strings.TrimSpace(workspace) == "" {
		return nil
	}
	resolved, err := s.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    workspace,
		ConnectionID: connID,
	})
	if err != nil {
		return err
	}

	writeDoc := func(relDir []string, fileName, content string, overwrite bool) error {
		dir := resolved.Workspace
		if len(relDir) > 0 {
			var mkErr error
			dir, mkErr = ensureDirPath(ctx, resolved.FileSystem, connID, resolved.Workspace, relDir...)
			if mkErr != nil {
				return mkErr
			}
		}
		target := joinBotPath(connID, dir, fileName)
		if !overwrite {
			if info, statErr := resolved.FileSystem.Stat(ctx, target); statErr == nil && !info.IsDir && info.Size > 0 {
				return nil
			}
		}
		_, wErr := resolved.FileSystem.WriteAtomic(ctx, target, strings.NewReader(content), 0o644)
		return wErr
	}

	if overwriteAll {
		if err := writeDoc(nil, "AGENTS.md", mentorAgentsMDTemplate, true); err != nil {
			return err
		}
		if err := writeDoc([]string{".agents", "skills", defaultMentorSkillSlug}, "SKILL.md", mentorSkillMDTemplate, true); err != nil {
			return err
		}
	}
	for _, sk := range mentorExtraSkills {
		if err := writeDoc([]string{".agents", "skills", sk.Slug}, "SKILL.md", sk.Content, overwriteAll); err != nil {
			return err
		}
	}
	_, _ = ensureDirPath(ctx, resolved.FileSystem, connID, resolved.Workspace, "notes")
	_, _ = ensureDirPath(ctx, resolved.FileSystem, connID, resolved.Workspace, "visuals")
	return nil
}

// DispatchScan collects local evidence, writes evidence-pack.json to the bound Mentor Bot's inbox,
// and invokes a native Bot scan session.
func (s *Service) DispatchScan(ctx context.Context, req TriggerScanRequest) (*TriggerScanResponse, error) {
	if s.options.Bots == nil {
		return nil, fmt.Errorf("bots service is not configured")
	}
	_, _ = s.repo.ExpireStaleRuns(ctx, DefaultScanTimeout)
	if running, err := s.repo.HasRunningRun(ctx); err == nil && running != nil {
		return nil, fmt.Errorf("%w: 当前已有正在运行中的扫描任务（%s），请等待完成或进入对应 Bot 会话查看", ErrConflict, running.ID)
	}

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	botRef := strings.TrimSpace(req.BotID)
	if botRef == "" {
		botRef = strings.TrimSpace(cfg.MentorBotID)
	}
	if botRef == "" {
		return nil, fmt.Errorf("%w: 尚未绑定「成长导师 Bot」，请先在「定时与历史」中选择已有 Bot 或一键创建官方导师 Bot", ErrInvalid)
	}
	mentorBot, err := s.options.Bots.GetSummary(ctx, botRef)
	if err != nil || mentorBot == nil {
		return nil, fmt.Errorf("%w: 未找到绑定的成长导师 Bot（%s），请重新绑定", ErrNotFound, botRef)
	}

	trigger := strings.TrimSpace(req.Trigger)
	if trigger == "" {
		trigger = "manual"
	}
	lookback := req.LookbackDays
	if lookback <= 0 {
		lookback = cfg.LookbackDays
	}
	if lookback <= 0 {
		lookback = DefaultLookbackDays
	}

	now := s.now().UTC()
	runID := fmt.Sprintf("scan-%s-%s", now.Format("20060102-150405"), randomHex(2))
	pack, err := s.CollectEvidencePack(ctx, runID, trigger, lookback, req.ProjectKeys, cfg, mentorBot.Workspace)
	if err != nil {
		return nil, err
	}

	// Quiet-week noise guard for automatic weekly schedules.
	if trigger == "scheduled" &&
		pack.ActiveSessions < cfg.MinActivitySessions &&
		pack.CommitCount < cfg.MinActivityCommits {
		skippedRun := ScanRun{
			ID:             runID,
			Trigger:        trigger,
			Status:         "skipped",
			BotID:          mentorBot.ID,
			BotName:        mentorBot.Name,
			LookbackDays:   lookback,
			StartedAt:      now.Format(time.RFC3339),
			FinishedAt:     now.Format(time.RFC3339),
			EvidenceCount:  pack.EvidenceCount,
			ActiveSessions: pack.ActiveSessions,
			CommitCount:    pack.CommitCount,
			Summary:        fmt.Sprintf("未满足低活跃触发阈值（本周期活跃会话 %d、提交 %d），已自动跳过", pack.ActiveSessions, pack.CommitCount),
		}
		_ = s.repo.SaveRun(ctx, skippedRun)
		_, _ = s.repo.AdvanceScheduleAfterTrigger(ctx, now)
		return &TriggerScanResponse{
			OK:      true,
			Run:     skippedRun,
			Message: skippedRun.Summary,
		}, nil
	}

	inboxDir, packPath, err := s.stageJSONToBotInbox(ctx, mentorBot.ConnectionID, mentorBot.Workspace, runID, "evidence-pack.json", pack)
	if err != nil {
		return nil, fmt.Errorf("写入证据包到导师 Bot 收件箱失败: %w", err)
	}

	resultPath := joinBotPath(mentorBot.ConnectionID, inboxDir, "radar-result.json")
	prompt := fmt.Sprintf(`请先完整阅读本工作区技能 `+"`.agents/skills/learning-radar-mentor/SKILL.md`"+`，严格按照其 4 阶段侦察与质量门禁执行「成长雷达」深度盲区扫描任务（run_id: %s，周期：近 %d 天，触发方式：%s）。

1. 先读取已投递到本工作区的结构化证据包：`+"`%s`"+`（共采集 %d 条线索、涵盖 %d 个项目、%d 个活跃会话、%d 笔提交）。
2. 严格排除证据包中 `+"`excluded_topics`"+` 列出的已掌握或已忽略主题；若涉及 `+"`active_topics`"+` 已有主题且发现新线索，保持标题一致以便自动合并证据。
3. **必须做真实源码与现场查证（严禁只看标题瞎猜）**：对高改动的「AI 代写黑盒」或关键报错/追问会话，动用原生工具读取真实项目源码、执行 `+"`git show --stat`"+`、调用 `+"`astrorder-agent sessions read`"+` 或 `+"`astrorder-agent gitlab log`"+` 核实底层机理。
4. 宁缺毋滥精选 3~6 个高价值盲区卡片（钩子式标题 + 3~4 条递进因果速懂要点 + 2~3 道场景/反例自测题 + ≥2 条真实证据），将完整 JSON 写入 `+"`%s`"+`。
5. 最后务必运行：
   `+"`astrorder-agent learning ingest --run-id %s --file %s`"+`
   完成回写，并用清晰分点的中文向用户汇报本次最值得优先攻克的盲区。`,
		runID, lookback, trigger, packPath, pack.EvidenceCount, len(pack.Projects), pack.ActiveSessions, pack.CommitCount, resultPath, runID, resultPath)

	notifyCaller := false
	invokeResp, err := s.options.Bots.Invoke(ctx, bots.InvokeRequest{
		Bot:          mentorBot.ID,
		OperationID:  "learning-" + runID,
		Title:        fmt.Sprintf("成长雷达扫描 · 近 %d 天 (%s)", lookback, now.In(s.location).Format("01-02 15:04")),
		Prompt:       prompt,
		NotifyCaller: &notifyCaller,
	})
	if err != nil {
		failedRun := ScanRun{
			ID:             runID,
			Trigger:        trigger,
			Status:         "failed",
			BotID:          mentorBot.ID,
			BotName:        mentorBot.Name,
			InboxDir:       inboxDir,
			LookbackDays:   lookback,
			StartedAt:      now.Format(time.RFC3339),
			FinishedAt:     s.now().UTC().Format(time.RFC3339),
			EvidenceCount:  pack.EvidenceCount,
			ActiveSessions: pack.ActiveSessions,
			CommitCount:    pack.CommitCount,
			Error:          err.Error(),
		}
		_ = s.repo.SaveRun(ctx, failedRun)
		s.publishEvent(ctx, "learning.scan.failed", map[string]any{"run": failedRun})
		return nil, fmt.Errorf("派发导师 Bot 扫描会话失败: %w", err)
	}

	sessionKey := extractSessionKey(invokeResp.Session)
	run := ScanRun{
		ID:             runID,
		Trigger:        trigger,
		Status:         "running",
		BotID:          mentorBot.ID,
		BotName:        mentorBot.Name,
		SessionKey:     sessionKey,
		InboxDir:       inboxDir,
		LookbackDays:   lookback,
		StartedAt:      now.Format(time.RFC3339),
		EvidenceCount:  pack.EvidenceCount,
		ActiveSessions: pack.ActiveSessions,
		CommitCount:    pack.CommitCount,
		Summary:        fmt.Sprintf("已派发给 %s（@%s）深度分析 %d 条本地线索…", mentorBot.Name, mentorBot.Slug, pack.EvidenceCount),
	}
	_ = s.repo.SaveRun(ctx, run)
	_, _ = s.repo.AdvanceScheduleAfterTrigger(ctx, now)
	s.publishEvent(ctx, "learning.scan.started", map[string]any{"run": run})

	return &TriggerScanResponse{
		OK:      true,
		Run:     run,
		Message: run.Summary,
	}, nil
}

// DispatchStudy spawns a code-grounded tutoring or review-drill session on the bound Mentor Bot.
func (s *Service) DispatchStudy(ctx context.Context, topicID string, req StartStudyRequest) (*StartStudyResponse, error) {
	if s.options.Bots == nil {
		return nil, fmt.Errorf("bots service is not configured")
	}
	topic, err := s.repo.GetTopic(ctx, topicID)
	if err != nil {
		return nil, err
	}
	cfg, _ := s.repo.GetConfig(ctx)
	botRef := strings.TrimSpace(req.BotID)
	if botRef == "" {
		botRef = strings.TrimSpace(cfg.MentorBotID)
	}
	if botRef == "" {
		return nil, fmt.Errorf("%w: 尚未绑定「成长导师 Bot」，请先在「定时与历史」中选择或一键创建官方导师 Bot", ErrInvalid)
	}
	mentorBot, err := s.options.Bots.GetSummary(ctx, botRef)
	if err != nil || mentorBot == nil {
		return nil, fmt.Errorf("%w: 未找到成长导师 Bot %q", ErrNotFound, botRef)
	}

	jobID := fmt.Sprintf("study-%s-%s", topic.ID, randomHex(2))
	inboxDir, briefPath, err := s.stageJSONToBotInbox(ctx, mentorBot.ConnectionID, mentorBot.Workspace, jobID, "topic-brief.json", topic)
	if err != nil {
		return nil, fmt.Errorf("写入伴学资料到导师 Bot 收件箱失败: %w", err)
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	modeTitle := "伴学"
	modeGoal := "请先完整阅读本工作区的 `.agents/skills/feynman-code-tutoring/SKILL.md` 与 `.agents/skills/visual-explainer/SKILL.md`，严格按「费曼五步讲透法」开展教学：\n1) 先真实读取下方项目源码与行号（必要时通过 `astrorder-agent invoke plugins.open` 用 `monaco` 在右侧边栏打开核心源码）；\n2) 用 1 个通俗贴切的生活/工程类比破冰说人话；\n3) **强制图文并茂**：至少配 1 张高质量 Mermaid 图 / 高清 SVG 图（存 `visuals/` 并内嵌展示），若涉及复杂动态/并发/内存机制，请主动生成单文件交互式 HTML 动画图解并通过 `plugins.open`（`plugin_id: \"html\"`）在星序右侧 Sidecar 自动弹出，同时附上「误区 vs 底层真相 vs 推荐方案」对比表；\n4) 引用我项目里最关键的 8~20 行真实代码逐行注释 + 反事实推演（改错/删掉会怎样）；\n5) 讲透后**只抛 1 道场景/反例自测题**引导我用大白话复述检验，严禁敷衍或一口气扔题海。"
	if mode == "review_drill" {
		modeTitle = "抽查复习"
		modeGoal = "请先阅读 `.agents/skills/feynman-code-tutoring/SKILL.md` 的「间隔复习抽查模式（提取练习法）」与 `.agents/skills/visual-explainer/SKILL.md`！这是一次间隔复习抽查：**严禁一上来长篇大论剧透原理**。先用 2 句话唤起项目源码语境，直接抛出第 1 道变体场景检验题让我作答，再根据我的回忆准确度用图解+代码精准补漏。"
	}

	var evLines []string
	for _, ev := range topic.Evidence {
		line := fmt.Sprintf("- [%s] %s", ev.Kind, ev.Label)
		if ev.FilePath != "" {
			line += fmt.Sprintf("（文件：`%s`）", ev.FilePath)
		}
		if ev.SessionID != "" && ev.AgentID != "" {
			line += fmt.Sprintf("（原会话：`%s::%s`）", ev.AgentID, ev.SessionID)
		}
		if ev.CommitSHA != "" {
			line += fmt.Sprintf("（Commit：`%s`）", ev.CommitSHA)
		}
		evLines = append(evLines, line)
	}
	evidenceSection := "（见 topic-brief.json）"
	if len(evLines) > 0 {
		evidenceSection = strings.Join(evLines, "\n")
	}

	extraNote := ""
	if strings.TrimSpace(req.ExtraInstruction) != "" {
		extraNote = "\n\n【用户补充诉求】\n" + strings.TrimSpace(req.ExtraInstruction)
	}

	prompt := fmt.Sprintf(`【成长雷达 · %s任务 | topic_id: %s】

主题：%s
所属项目：%s（工作区：`+"`%s`"+`）
背景原因：%s
完整卡片资料：`+"`%s`"+`

【关联工作现场证据】
%s

【教学要求】
%s
- 学习过程中，如沉淀出清晰笔记，请保存到本工作区 `+"`notes/%s/%s.md`"+`。
- 当我通过检验表现出已掌握时，请主动调用：
  `+"`astrorder-agent learning update --topic-id %s --status mastered --notes \"<关键收获>\" --notes-file \"<笔记路径>\"`"+`%s`,
		modeTitle, topic.ID, topic.Title, nonEmpty(topic.ProjectName, "当前工作区"), topic.Workspace,
		topic.WhyNow, briefPath, evidenceSection, modeGoal,
		safePathSegment(nonEmpty(topic.ProjectName, "general")), safePathSegment(topic.ID),
		topic.ID, extraNote)

	notifyCaller := false
	invokeResp, err := s.options.Bots.Invoke(ctx, bots.InvokeRequest{
		Bot:          mentorBot.ID,
		OperationID:  "learning-" + jobID,
		AgentID:      strings.TrimSpace(req.AgentID),
		Title:        fmt.Sprintf("[%s] %s", modeTitle, truncateText(topic.Title, 32)),
		Prompt:       prompt,
		NotifyCaller: &notifyCaller,
	})
	if err != nil {
		return nil, fmt.Errorf("开启导师 Bot 伴学会话失败: %w", err)
	}

	sessionKey := extractSessionKey(invokeResp.Session)
	aid, sid := splitSessionKey(sessionKey)
	enrollReview := true
	var scheduleID string
	if req.SyncSessionSchedule != nil && *req.SyncSessionSchedule && aid != "" && sid != "" {
		scheduleID = s.syncTopicSessionSchedule(ctx, *topic, aid, sid, s.now().UTC().AddDate(0, 0, leitnerIntervals[0]))
	}
	updated, err := s.repo.AttachStudySession(ctx, topic.ID, mentorBot.ID, sessionKey, enrollReview, scheduleID)
	if err != nil {
		return nil, err
	}
	s.publishEvent(ctx, "learning.topic.updated", map[string]any{"topic": updated})

	return &StartStudyResponse{
		OK:         true,
		Topic:      *updated,
		SessionKey: sessionKey,
		AgentID:    aid,
		SessionID:  sid,
		InboxDir:   inboxDir,
		Message:    fmt.Sprintf("已由 %s 开启%s会话", mentorBot.Name, modeTitle),
	}, nil
}

// syncTopicSessionSchedule creates or updates a session-bound spaced-repetition reminder on the tutor session.
func (s *Service) syncTopicSessionSchedule(ctx context.Context, topic LearningTopic, agentID, sessionID string, runAt time.Time) string {
	if s.options.Store == nil || agentID == "" || sessionID == "" {
		return ""
	}
	schedID := strings.TrimSpace(topic.Review.ScheduleID)
	if schedID == "" {
		schedID = "lrev-" + topic.ID
	}
	nowISO := s.now().UTC().Format(time.RFC3339)
	runISO := runAt.UTC().Format(time.RFC3339)
	item := store.SessionSchedule{
		ID:              schedID,
		AgentID:         agentID,
		SessionID:       sessionID,
		Name:            "复习抽查：" + truncateText(topic.Title, 28),
		Prompt:          fmt.Sprintf("【成长雷达 · 到期间隔复习抽查 | topic_id: %s】距离上次学习「%s」已到复习周期，请结合项目实际代码向我提出 1~2 道简短检验题。我通过后请帮我更新掌握状态（astrorder-agent learning update --topic-id %s ...）。", topic.ID, topic.Title, topic.ID),
		Enabled:         true,
		ScheduleKind:    "once",
		RunAt:           runISO,
		Timezone:        "Local",
		WhenBusy:        "queue",
		MisfirePolicy:   "run_once",
		MisfireGraceSec: 86400,
		NextRunAt:       runISO,
		CreatedAt:       nowISO,
		UpdatedAt:       nowISO,
	}
	existing, _ := s.options.Store.GetSessionSchedule(ctx, schedID)
	var saveErr error
	if existing != nil {
		_, saveErr = s.options.Store.UpdateSessionSchedule(ctx, item)
	} else {
		_, saveErr = s.options.Store.CreateSessionSchedule(ctx, item)
	}
	if saveErr != nil {
		s.logger.Warn("sync learning review session schedule failed", "topic_id", topic.ID, "error", saveErr)
		return ""
	}
	if s.options.WakeSchedules != nil {
		s.options.WakeSchedules()
	}
	return schedID
}

func (s *Service) stageJSONToBotInbox(ctx context.Context, connID, workspace, jobDirName, fileName string, payload any) (string, string, error) {
	if s.options.Resolver == nil {
		return "", "", fmt.Errorf("filesystem resolver is not configured")
	}
	resolved, err := s.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    workspace,
		ConnectionID: connID,
	})
	if err != nil {
		return "", "", err
	}
	inboxRoot, err := ensureDirPath(ctx, resolved.FileSystem, connID, resolved.Workspace, "inbox", jobDirName)
	if err != nil {
		return "", "", err
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", "", err
	}
	raw = append(raw, '\n')
	targetPath := joinBotPath(connID, inboxRoot, fileName)
	if _, err := resolved.FileSystem.WriteAtomic(ctx, targetPath, strings.NewReader(string(raw)), 0o644); err != nil {
		return "", "", err
	}
	return inboxRoot, targetPath, nil
}

func (s *Service) ensureWorkspaceExists(ctx context.Context, connID, workspace string) error {
	clean := strings.TrimSpace(workspace)
	if clean == "" {
		return nil
	}
	isPOSIX := (connID != "" && !strings.EqualFold(connID, targetfs.LocalConnectionID)) || strings.HasPrefix(clean, "/")
	var parent, name string
	if isPOSIX {
		clean = strings.TrimRight(strings.ReplaceAll(clean, "\\", "/"), "/")
		parent = path.Dir(clean)
		name = path.Base(clean)
	} else {
		parent = filepath.Dir(clean)
		name = filepath.Base(clean)
	}
	if parent == "" || parent == "." || name == "" || name == "." {
		return nil
	}
	resolved, err := s.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    parent,
		ConnectionID: connID,
	})
	if err != nil {
		return err
	}
	_, err = ensureDirPath(ctx, resolved.FileSystem, connID, resolved.Workspace, name)
	return err
}

func ensureDirPath(ctx context.Context, fs targetfs.FileSystem, connID, base string, segments ...string) (string, error) {
	current := strings.TrimSpace(base)
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		next := joinBotPath(connID, current, seg)
		if info, err := fs.Stat(ctx, next); err == nil && info.IsDir {
			current = next
			continue
		}
		if _, err := fs.Mkdir(ctx, targetfs.MkdirRequest{Parent: current, Name: seg}); err != nil {
			if info, statErr := fs.Stat(ctx, next); statErr == nil && info.IsDir {
				current = next
				continue
			}
			return "", err
		}
		current = next
	}
	return current, nil
}

func joinBotPath(connID, base string, segs ...string) string {
	isPOSIX := (connID != "" && !strings.EqualFold(connID, targetfs.LocalConnectionID)) || strings.HasPrefix(strings.TrimSpace(base), "/")
	if isPOSIX {
		parts := append([]string{strings.ReplaceAll(strings.TrimSpace(base), "\\", "/")}, segs...)
		return path.Join(parts...)
	}
	parts := append([]string{strings.TrimSpace(base)}, segs...)
	return filepath.Join(parts...)
}

func (s *Service) pickDefaultReadyAgent(ctx context.Context, preferredConn string) (string, string) {
	agents, err := s.options.Store.ListAgents(ctx)
	if err != nil || len(agents) == 0 {
		return "", preferredConn
	}
	prefConn := strings.TrimSpace(preferredConn)
	var fallbackID, fallbackConn string
	for _, a := range agents {
		id := strVal(a["id"])
		if id == "" {
			continue
		}
		cid := strVal(a["connection_id"])
		status := strings.ToLower(strVal(a["status"]))
		if fallbackID == "" {
			fallbackID, fallbackConn = id, cid
		}
		if prefConn != "" && cid != prefConn {
			continue
		}
		if status == "ready" || status == "online" || status == "connected" || status == "idle" {
			return id, cid
		}
	}
	return fallbackID, fallbackConn
}

func (s *Service) defaultMentorWorkspace(connID string) string {
	base := strings.TrimSpace(s.options.DefaultWorkspace)
	if base != "" {
		return joinBotPath(connID, base, "learning-mentor")
	}
	if connID != "" && !strings.EqualFold(connID, targetfs.LocalConnectionID) {
		return "/tmp/astrorder-learning-mentor"
	}
	return filepath.Join(".", "learning-mentor")
}

func extractSessionKey(sess map[string]any) string {
	if sess == nil {
		return ""
	}
	if k := strings.TrimSpace(strVal(sess["key"])); k != "" {
		return k
	}
	aid := strings.TrimSpace(strVal(sess["agent_id"]))
	sid := strings.TrimSpace(strVal(sess["id"]))
	if aid != "" && sid != "" {
		return aid + "::" + sid
	}
	return ""
}

func splitSessionKey(key string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(key), "::", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func safePathSegment(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' || r == '/' || r == '\\' {
			b.WriteByte('-')
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		return "topic"
	}
	return res
}
