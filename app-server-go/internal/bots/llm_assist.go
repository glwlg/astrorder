package bots

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/llmcompat"
)

var skillPathPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_-])skills[/\\]([a-z0-9][a-z0-9_-]{1,63})[/\\]SKILL\.md`)

func (s *Service) resolveLLM(ctx context.Context, providerID, model string) aiprovider.ResolvedModel {
	if s.options.Store == nil {
		return aiprovider.ResolvedModel{}
	}
	if strings.TrimSpace(providerID) != "" {
		if cfg, ok := aiprovider.Resolve(ctx, s.options.Store, providerID, model); ok {
			return cfg
		}
	}
	cfg := aiprovider.ResolveDefault(ctx, s.options.Store)
	if strings.TrimSpace(model) != "" {
		cfg.Model = strings.TrimSpace(model)
	}
	return cfg
}

// Optimize transforms plain-language basic settings into a structured AGENTS.md constitution.
func (s *Service) Optimize(ctx context.Context, req OptimizeRequest) (*OptimizeResponse, error) {
	name := strings.TrimSpace(req.Name)
	desc := strings.TrimSpace(req.Description)
	instructions := strings.TrimSpace(req.Instructions)
	if name == "" && desc == "" && instructions == "" {
		return nil, fmt.Errorf("%w: 请至少填写 Bot 名称、简介或基础设定", ErrInvalid)
	}
	if name == "" {
		name = "Bot"
	}

	fallbackSlug := NormalizeSlug(name)
	if fallbackSlug == "" {
		fallbackSlug = NormalizeSlug(workspaceBasename(req.Workspace))
	}
	if fallbackSlug == "" {
		fallbackSlug = "specialist-bot"
	}
	fallbackDesc := desc
	if fallbackDesc == "" {
		fallbackDesc = truncateRunes(firstNonEmptyLine(instructions, name+" 专用工作台 Bot"), 80)
	}
	fallbackMD := buildTemplateAgentsMD(name, fallbackDesc, instructions, req.ExistingAgentsMD)

	llmCfg := s.resolveLLM(ctx, req.ProviderID, req.Model)
	llmConfigured := llmCfg.IsConfigured()
	if strings.EqualFold(strings.TrimSpace(req.Mode), "template") || !llmConfigured {
		msg := ""
		if !llmConfigured {
			msg = "未配置通用 AI 模型（可在设置中选择已接入 Agent 或填写 API Key），已使用结构化专业模板生成 AGENTS.md，可直接编辑保存。"
		}
		return &OptimizeResponse{
			OK:                   true,
			Mode:                 "template",
			LLMConfigured:        llmConfigured,
			Model:                llmCfg.Model,
			SuggestedName:        name,
			SuggestedSlug:        fallbackSlug,
			SuggestedDescription: fallbackDesc,
			SuggestedTags:        inferDefaultTags(name, fallbackDesc, instructions),
			AgentsMD:             fallbackMD,
			Message:              msg,
		}, nil
	}

	systemPrompt := strings.TrimSpace(`你是 Astrorder「Bot」架构师。你的任务是将用户输入的基础设定优化为一份专业、清晰、可直接写入工作区根目录的 AGENTS.md 角色宪章。

严格输出一个合法 JSON 对象（不要包含 Markdown 代码围栏或额外说明），包含以下字段：
{
  "name": "简短清晰的角色名（中文或中英结合）",
  "slug": "小写英文 kebab-case 标识（如 video-studio、contract-reviewer）",
  "description": "一句话核心能力说明（<= 60 字，供其他会话 @Bot 检索）",
  "tags": ["标签1", "标签2"],
  "agents_md": "完整的 AGENTS.md Markdown 文本"
}

AGENTS.md 编写规范：
1. 必须包含：角色定位与核心能力、默认工作流程（含自检与交付标准）、输入与跨会话协作约定、产物目录与命名规范、安全边界。
2. 在「输入与跨会话协作约定」中明确：
   - 优先使用派单消息和工作区 inbox/<job-id>/ 中已推送的材料开展工作，默认不需要主动翻找外部项目。
   - 若执行中确认为完成高质量交付仍需补充发起方项目材料或确认细节，允许按需读取发起方上下文或向发起方会话回问，再继续推进。
