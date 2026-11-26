package agentapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/connections"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// SetBotCallbacks wires the Specialist Bot service callbacks onto an initialized Handler.
func (h *Handler) SetBotCallbacks(
	listFn func(context.Context) (any, error),
	invokeFn func(context.Context, map[string]any) (any, error),
) {
	if h == nil {
		return
	}
	h.options.BotsList = listFn
	h.options.BotsInvoke = invokeFn
}

func (h *Handler) invokeBotsList(ctx context.Context, _ map[string]any) (any, error) {
	if h.options.BotsList == nil {
		return map[string]any{"items": []any{}}, nil
	}
	return h.options.BotsList(ctx)
}

func (h *Handler) invokeBotsInvoke(ctx context.Context, payload map[string]any) (any, error) {
	if h.options.BotsInvoke == nil {
		return nil, NewAgentApiError("unavailable", "Bots service is not configured")
	}
	if payload == nil {
		return nil, NewAgentApiError("invalid_input", "payload is required")
	}
	enriched := make(map[string]any, len(payload)+4)
	for k, v := range payload {
		enriched[k] = v
	}

	callerAID, _ := payload["caller_agent_id"].(string)
	callerSID, _ := payload["caller_session_id"].(string)
	callerConn, _ := payload["caller_connection_id"].(string)
	if callerAID != "" && callerSID != "" {
		enriched["caller_session_key"] = callerAID + "::" + callerSID
		if h.options.Store != nil {
			if sess, err := h.options.Store.GetSession(ctx, callerAID, callerSID); err == nil && sess != nil {
				if ws, ok := sess["workspace"].(string); ok && strings.TrimSpace(ws) != "" {
					enriched["caller_workspace"] = ws
				}
				if cid, ok := sess["connection_id"].(string); ok && strings.TrimSpace(cid) != "" && callerConn == "" {
					enriched["caller_connection_id"] = cid
				}
			}
		}
	}
	return h.options.BotsInvoke(ctx, enriched)
}

