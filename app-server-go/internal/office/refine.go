package office

import (
	"context"
	"fmt"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/officepersona"
	"astrorder.dev/app-server/internal/store"
)

// RefineRequest is accepted by POST /api/v1/office/refine for section-level
// document co-editing with local diff confirmation.
type RefineRequest struct {
	DocumentTitle string `json:"document_title,omitempty"`
	SelectedText  string `json:"selected_text"`
	ContextBefore string `json:"context_before,omitempty"`
	ContextAfter  string `json:"context_after,omitempty"`
	Instruction   string `json:"instruction"`
	Mode          string `json:"mode,omitempty"` // "llm" (default) or "prompt_only"
	ProviderID    string `json:"provider_id,omitempty"`
	Model         string `json:"model,omitempty"`
}

// RefineResponse returns the rewritten passage or an assembled prompt when LLM
// is not configured.
type RefineResponse struct {
	OK              bool   `json:"ok"`
	Mode            string `json:"mode"` // "llm" or "prompt_only"
	LLMConfigured   bool   `json:"llm_configured"`
	Model           string `json:"model,omitempty"`
	RewrittenText   string `json:"rewritten_text"`
	AssembledPrompt string `json:"assembled_prompt"`
	Message         string `json:"message,omitempty"`
}

func loadHelperLLMConfig(ctx context.Context, st *store.Store) aiprovider.ResolvedModel {
	return aiprovider.ResolveDefault(ctx, st)
}

func (h *Handler) refineSelection(ctx context.Context, req RefineRequest) (*RefineResponse, error) {
	selected := strings.TrimSpace(req.SelectedText)
	instruction := strings.TrimSpace(req.Instruction)
	if selected == "" {
		return nil, fmt.Errorf("请先选择需要精修的段落")
	}
	if instruction == "" {
		return nil, fmt.Errorf("请填写改写要求")
	}

	systemPrompt := strings.TrimSpace(`你是一位专业中文文档编辑。请严格只改写用户提供的「目标段落」，直接输出替换后的完整 Markdown 段落内容。
要求：
1. 保持原段落的 Markdown 层级结构（如原本是标题则保留同级标题，原本是列表则保留列表结构，除非用户要求转换格式）。
2. 严禁编造事实或数据，严禁输出多余解释、寒暄或外层 ` + "```markdown" + ` 代码围栏。`)
	if persona := officepersona.PromptSection(officepersona.Load(ctx, h.options.Store)); persona != "" {
		systemPrompt += "\n\n" + persona
	}

	var userBuf strings.Builder
	if strings.TrimSpace(req.DocumentTitle) != "" {
		userBuf.WriteString(fmt.Sprintf("文档标题：%s\n", strings.TrimSpace(req.DocumentTitle)))
	}
	userBuf.WriteString(fmt.Sprintf("改写要求：%s\n", instruction))
	if before := strings.TrimSpace(req.ContextBefore); before != "" {
		userBuf.WriteString("\n【上文参考（不要输出）】\n" + truncateRunes(before, 300) + "\n")
	}
	userBuf.WriteString("\n【目标段落（仅改写此段）】\n" + selected + "\n")
	if after := strings.TrimSpace(req.ContextAfter); after != "" {
		userBuf.WriteString("\n【下文参考（不要输出）】\n" + truncateRunes(after, 300) + "\n")
	}
	userPrompt := userBuf.String()
	assembled := fmt.Sprintf("请根据以下要求仅改写指定段落，不要改动文档其他部分：\n\n%s", userPrompt)

	llmCfg := loadHelperLLMConfig(ctx, h.options.Store)
	if strings.TrimSpace(req.ProviderID) != "" {
		if resolved, ok := aiprovider.Resolve(ctx, h.options.Store, req.ProviderID, req.Model); ok {
			llmCfg = resolved
		}
	} else if strings.TrimSpace(req.Model) != "" {
		llmCfg.Model = strings.TrimSpace(req.Model)
	}
	llmConfigured := llmCfg.IsConfigured()
	if strings.EqualFold(strings.TrimSpace(req.Mode), "prompt_only") || !llmConfigured {
		return &RefineResponse{
			OK:              true,
			Mode:            "prompt_only",
			LLMConfigured:   llmConfigured,
			Model:           llmCfg.Model,
			RewrittenText:   selected,
			AssembledPrompt: assembled,
			Message:         "未配置通用 AI 模型（可在设置中选择已接入 Agent 或填写 API Key），可将改写指令直接发给当前会话助手执行。",
		}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	res, err := llmcompat.CompleteChat(callCtx, h.httpClient, llmCfg.ToChatRequest(2048, []llmcompat.ChatMessage{
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

	cleaned := strings.TrimSpace(res.Content)
	cleaned = strings.TrimPrefix(cleaned, "```markdown\n")
	cleaned = strings.TrimPrefix(cleaned, "```md\n")
	cleaned = strings.TrimSuffix(cleaned, "\n```")

	return &RefineResponse{
		OK:              true,
		Mode:            "llm",
		LLMConfigured:   true,
		Model:           res.Model,
		RewrittenText:   strings.TrimSpace(cleaned),
		AssembledPrompt: assembled,
	}, nil
}