3. 严禁虚构不存在的脚本路径、硬件型号或工具；只围绕用户设定和已有 AGENTS.md（如提供）进行结构化提炼与增强。`)

	var userBuf strings.Builder
	userBuf.WriteString("Bot 名称：" + name + "\n")
	if desc != "" {
		userBuf.WriteString("简介：" + desc + "\n")
	}
	if strings.TrimSpace(req.Workspace) != "" {
		userBuf.WriteString("工作区目录：" + strings.TrimSpace(req.Workspace) + "\n")
	}
	if instructions != "" {
		userBuf.WriteString("\n【用户基础设定】\n" + instructions + "\n")
	}
	if existing := strings.TrimSpace(req.ExistingAgentsMD); existing != "" {
		userBuf.WriteString("\n【当前工作区已有 AGENTS.md（请在保留有效约束基础上迭代优化）】\n" + truncateRunes(existing, 6000) + "\n")
	}

	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := llmcompat.CompleteChat(callCtx, s.httpClient, llmCfg.ToChatRequest(3200, []llmcompat.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userBuf.String()},
	}))
	if err != nil || res.Status >= 400 || strings.TrimSpace(res.Content) == "" {
		errHint := "调用辅助 AI 模型失败，已回退为结构化模板。"
		if err != nil {
			errHint = fmt.Sprintf("调用辅助 AI 模型失败（%v），已回退为结构化模板。", err)
		} else if detail := llmcompat.ExtractErrorMessage(res.Raw); detail != "" {
			errHint = fmt.Sprintf("辅助 AI 模型返回错误（%s），已回退为结构化模板。", detail)
		}
		return &OptimizeResponse{
			OK:                   true,
			Mode:                 "template",
			LLMConfigured:        true,
			Model:                llmCfg.Model,
			SuggestedName:        name,
			SuggestedSlug:        fallbackSlug,
			SuggestedDescription: fallbackDesc,
			SuggestedTags:        inferDefaultTags(name, fallbackDesc, instructions),
			AgentsMD:             fallbackMD,
			Message:              errHint,
		}, nil
	}

	var parsed struct {
		Name        string   `json:"name"`
		Slug        string   `json:"slug"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		AgentsMD    string   `json:"agents_md"`
	}
	if err := unmarshalLooseJSON(res.Content, &parsed); err != nil || strings.TrimSpace(parsed.AgentsMD) == "" {
		// If LLM returned pure Markdown instead of JSON, treat it directly as agents_md.
		rawMD := stripOuterMarkdownFence(res.Content)
		if strings.TrimSpace(rawMD) == "" {
			rawMD = fallbackMD
		}
		return &OptimizeResponse{
			OK:                   true,
			Mode:                 "llm",
			LLMConfigured:        true,
			Model:                nonEmpty(res.Model, llmCfg.Model),
			SuggestedName:        name,
			SuggestedSlug:        fallbackSlug,
			SuggestedDescription: fallbackDesc,
			SuggestedTags:        inferDefaultTags(name, fallbackDesc, instructions),
			AgentsMD:             rawMD,
		}, nil
	}

	outName := nonEmpty(strings.TrimSpace(parsed.Name), name)
	outSlug := nonEmpty(NormalizeSlug(parsed.Slug), fallbackSlug)
	outDesc := nonEmpty(strings.TrimSpace(parsed.Description), fallbackDesc)
	outTags := cleanTags(parsed.Tags)
	if len(outTags) == 0 {
		outTags = inferDefaultTags(outName, outDesc, instructions)
	}
	return &OptimizeResponse{
		OK:                   true,
		Mode:                 "llm",
		LLMConfigured:        true,
		Model:                nonEmpty(res.Model, llmCfg.Model),
		SuggestedName:        outName,
		SuggestedSlug:        outSlug,
		SuggestedDescription: outDesc,
		SuggestedTags:        outTags,
		AgentsMD:             strings.TrimSpace(parsed.AgentsMD),
	}, nil
}

