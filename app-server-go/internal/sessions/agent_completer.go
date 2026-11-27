package sessions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/llmcompat"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func canonicalAgentKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "antigravity-ssh", "agy":
		return "antigravity"
	case "codex-ssh":
		return "codex"
	case "grok-ssh":
		return "grok"
	case "ssh-hermes", "hermes-ssh", "ssh":
		return "hermes"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func randomCompletionOpID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "agentllm-" + hex.EncodeToString(b[:])
}

// ListAgentModels queries available native models for a connected Agent without
// requiring the caller to already own an interactive session.
func (h *Handler) ListAgentModels(ctx context.Context, agentID string) ([]string, error) {
	aid := strings.TrimSpace(agentID)
	if aid == "" {
		return nil, errors.New("agent_id is required")
	}
	if h.store == nil || h.client == nil || h.client.Runtime == nil || h.client.Session == nil {
		return nil, errors.New("会话后台服务暂不可用")
	}
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, fmt.Errorf("未找到 Agent：%s", aid)
	}
	kind := canonicalAgentKind(stringValue(agent, "kind"))
	conn := stringValue(agent, "connection_id")
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return nil, err
	}

	var ref *pb.SessionRef
	// Prefer an existing session for runtimes like hermes/grok that read models from a live session,
	// or attach a read-only runtime probe for codex/antigravity.
	if kind == "codex" || kind == "antigravity" {
		label := conn
		if label == "" {
			label = "local"
		}
		probeRef := buildSessionRef(aid, fmt.Sprintf("probe-%s-%s", kind, label), conn)
		receipt, aErr := h.client.Session.AttachSession(ctx, &pb.AttachSessionRequest{
			Session:        probeRef,
			Target:         target,
			RuntimeControl: true,
		})
		if aErr == nil && receipt != nil && receipt.Attached && receipt.Session != nil {
			ref = receipt.Session
		}
	}
	if ref == nil {
		if rows, lErr := h.store.ListSessions(ctx, aid); lErr == nil {
			for i, row := range rows {
				if i >= 20 {
					break
				}
				sid := stringValue(row, "id")
				if sid == "" {
					continue
				}
				candidate := buildSessionRef(aid, sid, stringValue(row, "connection_id"))
				if nativeID := stringValue(row, "source_session_id"); nativeID != "" {
					candidate.SessionId = nativeID
				}
				if resp, mErr := h.client.Runtime.ListModels(ctx, &pb.ListModelsRequest{Session: candidate}); mErr == nil && resp != nil {
					return extractUniqueModelIDs(resp.Items), nil
				}
			}
		}
		// Final fallback: try attaching a generic control probe
		label := conn
		if label == "" {
			label = "local"
		}
		probeRef := buildSessionRef(aid, fmt.Sprintf("probe-%s-%s", kind, label), conn)
		if receipt, aErr := h.client.Session.AttachSession(ctx, &pb.AttachSessionRequest{
			Session:        probeRef,
			Target:         target,
			RuntimeControl: true,
		}); aErr == nil && receipt != nil && receipt.Attached && receipt.Session != nil {
			ref = receipt.Session
		}
	}
	if ref == nil {
		return nil, errors.New("当前 Agent 尚未就绪，暂无法读取模型列表")
	}
	resp, err := h.client.Runtime.ListModels(ctx, &pb.ListModelsRequest{Session: ref})
	if err != nil {
		return nil, err
	}
	return extractUniqueModelIDs(resp.Items), nil
}