// SpawnForBot creates a clean native session in a Bot's workspace and sends the initial job brief.
// When caller is provided (CLI invocation from another session), it delegates through
// invokeSessionsDelegate for full caller verification, idempotency, and delegation DAG recording.
// When caller is nil (direct browser UI dispatch), it creates the session and submits the prompt directly.
func (h *Handler) SpawnForBot(ctx context.Context, input map[string]any) (map[string]any, error) {
	approvalModeRaw, _ := input["approval_mode"].(string)
	approvalMode, err := normalizeBotApprovalMode(approvalModeRaw)
	if err != nil {
		return nil, err
	}
	if approvalMode == "" {
		approvalMode = "full_access"
	}

	if caller, ok := input["caller"]; ok && caller != nil {
		delegatePayload := map[string]any{
			"operation_id":  input["operation_id"],
			"agent_id":      input["agent_id"],
			"title":         input["title"],
			"workspace":     input["workspace"],
			"prompt":        input["prompt"],
			"caller":        caller,
			"approval_mode": approvalMode,
		}
		for _, opt := range []string{"model", "provider", "effort"} {
			if v, ok := input[opt].(string); ok && strings.TrimSpace(v) != "" {
				delegatePayload[opt] = strings.TrimSpace(v)
			}
		}
		res, err := h.invokeSessionsDelegate(ctx, delegatePayload)
		if err != nil {
			return nil, err
		}
		if resMap, ok := res.(map[string]any); ok {
			return resMap, nil
		}
		return map[string]any{"ok": true, "result": res}, nil
	}

	agentID, _ := input["agent_id"].(string)
	workspace, _ := input["workspace"].(string)
	title, _ := input["title"].(string)
	prompt, _ := input["prompt"].(string)
	model, _ := input["model"].(string)
	provider, _ := input["provider"].(string)
	effort, _ := input["effort"].(string)
	opID, _ := input["operation_id"].(string)
	model = strings.TrimSpace(model)
	provider = strings.TrimSpace(provider)
	effort = strings.TrimSpace(effort)

	if strings.TrimSpace(agentID) == "" {
		return nil, NewAgentApiError("invalid_input", "agent_id is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, NewAgentApiError("invalid_input", "prompt is required")
	}
	if h.options.Store == nil {
		return nil, NewAgentApiError("unavailable", "store is not configured")
	}
	agent, err := h.options.Store.GetAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("load target agent %q: %w", agentID, err)
	}
	if agent == nil {
		return nil, NewAgentApiError("not_found", fmt.Sprintf("未找到执行 Agent %q", agentID))
	}
	if h.options.Client == nil || h.options.Client.Session == nil {
		return nil, NewAgentApiError("unavailable", "daemon client is required")
	}

	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return nil, NewAgentApiError("runtime_error", err.Error())
	}
	if target.Ssh != nil {
		workspace = connections.RemoteWorkspace(workspace, target.Ssh.GetWorkspace())
		if workspace == "" {
			return nil, NewAgentApiError("invalid_input", "an absolute SSH workspace path is required")
		}
	}

	createReq := &sessiondv1.CreateSessionRequest{
		Meta:   newCommandMeta(opID, ""),
		Target: target,
	}
	kind, _ := agent["kind"].(string)
	switch strings.ToLower(kind) {
	case "codex":
		opt := &sessiondv1.CodexCreateOptions{Cwd: workspace}
		if model != "" {
			opt.Model = &model
		}
		if provider != "" {
			opt.Provider = &provider
		}
		if effort != "" {
			opt.Effort = &effort
		}
		createReq.Options = &sessiondv1.CreateSessionRequest_Codex{Codex: opt}
	case "hermes":
		opt := &sessiondv1.HermesCreateOptions{}
		if model != "" {
			opt.Model = &model
		}
		if provider != "" {
			opt.Provider = &provider
		}
		if effort != "" {
			opt.Effort = &effort
		}
		cwdJSON := fmt.Sprintf(`{"workspace":%q,"cwd":%q}`, workspace, workspace)
		opt.NativeOptions = &sessiondv1.NativeJson{
			Schema:   "astrorder.hermes.create.options.v1",
			Utf8Json: []byte(cwdJSON),
		}
		createReq.Options = &sessiondv1.CreateSessionRequest_Hermes{Hermes: opt}
	case "antigravity", "antigravity-ssh":
		opt := &sessiondv1.AntigravityCreateOptions{Cwd: workspace}
		if model != "" {
			opt.Model = &model
		}
		if effort != "" && !modelHasEffortSuffix(model) {
			opt.Effort = &effort
		}
		createReq.Options = &sessiondv1.CreateSessionRequest_Antigravity{Antigravity: opt}
	case "grok":
		opt := &sessiondv1.GrokCreateOptions{Cwd: workspace}
		if model != "" {
			opt.Model = &model
		}
		if effort != "" {
			opt.Effort = &effort
		}
		createReq.Options = &sessiondv1.CreateSessionRequest_Grok{Grok: opt}
	default:
		return nil, NewAgentApiError("invalid_input", fmt.Sprintf("unsupported agent kind %q", kind))
	}

	receipt, err := h.options.Client.Session.CreateSession(ctx, createReq)
	if err != nil {
		return nil, NewAgentApiError("command_rejected", fmt.Sprintf("创建 Bot 会话失败: %v", err))
	}
	if receipt == nil || !receipt.Accepted || receipt.OperationState != sessiondv1.OperationState_OPERATION_STATE_COMPLETED || receipt.Session == nil || receipt.Session.SessionId == "" {
		return nil, NewAgentApiError("command_rejected", "daemon rejected Bot session creation")
	}

	sid := receipt.Session.SessionId
	status := "idle"
	switch receipt.Status {
	case sessiondv1.SessionStatus_SESSION_STATUS_RUNNING:
		status = "running"
	case sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
		status = "waiting_approval"
	case sessiondv1.SessionStatus_SESSION_STATUS_ERROR:
		status = "error"
	}

	sourceID, _ := agent["source_id"].(string)
	if sourceID == "" {
		sourceID = agentID
	}
	connID, _ := agent["connection_id"].(string)

	sessData := map[string]any{
		"id":                     sid,
		"agent_id":               agentID,
		"title":                  title,
		"workspace":              workspace,
		"status":                 status,
		"source_id":              sourceID,
		"connection_id":          connID,
		"native_kind":            kind,
		"source_session_id":      sid,
		"history_state":          "available",
		"control_state":          "owned",
		"ephemeral":              false,
		"selected_approval_mode": approvalMode,
		"updated_at":             time.Now().UTC().Format(time.RFC3339Nano),
	}
	canonical, err := h.options.Store.UpsertSession(ctx, sessData)
	if err != nil {
		return nil, fmt.Errorf("persist Bot session: %w", err)
	}
	if err := h.applySpawnedSessionPolicy(ctx, agent, sid, connID, opID, provider, model, effort, approvalMode); err != nil {
		return nil, err
	}
	if refreshed, rErr := h.options.Store.GetSession(ctx, agentID, sid); rErr == nil && refreshed != nil {
		canonical = refreshed
	}
	if h.options.Publisher != nil {
		_ = h.options.Publisher.Publish(ctx, "session.upsert", canonical)
	}

	var sendReceiptMap map[string]any
	if h.options.CommandSubmit != nil {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		cmdID := "bot-cmd-" + hex.EncodeToString(b)
		cmdRes, sendErr := h.options.CommandSubmit(ctx, map[string]any{
			"id":             cmdID,
			"agent_id":       agentID,
			"session_id":     sid,
			"action":         "send",
			"text":           prompt,
			"attachment_ids": []any{},
			"target_id":      nil,
		})
		if sendErr != nil {
			return nil, fmt.Errorf("派发 Bot 初始任务失败: %w", sendErr)
		}
		sendReceiptMap = cmdRes
	} else {
		sendOp := opID + "-send"
		sendRes, sendErr := h.options.Client.Session.Send(ctx, &sessiondv1.SendRequest{
			Meta:    newCommandMeta(sendOp, sendOp),
			Session: buildSessionRef(agentID, sid, connID),
			Input: []*sessiondv1.InputPart{
				{Part: &sessiondv1.InputPart_Text{Text: &sessiondv1.TextInput{Text: prompt}}},
			},
		})
		if sendErr != nil {
			return nil, fmt.Errorf("派发 Bot 初始任务失败: %w", sendErr)
		}
		if sendRes != nil {
			sendReceiptMap = map[string]any{
				"accepted":        sendRes.Accepted,
				"operation_state": sendRes.OperationState.String(),
				"status":          sendRes.Status.String(),
			}
		}
	}

	return map[string]any{
		"ok":           true,
		"operation_id": opID,
		"stage":        "completed",
		"session": map[string]any{
			"key":           agentID + "::" + sid,
			"id":            sid,
			"agent_id":      agentID,
			"title":         title,
			"workspace":     workspace,
			"connection_id": connID,
			"status":        status,
		},
		"receipt": sendReceiptMap,
	}, nil
}