// Distill reads an existing session's transcript and summarizes reusable experience into AGENTS.md + SKILL.md.
func (s *Service) Distill(ctx context.Context, req DistillRequest) (*DistillResponse, error) {
	if s.options.Store == nil {
		return nil, fmt.Errorf("store is not configured")
	}
	agentID, sessionID, err := parseSessionRef(req.SessionKey, req.AgentID, req.SessionID)
	if err != nil {
		return nil, err
	}
	session, err := s.options.Store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if session == nil {
		return nil, fmt.Errorf("%w: 未找到指定会话 %s::%s", ErrNotFound, agentID, sessionID)
	}

	title, _ := session["title"].(string)
	workspace, _ := session["workspace"].(string)
	connID, _ := session["connection_id"].(string)
	if strings.TrimSpace(title) == "" {
		title = sessionID
	}

	var existingBot *BotProfile
	if strings.TrimSpace(req.BotID) != "" {
		if b, findErr := s.registry.Find(ctx, req.BotID); findErr == nil {
			existingBot = b
		}
	}

	inspectWorkspace := workspace
	inspectConnID := connID
	if existingBot != nil {
		inspectWorkspace = existingBot.Workspace
		inspectConnID = existingBot.ConnectionID
	}

	var existingAgentsMD string
	var hasExistingAgentsMD bool
	var existingSkills []BotSkillSummary
	if strings.TrimSpace(inspectWorkspace) != "" && s.options.Resolver != nil {
		probeBot := BotProfile{Workspace: inspectWorkspace, ConnectionID: inspectConnID}
		if _, content, exists, readErr := s.ReadAgentsMD(ctx, probeBot); readErr == nil && exists {
			existingAgentsMD = content
			hasExistingAgentsMD = true
		}
		if skills, listErr := s.ListSkills(ctx, probeBot, false); listErr == nil {
			for _, sk := range skills {
				existingSkills = append(existingSkills, sk.BotSkillSummary)
			}
		}
	}
	if existingSkills == nil {
		existingSkills = []BotSkillSummary{}
	}

	maxMsgs := req.MaxMessages
	if maxMsgs <= 0 || maxMsgs > 120 {
		maxMsgs = 80
	}
	var messages []map[string]any
	if s.options.HistoryReader != nil {
		if loaded, readErr := s.options.HistoryReader(ctx, agentID, sessionID, maxMsgs); readErr == nil {
			messages = loaded
		}
	}

	transcript := formatSessionTranscript(messages, 20000)
	usedSkills := detectSessionUsedSkills(messages, existingSkills)
	generateSkill := req.GenerateSkill != nil && *req.GenerateSkill

	suggestedName := strings.TrimSpace(req.Name)
	if suggestedName == "" && existingBot != nil {
		suggestedName = existingBot.Name
	}
	if suggestedName == "" {
		suggestedName = title + " Bot"
	}
	suggestedSlug := ""
	if existingBot != nil {
		suggestedSlug = existingBot.Slug
	}
	if suggestedSlug == "" {
		suggestedSlug = NormalizeSlug(workspaceBasename(inspectWorkspace))
	}
	if suggestedSlug == "" {
		suggestedSlug = NormalizeSlug(suggestedName)
	}
	if suggestedSlug == "" {
		suggestedSlug = "bot-" + randomHex(2)
	}

	fallbackSkillSlug := ""
	fallbackSkillDesc := ""
	if generateSkill {
		fallbackSkillSlug = suggestedSlug + "-workflow"
		if len(existingSkills) == 1 {
			fallbackSkillSlug = existingSkills[0].Slug
		}
		fallbackSkillDesc = fmt.Sprintf("Standard workflow distilled from session %s.", title)
	}
	fallbackDesc := fmt.Sprintf("沉淀自会话「%s」的 Bot，在 %s 中按标准流程交付任务。", title, nonEmpty(workspaceBasename(inspectWorkspace), "专属工作区"))
	fallbackAgentsMD, fallbackSkillMD := buildFallbackDistillDocs(
		suggestedName,
		fallbackDesc,
		title,
		inspectWorkspace,
		transcript,
		existingAgentsMD,
		req.Hint,
		usedSkills,
		existingSkills,
		generateSkill,
		fallbackSkillSlug,
	)

	baseResp := &DistillResponse{
		OK:                   true,
		Mode:                 "template",
		SourceSessionKey:     agentID + "::" + sessionID,
		SourceSessionTitle:   title,
		Workspace:            inspectWorkspace,
		ConnectionID:         normalizeConnectionID(inspectConnID),
		DefaultAgentID:       nonEmpty(botAgentOr(existingBot, agentID), agentID),
		SuggestedName:        suggestedName,
		SuggestedSlug:        suggestedSlug,
		SuggestedDescription: fallbackDesc,
		SuggestedTags:        inferDefaultTags(suggestedName, title, req.Hint),
		AgentsMD:             fallbackAgentsMD,
		SkillGenerated:       generateSkill,
		SkillSlug:            fallbackSkillSlug,
		SkillName:            fallbackSkillSlug,
		SkillDescription:     fallbackSkillDesc,
		SkillMD:              fallbackSkillMD,
		ExistingAgentsMD:     existingAgentsMD,
		HasExistingAgentsMD:  hasExistingAgentsMD,
		ExistingSkills:       existingSkills,
		UsedSkills:           usedSkills,
		MessageCount:         len(messages),
	}

	llmCfg := s.resolveLLM(ctx, req.ProviderID, req.Model)
	llmConfigured := llmCfg.IsConfigured()
	baseResp.LLMConfigured = llmConfigured
	baseResp.Model = llmCfg.Model

	if strings.EqualFold(strings.TrimSpace(req.Mode), "template") || !llmConfigured {
		if !llmConfigured {
			if generateSkill {
				baseResp.Message = "未配置通用 AI 模型（可在设置中选择已接入 Agent 或填写 API Key），已基于会话记录生成结构化 AGENTS.md 与 SKILL.md 草案，可编辑后保存。"
			} else {
				baseResp.Message = "未配置通用 AI 模型（可在设置中选择已接入 Agent 或填写 API Key），已基于会话记录与关联 Skill 生成结构化 AGENTS.md 草案，可编辑后保存。"
			}
		}
		return baseResp, nil
	}

	var systemPrompt string
	if generateSkill {
		systemPrompt = strings.TrimSpace(`你是 Astrorder「Bot 经验沉淀专家」。请仔细阅读这段历史会话记录，提炼可长期复用的工作经验、工具链约束、质量验收标准与避坑要点，生成：
1. AGENTS.md（工作区级角色宪章：Bot 身份、核心能力、技能调用指引、默认行为准则、质量红线、输入与跨会话协作约定）
2. SKILL.md（带 YAML frontmatter 的标准作业流程 SOP，存放于 .agents/skills/<skill_slug>/SKILL.md）

严格输出一个合法 JSON 对象（不要输出 Markdown 代码围栏或额外解释）：
{
  "name": "专业简洁的 Bot 名称",
  "slug": "小写英文 kebab-case 标识",
  "description": "一句话说明此 Bot 擅长什么任务（<= 60 字）",
  "tags": ["标签1", "标签2"],
  "agents_md": "完整 AGENTS.md Markdown 文本",
  "skill_slug": "小写英文 kebab-case 技能目录名",
  "skill_name": "与 skill_slug 一致的技能名",
  "skill_description": "简洁的一句话技能触发说明（<= 140 字）",
  "skill_md": "带 --- frontmatter (name, description) --- 的完整 SKILL.md 文本"
}

关键要求：
- 只沉淀真正可复用的经验、命令规范、参数经验、踩坑记录和验收步骤；剔除本次一次性具体业务文案或临时路径。
- 必须在 AGENTS.md 中包含「技能调用指引」章节：明确列出本会话实际用到/读取过的 Skill、工作区已有相关 Skill 以及本次沉淀的 Skill 的确切名称，清晰说明各自在什么场景/环节优先调用，引导 Bot 先读取对应 SKILL.md 并按其流程执行，不要在 AGENTS.md 里冗长重复 SKILL.md 的底层细节。
- 如果工作区已有 AGENTS.md，在其基础上做增补与提炼，保留已有有效规则。
- 在 AGENTS.md 的协作约定中写明：优先消费派单说明和 inbox/<job-id>/ 内推送的素材，不需要默认跨项目翻找；若执行中确需补充源项目材料或向发起方确认，允许按需读取或回问。`)
	} else {
		systemPrompt = strings.TrimSpace(`你是 Astrorder「Bot 经验沉淀专家」。请仔细阅读这段历史会话记录，提炼可长期复用的工作经验、技能调度指引、质量验收标准与避坑要点，生成高质量的 AGENTS.md（本次无需生成新的 SKILL.md，请充分引导 Bot 复用工作区与本会话中已使用的 Skill）。

严格输出一个合法 JSON 对象（不要输出 Markdown 代码围栏或额外解释）：
{
  "name": "专业简洁的 Bot 名称",
  "slug": "小写英文 kebab-case 标识",
  "description": "一句话说明此 Bot 擅长什么任务（<= 60 字）",
  "tags": ["标签1", "标签2"],
  "agents_md": "完整 AGENTS.md Markdown 文本"
}

关键要求：
- 只沉淀真正可复用的角色定位、工作流程决策、质量红线与避坑要点；剔除本次一次性具体业务文案或临时路径。
- 必须在 AGENTS.md 中包含专门的「技能调用指引」章节：如果提供了「本会话实际使用过的 Skills」或「工作区已有 Skills」（或对话记录中实际读取/调用了某 Skill），必须逐一写出这些 Skill 的确切名称（如 ` + "`skill-name`" + `）、适用场景与先后顺序，明确引导 Bot 在执行任务时优先读取并遵循对应 Skill 的 SKILL.md，而不是把 Skill 的底层步骤重复写进 AGENTS.md。
- 如果工作区已有 AGENTS.md，在其基础上做增补与提炼，保留已有有效规则。
- 在 AGENTS.md 的协作约定中写明：优先消费派单说明和 inbox/<job-id>/ 内推送的素材，不需要默认跨项目翻找；若执行中确需补充源项目材料或向发起方确认，允许按需读取或回问。`)
	}

	var userBuf strings.Builder
	userBuf.WriteString(fmt.Sprintf("源会话标题：%s\n工作区：%s\n", title, inspectWorkspace))
	if existingBot != nil {
		userBuf.WriteString(fmt.Sprintf("目标反哺 Bot：%s (%s)\n", existingBot.Name, existingBot.Slug))
	}
	if strings.TrimSpace(req.Hint) != "" {
		userBuf.WriteString("用户重点要求：" + strings.TrimSpace(req.Hint) + "\n")
	}
	if len(usedSkills) > 0 {
		skillDescBySlug := make(map[string]string, len(existingSkills))
		for _, sk := range existingSkills {
			skillDescBySlug[strings.ToLower(sk.Slug)] = sk.Description
		}
		userBuf.WriteString("\n【本会话实际使用 / 提及的 Skills（请在 AGENTS.md 中明确引导调用）】\n")
		for _, slug := range usedSkills {
			if desc := skillDescBySlug[strings.ToLower(slug)]; desc != "" {
				userBuf.WriteString(fmt.Sprintf("- %s: %s\n", slug, desc))
			} else {
				userBuf.WriteString(fmt.Sprintf("- %s\n", slug))
			}
		}
	}
	if len(existingSkills) > 0 {
		userBuf.WriteString("\n【工作区已有 Skills】\n")
		for _, sk := range existingSkills {
			userBuf.WriteString(fmt.Sprintf("- %s: %s\n", sk.Slug, sk.Description))
		}
	}
	if hasExistingAgentsMD {
		userBuf.WriteString("\n【工作区当前 AGENTS.md】\n" + truncateRunes(existingAgentsMD, 4000) + "\n")
	}
	userBuf.WriteString("\n【会话对话记录摘要】\n")
	if strings.TrimSpace(transcript) == "" {
		userBuf.WriteString("（当前会话尚无可读消息历史，请结合标题、工作区、已有 Skills 和用户要求生成初始草案。）\n")
	} else {
		userBuf.WriteString(transcript + "\n")
	}

	callCtx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	res, err := llmcompat.CompleteChat(callCtx, s.httpClient, llmCfg.ToChatRequest(4096, []llmcompat.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userBuf.String()},
	}))
	if err != nil || res.Status >= 400 || strings.TrimSpace(res.Content) == "" {
		if err != nil {
			baseResp.Message = fmt.Sprintf("调用辅助 AI 模型失败（%v），已回退为结构化模板草案。", err)
		} else if detail := llmcompat.ExtractErrorMessage(res.Raw); detail != "" {
			baseResp.Message = fmt.Sprintf("辅助 AI 模型返回错误（%s），已回退为结构化模板草案。", detail)
		} else {
			baseResp.Message = "辅助 AI 模型返回空响应，已回退为结构化模板草案。"
		}
		return baseResp, nil
	}

	var parsed struct {
		Name             string   `json:"name"`
		Slug             string   `json:"slug"`
		Description      string   `json:"description"`
		Tags             []string `json:"tags"`
		AgentsMD         string   `json:"agents_md"`
		SkillSlug        string   `json:"skill_slug"`
		SkillName        string   `json:"skill_name"`
		SkillDescription string   `json:"skill_description"`
		SkillMD          string   `json:"skill_md"`
	}
	if err := unmarshalLooseJSON(res.Content, &parsed); err != nil {
		baseResp.Message = "辅助 AI 输出未按标准 JSON 格式返回，已回退为结构化模板草案。"
		return baseResp, nil
	}

	baseResp.Mode = "llm"
	baseResp.Model = nonEmpty(res.Model, llmCfg.Model)
	if strings.TrimSpace(parsed.Name) != "" && existingBot == nil {
		baseResp.SuggestedName = strings.TrimSpace(parsed.Name)
	}
	if slug := NormalizeSlug(parsed.Slug); slug != "" && existingBot == nil {
		baseResp.SuggestedSlug = slug
	}
	if strings.TrimSpace(parsed.Description) != "" {
		baseResp.SuggestedDescription = strings.TrimSpace(parsed.Description)
	}
	if tags := cleanTags(parsed.Tags); len(tags) > 0 {
		baseResp.SuggestedTags = tags
	}
	if strings.TrimSpace(parsed.AgentsMD) != "" {
		baseResp.AgentsMD = strings.TrimSpace(parsed.AgentsMD)
	}
	if generateSkill {
		if skSlug := NormalizeSlug(parsed.SkillSlug); skSlug != "" {
			baseResp.SkillSlug = skSlug
			baseResp.SkillName = nonEmpty(strings.TrimSpace(parsed.SkillName), skSlug)
		}
		if strings.TrimSpace(parsed.SkillDescription) != "" {
			baseResp.SkillDescription = strings.TrimSpace(parsed.SkillDescription)
		}
		if strings.TrimSpace(parsed.SkillMD) != "" {
			baseResp.SkillMD = ensureSkillFrontmatter(strings.TrimSpace(parsed.SkillMD), baseResp.SkillSlug, baseResp.SkillDescription)
		}
	}
	return baseResp, nil
}

