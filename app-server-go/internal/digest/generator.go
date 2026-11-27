package digest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/officepersona"
)

// GenerateRequest is accepted by POST /api/v1/digest/generate.
type GenerateRequest struct {
	From              string       `json:"from"`
	To                string       `json:"to"`
	TemplateID        string       `json:"template_id"`
	CustomPrompt      string       `json:"custom_prompt,omitempty"`
	Title             string       `json:"title,omitempty"`
	Items             []DigestItem `json:"items"`
	ManualNotes       string       `json:"manual_notes,omitempty"`
	RefineInstruction string       `json:"refine_instruction,omitempty"`
	ExistingMarkdown  string       `json:"existing_markdown,omitempty"`
	Mode              string       `json:"mode,omitempty"`        // "llm" (default) or "prompt_only"
	ProviderID        string       `json:"provider_id,omitempty"` // Optional AI Provider override
	Model             string       `json:"model,omitempty"`       // Optional model override
}

// GenerateResponse is returned by POST /api/v1/digest/generate.
type GenerateResponse struct {
	OK              bool   `json:"ok"`
	Mode            string `json:"mode"` // "llm" or "prompt_only"
	LLMConfigured   bool   `json:"llm_configured"`
	Model           string `json:"model,omitempty"`
	TemplateID      string `json:"template_id"`
	TemplateName    string `json:"template_name"`
	Title           string `json:"title"`
	Markdown        string `json:"markdown"`
	AssembledPrompt string `json:"assembled_prompt"`
	Message         string `json:"message,omitempty"`
}

// Generate turns selected work evidence items and optional manual notes into a
// formatted report using the configured helper LLM (or returns the assembled prompt
// + structured draft when LLM is unconfigured or prompt_only mode is requested).
func (h *handler) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	tpl := h.findTemplate(ctx, req.TemplateID)
	systemPrompt := tpl.SystemPrompt
	if strings.TrimSpace(req.CustomPrompt) != "" {
		systemPrompt = strings.TrimSpace(req.CustomPrompt)
	}
	if persona := officepersona.PromptSection(officepersona.Load(ctx, h.store)); persona != "" {
		systemPrompt += "\n\n" + persona
	}

	selected := make([]DigestItem, 0, len(req.Items))
	for _, it := range req.Items {
		if it.Selected {
			selected = append(selected, it)
		}
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = buildDefaultReportTitle(tpl.Name, req.From, req.To)
	}

	userPrompt := buildDigestUserPrompt(req, selected, title)
	assembledPrompt := fmt.Sprintf("【角色与格式要求】\n%s\n\n%s", systemPrompt, userPrompt)

	var llmCfg aiprovider.ResolvedModel
	if strings.TrimSpace(req.ProviderID) != "" {
		if resolved, ok := aiprovider.Resolve(ctx, h.store, req.ProviderID, req.Model); ok {
			llmCfg = resolved
		}
	}
	if !llmCfg.IsConfigured() && strings.TrimSpace(req.ProviderID) == "" {
		llmCfg = loadLLMConfig(ctx, h.store)
		if m := strings.TrimSpace(req.Model); m != "" {
			llmCfg.Model = m
		}
	}
	llmConfigured := llmCfg.IsConfigured()
	wantMode := strings.ToLower(strings.TrimSpace(req.Mode))

	if wantMode == "prompt_only" || !llmConfigured {
		draft := buildOfflineMarkdownDraft(title, tpl.Name, req.From, req.To, selected, req.ManualNotes)
		msg := "已拼装完整素材提示词，可直接复制或交给会话助手生成。"
		if !llmConfigured && wantMode != "prompt_only" {
			msg = "未配置通用 AI 模型（可在「设置 → AI 供应商」直接选择已接入 Agent 或填写 API Key），已生成离线素材草稿与完整提示词，也可一键发给对话润色成稿。"
		}
		return &GenerateResponse{
			OK:              true,
			Mode:            "prompt_only",
			LLMConfigured:   llmConfigured,
			Model:           llmCfg.Model,
			TemplateID:      tpl.ID,
			TemplateName:    tpl.Name,
			Title:           title,
			Markdown:        draft,
			AssembledPrompt: assembledPrompt,
			Message:         msg,
		}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	res, err := llmcompat.CompleteChat(callCtx, h.httpClient, llmCfg.ToChatRequest(4096, []llmcompat.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}))
	if err != nil {
		return nil, fmt.Errorf("调用 AI 供应商模型失败：%w", err)
	}
	if res.Status >= 400 || strings.TrimSpace(res.Content) == "" {
		detail := llmcompat.ExtractErrorMessage(res.Raw)
		if detail != "" {
			return nil, fmt.Errorf("AI 供应商返回错误（HTTP %d）：%s", res.Status, detail)
		}
		return nil, fmt.Errorf("AI 供应商返回异常（HTTP %d）", res.Status)
	}

	return &GenerateResponse{
		OK:              true,
		Mode:            "llm",
		LLMConfigured:   true,
		Model:           res.Model,
		TemplateID:      tpl.ID,
		TemplateName:    tpl.Name,
		Title:           title,
		Markdown:        strings.TrimSpace(res.Content),
		AssembledPrompt: assembledPrompt,
	}, nil
}

func buildDefaultReportTitle(templateName, from, to string) string {
	fromDate := formatDateShort(from)
	toDate := formatDateShort(to)
	if fromDate != "" && toDate != "" {
		if fromDate == toDate {
			return fmt.Sprintf("%s（%s）", templateName, fromDate)
		}
		return fmt.Sprintf("%s（%s ~ %s）", templateName, fromDate, toDate)
	}
	return fmt.Sprintf("%s（%s）", templateName, time.Now().Format("2006-01-02"))
}