func modelHasEffortSuffix(model string) bool {
	low := strings.ToLower(strings.TrimSpace(model))
	for _, sfx := range []string{"-low", "-medium", "-high", "-xhigh", "-max"} {
		if strings.HasSuffix(low, sfx) {
			return true
		}
	}
	return false
}

func normalizeBotApprovalMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		return "", nil
	case "full_access", "auto", "manual":
		return strings.ToLower(strings.TrimSpace(mode)), nil
	default:
		return "", NewAgentApiError("invalid_input", fmt.Sprintf("invalid approval_mode %q (must be full_access, auto, or manual)", mode))
	}
}

func spawnedModelMatches(provider, model string, state *sessiondv1.ModelState) bool {
	if state == nil || model == "" || state.GetModel() == "" {
		return false
	}
	readProvider := strings.TrimSpace(state.GetProvider())
	if provider != "" && readProvider != "" && !strings.EqualFold(provider, readProvider) {
		return false
	}
	qualification := provider
	if qualification == "" {
		qualification = readProvider
	}
	wantModel := strings.TrimSpace(model)
	gotModel := strings.TrimSpace(state.GetModel())
	if qualification != "" {
		wantModel = strings.TrimPrefix(wantModel, qualification+"/")
		gotModel = strings.TrimPrefix(gotModel, qualification+"/")
	}
	return wantModel != "" && strings.EqualFold(wantModel, gotModel)
}