func parseSessionRef(sessionKey, agentID, sessionID string) (string, string, error) {
	if strings.Contains(sessionKey, "::") {
		parts := strings.SplitN(strings.TrimSpace(sessionKey), "::", 2)
		if strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
		}
	}
	aid := strings.TrimSpace(agentID)
	sid := strings.TrimSpace(sessionID)
	if aid != "" && sid != "" {
		return aid, sid, nil
	}
	return "", "", fmt.Errorf("%w: 请选择有效的源会话（session_key）", ErrInvalid)
}

func formatSessionTranscript(messages []map[string]any, maxChars int) string {
	if len(messages) == 0 {
		return ""
	}
	var lines []string
	total := 0
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		kind, _ := msg["kind"].(string)
		text, _ := msg["text"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if kind == "thinking" || kind == "reasoning" {
			continue
		}
		label := strings.ToUpper(nonEmpty(role, "msg"))
		entry := fmt.Sprintf("[%s] %s", label, truncateRunes(text, 1200))
		if total+len(entry) > maxChars {
			break
		}
		lines = append(lines, entry)
		total += len(entry) + 1
	}
	return strings.Join(lines, "\n\n")
}

func buildTemplateAgentsMD(name, desc, instructions, existing string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s — Bot 工作规范\n\n", name))
	b.WriteString(fmt.Sprintf("> %s\n\n", desc))
	b.WriteString("## 角色定位与核心能力\n\n")
	if strings.TrimSpace(instructions) != "" {
		b.WriteString(strings.TrimSpace(instructions) + "\n\n")
	} else {
		b.WriteString(fmt.Sprintf("- 作为独立的「%s」Bot 工作台，接收单次任务派单并在隔离的干净会话中完成端到端交付。\n", name))
		b.WriteString("- 严格遵循本工作区 `.agents/skills/` 中沉淀的 SOP、脚本与质量验收标准。\n\n")
	}
	b.WriteString("## 输入与跨会话协作约定\n\n")
	b.WriteString("1. **优先消费已推送材料**：先阅读派单说明，并检查工作区 `inbox/<job-id>/` 中已推送的附件、素材与参考稿，默认无需跨项目翻找无关源码。\n")
	b.WriteString("2. **按需回溯补充**：若执行中判断确需更多源项目背景、配置或素材，可按需读取发起方附带的上下文目录，或通过 `astrorder-agent sessions read/send` 向发起方确认。\n")
	b.WriteString("3. **任务隔离与产物归档**：每次任务尽量在独立任务目录中产出交付物，避免覆盖历史任务的成品。\n\n")
	b.WriteString("## 交付与自检标准\n\n")
	b.WriteString("- 输出最终交付物的明确文件路径、核心变更摘要与必要的验证结果。\n")
	b.WriteString("- 若存在未满足的前置条件或阻塞项，清晰列出缺失材料及建议方案，不虚构数据或结果。\n")
	if trimmed := strings.TrimSpace(existing); trimmed != "" {
		b.WriteString("\n---\n\n## 原有工作区约束（参考保留）\n\n")
		b.WriteString(trimmed + "\n")
	}
	return b.String()
}