func formatDateShort(iso string) string {
	if t, ok := parseFlexTimeString(iso, false); ok {
		return t.In(time.Local).Format("2006-01-02")
	}
	return ""
}

func buildDigestUserPrompt(req GenerateRequest, selected []DigestItem, title string) string {
	var b strings.Builder

	if strings.TrimSpace(req.RefineInstruction) != "" && strings.TrimSpace(req.ExistingMarkdown) != "" {
		b.WriteString(fmt.Sprintf("请根据以下润色要求改写已有报告，并保持基于事实、输出完整 Markdown：\n\n"))
		b.WriteString("【润色要求】\n")
		b.WriteString(strings.TrimSpace(req.RefineInstruction))
		b.WriteString("\n\n【已有报告原文】\n")
		b.WriteString(strings.TrimSpace(req.ExistingMarkdown))
		if len(selected) > 0 || strings.TrimSpace(req.ManualNotes) != "" {
			b.WriteString("\n\n【补充原始素材参考（严禁偏离事实）】\n")
			writeGroupedMaterials(&b, selected, req.ManualNotes)
		}
		return b.String()
	}

	b.WriteString(fmt.Sprintf("报告标题建议：%s\n", title))
	if f, t := formatDateShort(req.From), formatDateShort(req.To); f != "" && t != "" {
		b.WriteString(fmt.Sprintf("统计周期：%s 至 %s\n", f, t))
	}
	b.WriteString("\n以下是采集并经用户勾选确认的真实工作素材：\n\n")
	writeGroupedMaterials(&b, selected, req.ManualNotes)
	if strings.TrimSpace(req.RefineInstruction) != "" {
		b.WriteString("\n【额外写作偏好】\n")
		b.WriteString(strings.TrimSpace(req.RefineInstruction))
		b.WriteString("\n")
	}
	b.WriteString("\n请直接输出最终 Markdown 正文，不要在最外层包裹 ```markdown 代码块。")
	return b.String()
}

func writeGroupedMaterials(b *strings.Builder, items []DigestItem, manualNotes string) {
	byProj := make(map[string][]DigestItem)
	projNames := make([]string, 0)
	for _, it := range items {
		pName := strings.TrimSpace(it.ProjectName)
		if pName == "" {
			pName = "通用工作区"
		}
		if _, exists := byProj[pName]; !exists {
			projNames = append(projNames, pName)
		}
		byProj[pName] = append(byProj[pName], it)
	}
	sort.Strings(projNames)

	for _, pName := range projNames {
		b.WriteString(fmt.Sprintf("### 项目：%s\n", pName))
		for _, it := range byProj[pName] {
			kindLabel := digestKindLabel(it.Kind)
			dateLabel := formatDateShort(it.Timestamp)
			if dateLabel != "" {
				b.WriteString(fmt.Sprintf("- [%s | %s] **%s**", kindLabel, dateLabel, it.Title))
			} else {
				b.WriteString(fmt.Sprintf("- [%s] **%s**", kindLabel, it.Title))
			}
			if s := strings.TrimSpace(it.Summary); s != "" {
				b.WriteString("\n  " + strings.ReplaceAll(s, "\n", "\n  "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if notes := strings.TrimSpace(manualNotes); notes != "" {
		b.WriteString("### 线下沟通 / 手动补充事项\n")
		b.WriteString(notes)
		b.WriteString("\n")
	}
}

func buildOfflineMarkdownDraft(title, templateName, from, to string, items []DigestItem, manualNotes string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", title))
	if f, t := formatDateShort(from), formatDateShort(to); f != "" && t != "" {
		b.WriteString(fmt.Sprintf("> 统计周期：%s ~ %s\n\n", f, t))
	}
	if len(items) == 0 && strings.TrimSpace(manualNotes) == "" {
		b.WriteString("当前未勾选任何工作素材，请在左侧采集并勾选需要汇总的条目。\n")
		return b.String()
	}

	byProj := make(map[string][]DigestItem)
	projNames := make([]string, 0)
	for _, it := range items {
		pName := strings.TrimSpace(it.ProjectName)
		if pName == "" {
			pName = "通用工作区"
		}
		if _, ok := byProj[pName]; !ok {
			projNames = append(projNames, pName)
		}
		byProj[pName] = append(byProj[pName], it)
	}
	sort.Strings(projNames)

	b.WriteString("## 分项目工作进展\n\n")
	for _, pName := range projNames {
		b.WriteString(fmt.Sprintf("### %s\n\n", pName))
		for _, it := range byProj[pName] {
			b.WriteString(fmt.Sprintf("- **【%s】%s**", digestKindLabel(it.Kind), it.Title))
			if s := strings.TrimSpace(it.Summary); s != "" {
				firstLine := strings.Split(s, "\n")[0]
				b.WriteString(" — " + firstLine)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if notes := strings.TrimSpace(manualNotes); notes != "" {
		b.WriteString("## 补充与线下事项\n\n")
		b.WriteString(notes + "\n\n")
	}

	b.WriteString("## 后续计划\n\n- （待补充）\n")
	return b.String()
}

func digestKindLabel(kind string) string {
	switch kind {
	case "session":
		return "会话"
	case "git_commit":
		return "Git 提交"
	case "task":
		return "任务"
	case "checkpoint":
		return "代码变更"
	case "sql_audit":
		return "SQL 执行"
	case "meeting":
		return "会议纪要"
	case "office_doc":
		return "文档产出"
	default:
		return "记录"
	}
}