func extractUniqueModelIDs(items []*pb.ModelOption) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, opt := range items {
		if opt == nil {
			continue
		}
		m := strings.TrimSpace(opt.Model)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

func formatAgentCompletionPrompt(messages []llmcompat.ChatMessage) string {
	var b strings.Builder
	b.WriteString("你正在作为星序通用文本模型执行一次单轮文本任务。请直接输出最终结果文本，不要修改任何本地文件，不要执行命令行工具，不要输出多余寒暄或过程说明。\n")
	for _, m := range messages {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(m.Role)) {
		case "system":
			b.WriteString("\n【系统指令】\n")
			b.WriteString(content)
			b.WriteString("\n")
		case "assistant":
			b.WriteString("\n【此前回复】\n")
			b.WriteString(content)
			b.WriteString("\n")
		default:
			b.WriteString("\n【输入内容】\n")
			b.WriteString(content)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// CompleteViaAgent executes a single-turn universal LLM completion through a
// connected Astrorder Agent runtime using an isolated ephemeral session that is
// automatically deleted after completion.
func (h *Handler) CompleteViaAgent(ctx context.Context, req llmcompat.ChatRequest) (llmcompat.ChatResult, error) {
	aid := strings.TrimSpace(req.AgentID)
	if aid == "" {
		return llmcompat.ChatResult{}, errors.New("未指定用于通用补全的 Agent")
	}
	if h.store == nil || h.client == nil || h.client.Session == nil || h.client.Runtime == nil || h.client.Events == nil {
		return llmcompat.ChatResult{}, errors.New("会话后台服务暂不可用")
	}
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		return llmcompat.ChatResult{}, err
	}
	if agent == nil {
		return llmcompat.ChatResult{}, fmt.Errorf("未找到已接入的 Agent：%s", aid)
	}
	kind := canonicalAgentKind(stringValue(agent, "kind"))
	conn := stringValue(agent, "connection_id")
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return llmcompat.ChatResult{}, err
	}

	workspace := strings.TrimSpace(h.defaultWorkspace)
	var tempDir string
	if conn == "" || conn == "local" {
		if dir, mkErr := os.MkdirTemp("", "astrorder-agent-llm-*"); mkErr == nil {
			tempDir = dir
			workspace = dir
		}
	}
	if workspace == "" {
		workspace = os.TempDir()
	}
	if tempDir != "" {
		defer os.RemoveAll(tempDir)
	}

	providerHint := ""
	if kind == "antigravity" {
		providerHint = "antigravity"
	}
	model := strings.TrimSpace(req.Model)
	effort := strings.TrimSpace(req.Reasoning)
	if kind == "antigravity" {
		effort = ""
	}

	created, err := h.createHandoffNative(ctx, target, kind, workspace, "通用模型调用", "", providerHint, model, effort, true)
	if err != nil {
		return llmcompat.ChatResult{}, fmt.Errorf("创建 Agent 临时会话失败：%w", err)
	}
	ref := created.Session
	sid := ref.GetSessionId()
	completed := false
	var turnID string
	defer func() {
		if completed {
			h.cleanupCompletedHandoffFork(ref)
			return
		}
		intCtx, stopInt := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = h.client.Session.Interrupt(intCtx, &pb.InterruptRequest{Meta: newCommandMeta("", ""), Session: ref, TurnId: optString(turnID)})
		stopInt()
		h.cleanupCompletedHandoffFork(ref)
	}()

	_, err = h.store.UpsertSession(ctx, map[string]any{
		"id":                        sid,
		"source_session_id":         sid,
		"agent_id":                  aid,
		"title":                     "通用模型调用",
		"workspace":                 workspace,
		"connection_id":             agent["connection_id"],
		"status":                    "idle",
		"ephemeral":                 true,
		"history_state":             "available",
		"control_state":             "owned",
		"selected_model_provider":   providerHint,
		"selected_model":            model,
		"selected_reasoning_effort": effort,
		"updated_at":                time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return llmcompat.ChatResult{}, err
	}
	if model != "" {
		_ = h.restoreModel(ctx, ref)
	}

	prompt := formatAgentCompletionPrompt(req.Messages)
	sendID := randomCompletionOpID()
	send, err := h.client.Session.Send(ctx, &pb.SendRequest{
		Meta:    newCommandMeta(sendID, sendID),
		Session: ref,
		Input:   []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: prompt}}}},
		Policy:  &pb.SendPolicy{NativeOptions: &pb.NativeJson{Schema: "astrorder." + kind + ".send.options.v1", Utf8Json: []byte(`{"idle_only":true}`)}},
	})
	if err != nil {
		return llmcompat.ChatResult{}, fmt.Errorf("发送至 Agent 失败：%w", err)
	}
	turnID = send.GetTurnId()
	if err := confirmedReceipt(send); err != nil || !send.GetAccepted() {
		return llmcompat.ChatResult{}, fmt.Errorf("Agent 未确认执行请求：%v", err)
	}

	_, replyText, err := h.waitHandoffSummary(ctx, agent, ref, map[string]bool{}, send, sendID)
	if err != nil {
		return llmcompat.ChatResult{}, fmt.Errorf("等待 Agent 生成结果失败：%w", err)
	}
	completed = true
	outModel := model
	if outModel == "" {
		outModel = kind
	}
	return llmcompat.ChatResult{
		Status:  200,
		Model:   outModel,
		Content: strings.TrimSpace(replyText),
	}, nil
}