func detectSessionUsedSkills(messages []map[string]any, existingSkills []BotSkillSummary) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(existingSkills))
	add := func(raw string) {
		slug := strings.TrimSpace(raw)
		if slug == "" {
			return
		}
		key := strings.ToLower(slug)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, slug)
	}

	var blob strings.Builder
	for _, msg := range messages {
		if raw, err := json.Marshal(msg); err == nil {
			s := string(raw)
			for _, match := range skillPathPattern.FindAllStringSubmatch(s, -1) {
				if len(match) >= 2 {
					add(match[1])
				}
			}
			blob.WriteString(strings.ToLower(s))
			blob.WriteByte('\n')
		}
	}
	haystack := blob.String()
	for _, sk := range existingSkills {
		if sk.Slug != "" && strings.Contains(haystack, strings.ToLower(sk.Slug)) {
			add(sk.Slug)
		} else if sk.Name != "" && strings.Contains(haystack, strings.ToLower(sk.Name)) {
			add(sk.Slug)
		}
	}
	return out
}

func buildFallbackDistillDocs(
	name, desc, sessionTitle, workspace, transcript, existingAgentsMD, hint string,
	usedSkills []string,
	existingSkills []BotSkillSummary,
	generateSkill bool,
	skillSlug string,
) (string, string) {
	instructions := fmt.Sprintf("沉淀自历史会话「%s」（工作区 `%s`）的专业实践。", sessionTitle, workspace)
	if strings.TrimSpace(hint) != "" {
		instructions += "\n\n重点关注：\n" + strings.TrimSpace(hint)
	}

	skillDescBySlug := make(map[string]string, len(existingSkills))
	for _, sk := range existingSkills {
		skillDescBySlug[strings.ToLower(sk.Slug)] = sk.Description
	}
	var guidanceLines []string
	listed := make(map[string]struct{})
	addSkillGuide := func(slug, note string) {
		key := strings.ToLower(strings.TrimSpace(slug))
		if key == "" {
			return
		}
		if _, ok := listed[key]; ok {
			return
		}
		listed[key] = struct{}{}
		if note != "" {
			guidanceLines = append(guidanceLines, fmt.Sprintf("- **`%s`**：%s", slug, note))
		} else {
			guidanceLines = append(guidanceLines, fmt.Sprintf("- **`%s`**：处理对应环节任务前，优先读取并遵循该 Skill 的 `SKILL.md` 规范。", slug))
		}
	}
	for _, slug := range usedSkills {
		addSkillGuide(slug, skillDescBySlug[strings.ToLower(slug)])
	}
	for _, sk := range existingSkills {
		addSkillGuide(sk.Slug, sk.Description)
	}
	if generateSkill && strings.TrimSpace(skillSlug) != "" {
		addSkillGuide(skillSlug, "本次沉淀的 Bot 标准作业流程 Skill，接收同类任务时优先读取并按步骤执行。")
	}
	if len(guidanceLines) > 0 {
		instructions += "\n\n### 技能调用指引\n\n执行任务时优先读取并遵循以下关联 Skill（避免跳过既有 SOP）：\n" + strings.Join(guidanceLines, "\n")
	}

	agentsMD := buildTemplateAgentsMD(name, desc, instructions, existingAgentsMD)
	if !generateSkill {
		return agentsMD, ""
	}

	if strings.TrimSpace(skillSlug) == "" {
		skillSlug = NormalizeSlug(name)
	}
	if skillSlug == "" {
		skillSlug = "bot-workflow"
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", skillSlug))
	sb.WriteString(fmt.Sprintf("description: Reusable workflow distilled from session %s.\n", strings.ReplaceAll(sessionTitle, "\n", " ")))
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("# %s 标准作业流程\n\n", name))
	sb.WriteString("## 适用场景\n\n")
	sb.WriteString(fmt.Sprintf("- 收到与「%s」同类型的专业交付任务时使用。\n\n", sessionTitle))
	sb.WriteString("## 执行步骤\n\n")
	sb.WriteString("1. 检查派单目标、交付格式与 `inbox/<job-id>/` 中附带的参考素材；若信息不足，按需向发起方确认或读取发起方上下文。\n")
	sb.WriteString("2. 复用本工作区既有脚本、模板与参数规范执行生产与处理。\n")
	sb.WriteString("3. 按质量标准完成自检，输出最终交付物路径与摘要。\n")
	if strings.TrimSpace(hint) != "" {
		sb.WriteString("\n## 特别注意事项\n\n" + strings.TrimSpace(hint) + "\n")
	}
	if strings.TrimSpace(transcript) != "" {
		sb.WriteString("\n## 源会话关键摘录（供人工精简）\n\n")
		sb.WriteString(truncateRunes(transcript, 1800) + "\n")
	}
	return agentsMD, sb.String()
}

