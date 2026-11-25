package meetings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/officepersona"
)

func (h *handler) resolveSummaryLLMConfig(ctx context.Context) (aiprovider.ResolvedModel, MeetingSettings) {
	settings, _ := h.loadRawSettings(ctx)
	switch settings.ModelSource {
	case "provider":
		if resolved, ok := aiprovider.Resolve(ctx, h.store, settings.ProviderID, settings.Model); ok {
			return resolved, settings
		}
	case "custom":
		return aiprovider.ResolvedModel{
			Protocol:  llmcompat.ProtocolOpenAI,
			BaseURL:   settings.BaseURL,
			APIKey:    strings.TrimSpace(settings.APIKey),
			Model:     settings.Model,
			Reasoning: settings.Reasoning,
		}, settings
	}
	return aiprovider.ResolveDefault(ctx, h.store), settings
}

// TestSettingsConnectivity validates the supplied (or persisted) meeting summary model config.
func (h *handler) TestSettingsConnectivity(ctx context.Context, incoming *MeetingSettings) SettingsTestResponse {
	started := time.Now()
	var cfg aiprovider.ResolvedModel

	if incoming != nil {
		switch strings.ToLower(strings.TrimSpace(incoming.ModelSource)) {
		case "provider":
			resolved, ok := aiprovider.Resolve(ctx, h.store, incoming.ProviderID, incoming.Model)
			if !ok {
				return SettingsTestResponse{
					OK:        false,
					Model:     strings.TrimSpace(incoming.Model),
					LatencyMs: time.Since(started).Milliseconds(),
					Error:     "所选 AI 供应商不存在或已被删除",
				}
			}
			cfg = resolved
		case "custom":
			existing, _ := h.loadRawSettings(ctx)
			apiKey := strings.TrimSpace(incoming.APIKey)
			if apiKey == "" && !incoming.APIKeyClear {
				apiKey = strings.TrimSpace(existing.APIKey)
			}
			norm := normalizeMeetingSettings(*incoming)
			cfg = aiprovider.ResolvedModel{
				Protocol:  llmcompat.ProtocolOpenAI,
				BaseURL:   norm.BaseURL,
				APIKey:    apiKey,
				Model:     norm.Model,
				Reasoning: norm.Reasoning,
			}
		case "global":
			cfg = aiprovider.ResolveDefault(ctx, h.store)
		default:
			cfg, _ = h.resolveSummaryLLMConfig(ctx)
		}
	} else {
		cfg, _ = h.resolveSummaryLLMConfig(ctx)
	}

	if strings.TrimSpace(cfg.APIKey) == "" {
		return SettingsTestResponse{
			OK:        false,
			Model:     cfg.Model,
			LatencyMs: time.Since(started).Milliseconds(),
			Error:     "未配置 API Key",
		}
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return SettingsTestResponse{
			OK:        false,
			LatencyMs: time.Since(started).Milliseconds(),
			Error:     "请先选择或填写模型名称",
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	res, err := llmcompat.CompleteChat(callCtx, h.httpClient, llmcompat.ChatRequest{
		Protocol:  cfg.Protocol,
		BaseURL:   cfg.BaseURL,
		APIKey:    cfg.APIKey,
		Model:     cfg.Model,
		Reasoning: cfg.Reasoning,
		MaxTokens: 16,
		Messages:  []llmcompat.ChatMessage{{Role: "user", Content: "Reply with OK."}},
	})
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return SettingsTestResponse{OK: false, Model: cfg.Model, LatencyMs: latency, Error: err.Error()}
	}
	if res.Status >= 400 || strings.TrimSpace(res.Content) == "" {
		detail := llmcompat.ExtractErrorMessage(res.Raw)
		if detail == "" {
			detail = fmt.Sprintf("HTTP %d", res.Status)
		}
		return SettingsTestResponse{OK: false, Model: cfg.Model, LatencyMs: latency, Error: detail}
	}
	return SettingsTestResponse{OK: true, Model: res.Model, LatencyMs: latency}
}

type llmMeetingPayload struct {
	Title           string         `json:"title"`
	Summary         string         `json:"summary"`
	Topics          []MeetingTopic `json:"topics"`
	Decisions       []string       `json:"decisions"`
	Risks           []string       `json:"risks"`
	MinutesMarkdown string         `json:"minutes_markdown"`
	ActionItems     []struct {
		Text     string `json:"text"`
		Owner    string `json:"owner"`
		DueDate  string `json:"due_date"`
		Priority string `json:"priority"`
	} `json:"action_items"`
}

func styleGuidance(style string) string {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "detailed":
		return "风格要求：详尽纪要——完整保留每个议题的讨论上下文、方案对比与数据细节。"
	case "technical":
		return "风格要求：技术评审——突出架构决策、接口/性能约束、技术风险与明确负责的工程改造项。"
	default:
		return "风格要求：简洁汇报——结论先行、短句精炼，适合直接同步团队与管理者。"
	}
}