func (h *Handler) applySpawnedSessionPolicy(
	ctx context.Context,
	agent map[string]any,
	sessionID string,
	connID string,
	opID string,
	provider string,
	model string,
	effort string,
	approvalMode string,
) error {
	if approvalMode == "" && model == "" && effort == "" {
		return nil
	}
	agentID, _ := agent["id"].(string)
	kind, _ := agent["kind"].(string)
	kindLower := strings.ToLower(strings.TrimSpace(kind))
	isAgy := kindLower == "antigravity" || kindLower == "antigravity-ssh"
	ref := buildSessionRef(agentID, sessionID, connID)

	if h.options.Client != nil && h.options.Client.Runtime != nil {
		if approvalMode != "" {
			receipt, err := h.options.Client.Runtime.SetApprovalMode(ctx, &sessiondv1.SetApprovalModeRequest{
				Meta:    newCommandMeta(opID+"-approval", ""),
				Session: ref,
				Mode:    approvalMode,
			})
			if err != nil {
				return NewAgentApiError("command_rejected", fmt.Sprintf("设置 Bot 会话权限模式(%s)失败: %v", approvalMode, err))
			}
			if receipt == nil || !receipt.Accepted || receipt.OperationState != sessiondv1.OperationState_OPERATION_STATE_COMPLETED {
				return NewAgentApiError("command_rejected", fmt.Sprintf("设置 Bot 会话权限模式(%s)未被小内核确认", approvalMode))
			}
		}

		if model != "" || effort != "" {
			state, readErr := h.options.Client.Runtime.ReadModel(ctx, &sessiondv1.ReadModelRequest{Session: ref})
			if readErr == nil && state != nil {
				if model != "" && !spawnedModelMatches(provider, model, state) {
					mReceipt, mErr := h.options.Client.Runtime.SetModel(ctx, &sessiondv1.SetModelRequest{
						Meta:     newCommandMeta(opID+"-model", ""),
						Session:  ref,
						Provider: provider,
						Model:    model,
					})
					if mErr != nil {
						return NewAgentApiError("command_rejected", fmt.Sprintf("设置 Bot 会话模型(%s)失败: %v", model, mErr))
					}
					if mReceipt == nil || !mReceipt.Accepted || mReceipt.OperationState != sessiondv1.OperationState_OPERATION_STATE_COMPLETED {
						return NewAgentApiError("command_rejected", fmt.Sprintf("设置 Bot 会话模型(%s)未被小内核确认", model))
					}
					if refreshed, rErr := h.options.Client.Runtime.ReadModel(ctx, &sessiondv1.ReadModelRequest{Session: ref}); rErr == nil && refreshed != nil {
						state = refreshed
					}
				}
				shouldSetEffort := effort != "" && (!isAgy || !modelHasEffortSuffix(model))
				if shouldSetEffort && !strings.EqualFold(strings.TrimSpace(state.GetEffort()), effort) {
					eReceipt, eErr := h.options.Client.Runtime.SetReasoning(ctx, &sessiondv1.SetReasoningRequest{
						Meta:    newCommandMeta(opID+"-effort", ""),
						Session: ref,
						Effort:  effort,
					})
					if eErr != nil {
						return NewAgentApiError("command_rejected", fmt.Sprintf("设置 Bot 思考强度(%s)失败: %v", effort, eErr))
					}
					if eReceipt == nil || !eReceipt.Accepted || eReceipt.OperationState != sessiondv1.OperationState_OPERATION_STATE_COMPLETED {
						return NewAgentApiError("command_rejected", fmt.Sprintf("设置 Bot 思考强度(%s)未被小内核确认", effort))
					}
				}
				if provider == "" && strings.TrimSpace(state.GetProvider()) != "" {
					provider = strings.TrimSpace(state.GetProvider())
				}
			}
		}
	}

	if h.options.Store != nil {
		if approvalMode != "" {
			_, _ = h.options.Store.SetSessionApprovalModeBinding(ctx, agentID, sessionID, approvalMode)
		}
		if model != "" {
			bindProvider := provider
			if bindProvider == "" && isAgy {
				bindProvider = "antigravity"
			}
			if bindProvider != "" {
				_, _ = h.options.Store.SetSessionModelBinding(ctx, agentID, sessionID, bindProvider, model)
			}
		}
		if effort != "" && (!isAgy || !modelHasEffortSuffix(model)) {
			_, _ = h.options.Store.SetSessionReasoningBinding(ctx, agentID, sessionID, effort)
		}
	}
	return nil
}