func ensureSkillFrontmatter(md, slug, desc string) string {
	trimmed := strings.TrimSpace(md)
	if strings.HasPrefix(trimmed, "---\n") {
		return trimmed
	}
	safeDesc := strings.ReplaceAll(strings.TrimSpace(desc), "\n", " ")
	if safeDesc == "" {
		safeDesc = fmt.Sprintf("Standard workflow for %s.", slug)
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", slug, safeDesc, trimmed)
}

func unmarshalLooseJSON(raw string, out any) error {
	cleaned := stripOuterMarkdownFence(raw)
	if err := json.Unmarshal([]byte(cleaned), out); err == nil {
		return nil
	}
	start := strings.IndexByte(cleaned, '{')
	end := strings.LastIndexByte(cleaned, '}')
	if start >= 0 && end > start {
		return json.Unmarshal([]byte(cleaned[start:end+1]), out)
	}
	return fmt.Errorf("invalid json payload")
}

func stripOuterMarkdownFence(s string) string {
	trimmed := strings.TrimSpace(s)
	for _, prefix := range []string{"```json\n", "```JSON\n", "```markdown\n", "```md\n", "```\n"} {
		if strings.HasPrefix(trimmed, prefix) {
			trimmed = strings.TrimPrefix(trimmed, prefix)
			trimmed = strings.TrimSuffix(trimmed, "```")
			return strings.TrimSpace(trimmed)
		}
	}
	return trimmed
}

func firstNonEmptyLine(s, fallback string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return fallback
}

func inferDefaultTags(parts ...string) []string {
	joined := strings.ToLower(strings.Join(parts, " "))
	var tags []string
	add := func(t string) { tags = append(tags, t) }
	if strings.Contains(joined, "视频") || strings.Contains(joined, "video") || strings.Contains(joined, "宣传片") {
		add("视频制作")
	}
	if strings.Contains(joined, "文档") || strings.Contains(joined, "ppt") || strings.Contains(joined, "方案") || strings.Contains(joined, "周报") {
		add("文档写作")
	}
	if strings.Contains(joined, "审计") || strings.Contains(joined, "审查") || strings.Contains(joined, "review") {
		add("审查")
	}
	if strings.Contains(joined, "数据") || strings.Contains(joined, "sql") || strings.Contains(joined, "报表") {
		add("数据分析")
	}
	if len(tags) == 0 {
		add("Bot")
	}
	return tags
}

func botAgentOr(b *BotProfile, fallback string) string {
	if b != nil && strings.TrimSpace(b.DefaultAgentID) != "" {
		return b.DefaultAgentID
	}
	return fallback
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