// Summarize extracts structured meeting minutes, evolving topics, decisions, risks,
// and action items (supporting both full synthesis and fast live incremental updates).
func (h *handler) Summarize(ctx context.Context, req SummarizeRequest) (*SummarizeResponse, error) {
	transcript := strings.TrimSpace(req.Transcript)
	newChunk := strings.TrimSpace(req.NewTranscript)
	extra := strings.TrimSpace(req.ExtraNotes)
	if transcript == "" && newChunk == "" && extra == "" {
		return nil, errors.New("请先开始实时录制或输入会议内容")
	}

	llmCfg, savedSettings := h.resolveSummaryLLMConfig(ctx)
	style := strings.TrimSpace(req.SummaryStyle)
	if style == "" {
		style = savedSettings.SummaryStyle
	}

	meetingDate := strings.TrimSpace(req.MeetingDate)
	if meetingDate == "" {
		meetingDate = time.Now().Format("2006-01-02")
	}
	participants := cleanStringList(req.Participants)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "会议纪要（" + meetingDate + "）"
	}

	isIncremental := req.Incremental && (strings.TrimSpace(req.PreviousSummary) != "" || len(req.PreviousTopics) > 0 || len(req.PreviousActionItems) > 0)

	modeRule := "当前为【完整终稿整体总结】：请基于完整转写原文通盘统筹整理最终版会议纪要，按主题归类合并相近或碎片化讨论，给出准确简洁的会议标题与核心结论，严禁使用“新增发言”“新增片段”“截至目前”“转写仍不完整”等增量过渡措辞。"
	if isIncremental {
		modeRule = "当前为【实时增量更新】：会议仍在进行中，请在保留既有有效结论与行动项的基础上融合最新发言，去重并合并同一议题，保持结构简洁稳定。"
	}

	systemPrompt := strings.TrimSpace(`你是一位专业会议秘书。请根据会议转写与笔记，输出客观、清晰、可直接流转的中文会议纪要与行动项。
` + styleGuidance(style) + `
` + modeRule + `
请严格仅输出一个合法的 JSON 对象（不要输出额外说明，不要用外层代码围栏包裹），结构如下：
{
  "title": "精准简洁的会议主题（若已有明确主题可保持或优化）",
  "summary": "2-4 句话概括核心结论与关键进展",
  "topics": [
    {"timestamp": "00:12", "title": "议题短标题", "detail": "1-2 句概括该议题讨论要点与结论"}
  ],
  "decisions": ["已达成决议1"],
  "risks": ["待确认风险或阻塞1"],
  "action_items": [
    {"text": "具体可执行任务", "owner": "负责人（未明确则留空）", "due_date": "YYYY-MM-DD（未明确则留空）", "priority": "high|medium|low"}
  ],
  "minutes_markdown": "结构化 Markdown 纪要正文"
}
要求：严禁编造转写中未提及的事实、人名或截止日期；尽量结合上下文纠正明显的语音识别同音错字但不篡改原意。`)
	if persona := officepersona.PromptSection(officepersona.Load(ctx, h.store)); persona != "" {
		systemPrompt += "\n\n" + persona
	}

	var userBuf strings.Builder
	userBuf.WriteString(fmt.Sprintf("会议主题：%s\n", title))
	userBuf.WriteString(fmt.Sprintf("会议日期：%s\n", meetingDate))
	if len(participants) > 0 {
		userBuf.WriteString("参会人员：" + strings.Join(participants, "、") + "\n")
	}

	if isIncremental {
		prevJSON, _ := json.Marshal(map[string]any{
			"summary":      req.PreviousSummary,
			"topics":       req.PreviousTopics,
			"decisions":    req.PreviousDecisions,
			"risks":        req.PreviousRisks,
			"action_items": req.PreviousActionItems,
		})
		userBuf.WriteString("\n【上一版实时纪要（请在其基础上增量融合更新，保留已有未重复条目）】\n")
		userBuf.WriteString(string(prevJSON) + "\n")
		if newChunk != "" {
			userBuf.WriteString("\n【最新实时转写片段】\n" + newChunk + "\n")
		} else if transcript != "" {
			userBuf.WriteString("\n【完整转写】\n" + transcript + "\n")
		}
	} else {
		if transcript != "" {
			userBuf.WriteString("\n【会议完整转写原文】\n" + transcript + "\n")
		} else if newChunk != "" {
			userBuf.WriteString("\n【会议完整转写原文】\n" + newChunk + "\n")
		}
	}
	if extra != "" {
		userBuf.WriteString("\n【补充笔记】\n" + extra + "\n")
	}
	userPrompt := userBuf.String()
	assembled := fmt.Sprintf("请根据以下会议转写整理结构化中文会议纪要（核心摘要、议题脉络、决议、风险与行动项）：\n\n%s", userPrompt)

	apiKey := strings.TrimSpace(llmCfg.APIKey)
	llmConfigured := apiKey != "" && strings.TrimSpace(llmCfg.Model) != ""
	wantMode := strings.ToLower(strings.TrimSpace(req.Mode))

	if wantMode == "prompt_only" || !llmConfigured {
		sourceText := transcript
		if sourceText == "" {
			sourceText = newChunk
		}
		draft := buildOfflineMeetingDraft(title, meetingDate, participants, sourceText, extra)
		msg := "已生成本地结构化草稿，可在右上角选择已配置的 AI 供应商模型启用 AI 实时总结。"
		return &SummarizeResponse{
			OK:              true,
			Mode:            "prompt_only",
			LLMConfigured:   llmConfigured,
			Model:           llmCfg.Model,
			Title:           draft.Title,
			Summary:         draft.Summary,
			Topics:          draft.Topics,
			Decisions:       draft.Decisions,
			Risks:           draft.Risks,
			ActionItems:     draft.ActionItems,
			MinutesMarkdown: draft.MinutesMarkdown,
			AssembledPrompt: assembled,
			Message:         msg,
		}, nil
	}

	maxTokens := 3072
	timeout := 90 * time.Second
	if req.Incremental {
		maxTokens = 2048
		timeout = 45 * time.Second
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res, err := llmcompat.CompleteChat(callCtx, h.httpClient, llmcompat.ChatRequest{
		Protocol:  llmCfg.Protocol,
		BaseURL:   llmCfg.BaseURL,
		APIKey:    apiKey,
		Model:     llmCfg.Model,
		Reasoning: llmCfg.Reasoning,
		MaxTokens: maxTokens,
		Messages: []llmcompat.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("调用总结模型失败：%w", err)
	}
	if res.Status >= 400 || strings.TrimSpace(res.Content) == "" {
		detail := llmcompat.ExtractErrorMessage(res.Raw)
		if detail != "" {
			return nil, fmt.Errorf("总结模型返回错误（HTTP %d）：%s", res.Status, detail)
		}
		return nil, fmt.Errorf("总结模型返回异常（HTTP %d）", res.Status)
	}

	parsed := parseMeetingLLMOutput(res.Content, title, meetingDate, participants, req.PreviousActionItems)
	return &SummarizeResponse{
		OK:              true,
		Mode:            "llm",
		LLMConfigured:   true,
		Model:           res.Model,
		Title:           parsed.Title,
		Summary:         parsed.Summary,
		Topics:          parsed.Topics,
		Decisions:       parsed.Decisions,
		Risks:           parsed.Risks,
		ActionItems:     parsed.ActionItems,
		MinutesMarkdown: parsed.MinutesMarkdown,
		AssembledPrompt: assembled,
	}, nil
}

func parseMeetingLLMOutput(
	content, fallbackTitle, meetingDate string,
	participants []string,
	previousItems []MeetingActionItem,
) SummarizeResponse {
	cleaned := strings.TrimSpace(content)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```JSON")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	prevByText := make(map[string]MeetingActionItem, len(previousItems))
	for _, p := range previousItems {
		k := strings.ToLower(strings.TrimSpace(p.Text))
		if k != "" {
			prevByText[k] = p
		}
	}

	if start := strings.Index(cleaned, "{"); start >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end > start {
			var payload llmMeetingPayload
			if json.Unmarshal([]byte(cleaned[start:end+1]), &payload) == nil {
				title := strings.TrimSpace(payload.Title)
				if title == "" {
					title = fallbackTitle
				}
				now := time.Now().UTC().Format(time.RFC3339)
				items := make([]MeetingActionItem, 0, len(payload.ActionItems))
				seenItem := make(map[string]bool)
				for _, raw := range payload.ActionItems {
					txt := strings.TrimSpace(raw.Text)
					key := strings.ToLower(txt)
					if txt == "" || seenItem[key] {
						continue
					}
					seenItem[key] = true
					existing, hasPrev := prevByText[key]
					id := randomID("act")
					status := "open"
					created := now
					if hasPrev {
						if existing.ID != "" {
							id = existing.ID
						}
						status = normalizeStatus(existing.Status)
						if existing.CreatedAt != "" {
							created = existing.CreatedAt
						}
					}
					items = append(items, MeetingActionItem{
						ID:           id,
						MeetingTitle: title,
						Text:         txt,
						Owner:        strings.TrimSpace(raw.Owner),
						DueDate:      strings.TrimSpace(raw.DueDate),
						Priority:     normalizePriority(raw.Priority),
						Status:       status,
						CreatedAt:    created,
						UpdatedAt:    now,
					})
				}
				topics := cleanTopics(payload.Topics)
				decisions := cleanStringList(payload.Decisions)
				risks := cleanStringList(payload.Risks)
				md := strings.TrimSpace(payload.MinutesMarkdown)
				if md == "" {
					md = renderMinutesMarkdown(title, meetingDate, participants, strings.TrimSpace(payload.Summary), topics, decisions, risks, items, nil)
				}
				return SummarizeResponse{
					Title:           title,
					Summary:         strings.TrimSpace(payload.Summary),
					Topics:          topics,
					Decisions:       decisions,
					Risks:           risks,
					ActionItems:     items,
					MinutesMarkdown: md,
				}
			}
		}
	}

	items := extractHeuristicActionItems(content, fallbackTitle)
	return SummarizeResponse{
		Title:           fallbackTitle,
		Summary:         firstNonEmptySummary(content),
		Topics:          []MeetingTopic{},
		Decisions:       []string{},
		Risks:           []string{},
		ActionItems:     items,
		MinutesMarkdown: strings.TrimSpace(content),
	}
}

var (
	dueDateRe      = regexp.MustCompile(`\b(20\d{2}[-/]\d{1,2}[-/]\d{1,2})\b`)
	ownerPrefixRe  = regexp.MustCompile(`(?:负责人[:：]\s*|@)([^\s,，。;；()（）]{1,16})`)
	checkboxLineRe = regexp.MustCompile(`^\s*[-*]\s*\[[ xX]\]\s*(.+)$`)
	tsPrefixRe     = regexp.MustCompile(`^\[?(\d{1,2}:\d{2})\]?\s*(.+)$`)
)

func buildOfflineMeetingDraft(title, meetingDate string, participants []string, transcript, extra string) SummarizeResponse {
	combined := strings.TrimSpace(transcript)
	if extra != "" {
		if combined != "" {
			combined += "\n" + extra
		} else {
			combined = extra
		}
	}
	lines := strings.Split(strings.ReplaceAll(combined, "\r\n", "\n"), "\n")
	decisions := make([]string, 0)
	risks := make([]string, 0)
	notes := make([]string, 0)
	topics := make([]MeetingTopic, 0)
	actions := make([]MeetingActionItem, 0)
	now := time.Now().UTC().Format(time.RFC3339)

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		ts := ""
		clean := strings.TrimLeft(line, "-*•0123456789.、) ")
		if m := tsPrefixRe.FindStringSubmatch(line); m != nil {
			ts = m[1]
			clean = strings.TrimSpace(m[2])
		}
		if clean == "" {
			continue
		}
		lower := strings.ToLower(clean)
		switch {
		case checkboxLineRe.MatchString(line) ||
			strings.Contains(lower, "todo") ||
			strings.Contains(clean, "待办") ||
			strings.Contains(clean, "行动项") ||
			strings.Contains(clean, "跟进") ||
			strings.Contains(clean, "负责"):
			text := clean
			if m := checkboxLineRe.FindStringSubmatch(line); m != nil {
				text = strings.TrimSpace(m[1])
			}
			owner := ""
			if m := ownerPrefixRe.FindStringSubmatch(text); m != nil {
				owner = strings.TrimSpace(m[1])
			}
			due := ""
			if m := dueDateRe.FindStringSubmatch(text); m != nil {
				due = strings.ReplaceAll(m[1], "/", "-")
			}
			actions = append(actions, MeetingActionItem{
				ID:           randomID("act"),
				MeetingTitle: title,
				Text:         text,
				Owner:        owner,
				DueDate:      due,
				Priority:     "medium",
				Status:       "open",
				CreatedAt:    now,
				UpdatedAt:    now,
			})
		case strings.Contains(clean, "决定") || strings.Contains(clean, "决议") || strings.Contains(clean, "确认") || strings.Contains(clean, "定为"):
			decisions = append(decisions, clean)
		case strings.Contains(clean, "风险") || strings.Contains(clean, "阻塞") || strings.Contains(clean, "延期") || strings.Contains(clean, "问题"):
			risks = append(risks, clean)
		default:
			if len(notes) < 20 {
				notes = append(notes, clean)
			}
			if len(topics) < 4 {
				shortTitle := clean
				if runes := []rune(shortTitle); len(runes) > 16 {
					shortTitle = string(runes[:16]) + "…"
				}
				topics = append(topics, MeetingTopic{
					Timestamp: ts,
					Title:     shortTitle,
					Detail:    clean,
				})
			}
		}
	}

	decisions = cleanStringList(decisions)
	risks = cleanStringList(risks)
	summary := fmt.Sprintf("会议共记录 %d 条讨论要点、%d 条决议、%d 项行动项。", len(notes), len(decisions), len(actions))
	md := renderMinutesMarkdown(title, meetingDate, participants, summary, topics, decisions, risks, actions, notes)
	return SummarizeResponse{
		Title:           title,
		Summary:         summary,
		Topics:          topics,
		Decisions:       decisions,
		Risks:           risks,
		ActionItems:     actions,
		MinutesMarkdown: md,
	}
}

func extractHeuristicActionItems(content, title string) []MeetingActionItem {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]MeetingActionItem, 0)
	for _, line := range lines {
		if m := checkboxLineRe.FindStringSubmatch(line); m != nil {
			txt := strings.TrimSpace(m[1])
			if txt == "" {
				continue
			}
			out = append(out, MeetingActionItem{
				ID:           randomID("act"),
				MeetingTitle: title,
				Text:         txt,
				Priority:     "medium",
				Status:       "open",
				CreatedAt:    now,
				UpdatedAt:    now,
			})
		}
	}
	return out
}

func firstNonEmptySummary(md string) string {
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "|") {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, ">-* ")
		if len([]rune(trimmed)) > 140 {
			return string([]rune(trimmed)[:140]) + "…"
		}
		return trimmed
	}
	return ""
}

func renderMinutesMarkdown(
	title, meetingDate string,
	participants []string,
	summary string,
	topics []MeetingTopic,
	decisions, risks []string,
	actions []MeetingActionItem,
	notes []string,
) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", title))
	b.WriteString(fmt.Sprintf("- **会议日期**：%s\n", meetingDate))
	if len(participants) > 0 {
		b.WriteString(fmt.Sprintf("- **参会人员**：%s\n", strings.Join(participants, "、")))
	}
	b.WriteString("\n")
	if summary != "" {
		b.WriteString("## 核心摘要\n\n" + summary + "\n\n")
	}
	if len(topics) > 0 {
		b.WriteString("## 议题脉络\n\n")
		for _, t := range topics {
			if t.Timestamp != "" {
				b.WriteString(fmt.Sprintf("- **%s**（`%s`）— %s\n", t.Title, t.Timestamp, t.Detail))
			} else {
				b.WriteString(fmt.Sprintf("- **%s** — %s\n", t.Title, t.Detail))
			}
		}
		b.WriteString("\n")
	}
	if len(decisions) > 0 {
		b.WriteString("## 关键决议\n\n")
		for _, d := range decisions {
			b.WriteString("- " + d + "\n")
		}
		b.WriteString("\n")
	}
	if len(actions) > 0 {
		b.WriteString("## 行动项\n\n")
		b.WriteString("| 事项 | 负责人 | 截止日期 | 优先级 |\n")
		b.WriteString("| --- | --- | --- | --- |\n")
		for _, a := range actions {
			owner := a.Owner
			if owner == "" {
				owner = "-"
			}
			due := a.DueDate
			if due == "" {
				due = "-"
			}
			prio := "中"
			if a.Priority == "high" {
				prio = "高"
			} else if a.Priority == "low" {
				prio = "低"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				strings.ReplaceAll(a.Text, "|", "/"), owner, due, prio))
		}
		b.WriteString("\n")
	}
	if len(risks) > 0 {
		b.WriteString("## 风险与待确认\n\n")
		for _, r := range risks {
			b.WriteString("- " + r + "\n")
		}
		b.WriteString("\n")
	}
	if len(notes) > 0 && len(topics) == 0 {
		b.WriteString("## 讨论记录\n\n")
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}
