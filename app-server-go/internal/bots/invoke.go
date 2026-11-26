package bots

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Invoke resolves a Bot, stages any attached files into <bot.Workspace>/inbox/<job-id>/,
// composes a push-brief prompt with optional caller traceback pointers, and spawns a clean job session.
func (s *Service) Invoke(ctx context.Context, req InvokeRequest) (*InvokeResponse, error) {
	botRef := strings.TrimSpace(req.Bot)
	if botRef == "" {
		return nil, fmt.Errorf("%w: bot 标识不能为空", ErrInvalid)
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("%w: 任务说明（prompt）不能为空", ErrInvalid)
	}
	bot, err := s.registry.Find(ctx, botRef)
	if err != nil {
		return nil, fmt.Errorf("%w: 未找到 Bot %q", ErrNotFound, botRef)
	}
	if s.options.Spawner == nil {
		return nil, fmt.Errorf("session spawner is not configured")
	}

	jobID := fmt.Sprintf("job-%s-%s", time.Now().UTC().Format("20060102-150405"), randomHex(2))
	var inboxDir string
	var stagedFiles []StagedFile
	if len(req.AttachPaths) > 0 {
		inboxDir, stagedFiles, err = s.StageAttachmentsToInbox(ctx, req.CallerConnectionID, req.CallerWorkspace, *bot, jobID, req.AttachPaths)
		if err != nil {
			return nil, err
		}
	}

	notifyCaller := true
	if bot.DefaultNotifyCaller != nil {
		notifyCaller = *bot.DefaultNotifyCaller
	}
	if req.NotifyCaller != nil {
		notifyCaller = *req.NotifyCaller
	}

	fullPrompt := composeBotBrief(*bot, jobID, prompt, inboxDir, stagedFiles, req.CallerSessionKey, req.CallerWorkspace, req.CallerConnectionID, notifyCaller)

	agentID := nonEmpty(strings.TrimSpace(req.AgentID), bot.DefaultAgentID)
	provider := nonEmpty(strings.TrimSpace(req.Provider), bot.DefaultProvider)
	model := nonEmpty(strings.TrimSpace(req.Model), bot.DefaultModel)
	effort := nonEmpty(strings.TrimSpace(req.Effort), bot.DefaultEffort)
	rawApproval := nonEmpty(strings.TrimSpace(req.ApprovalMode), bot.DefaultApprovalMode)
	approvalMode, err := NormalizeApprovalMode(rawApproval)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("[%s] %s", bot.Name, truncateRunes(firstNonEmptyLine(prompt, jobID), 36))
	}
	opID := strings.TrimSpace(req.OperationID)
	if opID == "" {
		opID = "bot-" + jobID
	}

	spawnRes, err := s.options.Spawner(ctx, SpawnRequest{
		OperationID:  opID,
		AgentID:      agentID,
		Title:        title,
		Workspace:    bot.Workspace,
		Model:        model,
		Provider:     provider,
		Effort:       effort,
		ApprovalMode: approvalMode,
		Prompt:       fullPrompt,
		Caller:       req.Caller,
	})
	if err != nil {
		return nil, err
	}

	resp := &InvokeResponse{
		OK:          true,
		Bot:         *bot,
		JobID:       jobID,
		InboxDir:    inboxDir,
		StagedFiles: stagedFiles,
		Session:     spawnRes.Session,
		Receipt:     spawnRes.Receipt,
		OperationID: nonEmpty(spawnRes.OperationID, opID),
		Stage:       spawnRes.Stage,
	}
	return resp, nil
}

func composeBotBrief(
	bot BotProfile,
	jobID string,
	prompt string,
	inboxDir string,
	staged []StagedFile,
	callerSessionKey string,
	callerWorkspace string,
	callerConnectionID string,
	notifyCaller bool,
) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("【Bot 任务派单 · %s | %s】\n\n", bot.Name, jobID))
	b.WriteString(prompt)
	if len(staged) > 0 {
		b.WriteString("\n\n---\n【已推送到本工作区的任务素材】\n")
		b.WriteString(fmt.Sprintf("目录：`%s`\n", inboxDir))
		for _, item := range staged {
			b.WriteString(fmt.Sprintf("- `%s`（%d bytes）\n", item.TargetPath, item.Size))
		}
	}
	trimmedCallerKey := strings.TrimSpace(callerSessionKey)
	trimmedCallerWS := strings.TrimSpace(callerWorkspace)
	if trimmedCallerKey != "" || trimmedCallerWS != "" {
		b.WriteString("\n\n---\n【发起方上下文（按需参考，无需默认跨项目翻找）】\n")
		if trimmedCallerKey != "" {
			b.WriteString(fmt.Sprintf("- 发起会话：`%s`\n", trimmedCallerKey))
		}
		if trimmedCallerWS != "" {
			envLabel := nonEmpty(strings.TrimSpace(callerConnectionID), "local")
			b.WriteString(fmt.Sprintf("- 发起工作区：`%s`（环境：`%s`）\n", trimmedCallerWS, envLabel))
		}
		b.WriteString("说明：以上任务描述与 inbox 附件已尽量自包含；如执行中判断确需补充源项目材料或向发起方确认，可按需读取或联系发起会话。\n")
	}
	if trimmedCallerKey != "" && notifyCaller {
		b.WriteString("\n---\n【任务完成自动回告约定】\n")
		b.WriteString("任务完成交付（或遇到需发起方决策的阻塞）后，请主动向发起会话回传交付摘要与最终产物绝对路径：\n")
		b.WriteString(fmt.Sprintf("`astrorder-agent sessions send --session %s --text \"【来自 %s | %s】<交付结论、产物绝对路径与验收摘要>\"`\n", trimmedCallerKey, bot.Name, jobID))
	}
	return b.String()
}
