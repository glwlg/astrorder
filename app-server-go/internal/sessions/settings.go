package sessions

import (
	"context"
	"net/http"
	"strings"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func antigravityEffortFromModel(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	for _, suffix := range []string{"-xhigh", "-low", "-medium", "-high", "-max"} {
		if strings.HasSuffix(lower, suffix) {
			return strings.TrimPrefix(suffix, "-")
		}
	}
	return ""
}

func (h *Handler) settingsRef(ctx context.Context, w http.ResponseWriter, r *http.Request, sessionID string) (*sessiondv1.SessionRef, string, map[string]any, bool) {
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id is required")
		return nil, "", nil, false
	}
	session, err := h.store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to get session: "+err.Error())
		return nil, "", nil, false
	}
	if session == nil {
		if r.Method == http.MethodGet && sessionID == "__probe__" && h.client != nil && h.client.Runtime != nil && h.client.Session != nil {
			agent, aErr := h.store.GetAgent(ctx, agentID)
			if aErr == nil && agent != nil {
				probeRow := map[string]any{"id": "__probe__", "agent_id": agentID, "connection_id": stringValue(agent, "connection_id")}
				if controlRef, handled := h.unboundSettings(ctx, w, r, probeRow, status.Error(codes.NotFound, "probe")); handled {
					return controlRef, agentID, probeRow, controlRef != nil
				}
			}
		}
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return nil, "", nil, false
	}
	if h.client == nil || h.client.Runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon client is not configured")
		return nil, "", nil, false
	}
	connID, _ := session["connection_id"].(string)
	ref := buildSessionRef(agentID, sessionID, connID)
	if nativeID := stringValue(session, "source_session_id"); nativeID != "" {
		ref.SessionId = nativeID
	}
	if r.Method == http.MethodGet {
		// Reading controls must not attach, restore policy, or clear sleep state.
		if _, err := h.client.Runtime.ReadModel(ctx, &sessiondv1.ReadModelRequest{Session: ref}); err != nil {
			if controlRef, handled := h.unboundSettings(ctx, w, r, session, err); handled {
				return controlRef, agentID, session, controlRef != nil
			}
			writeJSONError(w, http.StatusBadGateway, "Native settings unavailable: "+err.Error())
			return nil, "", nil, false
		}
		return ref, agentID, session, true
	}
	if err := h.ensureNativeBinding(ctx, session); err != nil {
		writeJSONError(w, http.StatusBadGateway, "Native session binding unavailable: "+err.Error())
		return nil, "", nil, false
	}
	return ref, agentID, session, true
}

func (h *Handler) handleSessionModels(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	ref, _, _, ok := h.settingsRef(ctx, w, r, sessionID)
	if !ok {
		return
	}
	resp, err := h.client.Runtime.ListModels(ctx, &sessiondv1.ListModelsRequest{
		Session: ref,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "Failed to list models: "+err.Error())
		return
	}
	items := make([]map[string]any, 0, len(resp.Items))
	for _, opt := range resp.Items {
		item := map[string]any{
			"provider":          opt.Provider,
			"model":             opt.Model,
			"supported_efforts": opt.SupportedEfforts,
		}
		if opt.Label != nil {
			item["label"] = *opt.Label
		}
		if opt.Native != nil {
			var extra map[string]any
			if parseErr := parseNativeJSON(opt.Native, &extra); parseErr != nil {
				writeJSONError(w, http.StatusBadGateway, "Failed to parse native model extension: "+parseErr.Error())
				return
			}
			if extra != nil {
				for k, v := range extra {
					if _, exists := item[k]; !exists {
						item[k] = v
					}
				}
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetSessionModel(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	ref, agentID, sessRow, ok := h.settingsRef(ctx, w, r, sessionID)
	if !ok {
		return
	}
	saved, _ := h.store.GetSessionModelBinding(ctx, agentID, sessionID)
	resp, err := h.client.Runtime.ReadModel(ctx, &sessiondv1.ReadModelRequest{
		Session: ref,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "Failed to read model state: "+err.Error())
		return
	}
	result := map[string]any{}
	if resp.Provider != nil {
		result["provider"] = *resp.Provider
	}
	if resp.Model != nil {
		result["model"] = *resp.Model
	}
	if resp.Branch != nil {
		result["branch"] = *resp.Branch
	}
	if resp.Effort != nil {
		result["effort"] = *resp.Effort
	}
	agent, agentErr := h.store.GetAgent(ctx, agentID)
	if agentErr != nil {
		writeJSONError(w, 500, "Agent storage unavailable")
		return
	}
	if agentKind := stringValue(agent, "kind"); agentKind == "antigravity" || agentKind == "antigravity-ssh" {
		var native map[string]any
		if resp.Native != nil {
			_ = parseNativeJSON(resp.Native, &native)
		}
		result["model_read_only"] = native["model_switch_supported"] != true
		result["reasoning_read_only"] = native["reasoning_switch_supported"] != true
		if se, ok := native["supported_efforts"]; ok {
			result["supported_efforts"] = se
		}
		if native["read_only"] == true {
			result["read_only"] = true
			result["detail"] = native["detail"]
		}
		if result["model_read_only"] == true && result["read_only"] != true {
			result["detail"] = "当前小内核尚不支持 Antigravity 模型切换，请更新小内核。"
		}
		if stringValue(result, "provider") == "" {
			result["provider"] = "antigravity"
		}
		if curModel := stringValue(result, "model"); isAntigravityInternalWorkerModel(curModel) {
			if saved != nil && stringValue(saved, "model") != "" && !isAntigravityInternalWorkerModel(stringValue(saved, "model")) {
				result["model"] = stringValue(saved, "model")
			} else {
				result["model"] = "gemini-pro-orchestrator"
			}
		}
		if stringValue(result, "model") == "" {
			if saved != nil {
				if m := stringValue(saved, "model"); m != "" {
					result["model"] = m
				}
			}
			if stringValue(result, "model") == "" {
				target, _ := h.resolveRuntimeTarget(ctx, agent)
				h.backfillAntigravityModel(ctx, target, agentID, sessRow, result["read_only"] == true, result)
			}
		}
		if inferred := antigravityEffortFromModel(stringValue(result, "model")); inferred != "" && stringValue(result, "effort") == "" {
			result["effort"] = inferred
		}
	}
	if saved != nil {
		if _, ok := saved["effort"]; ok && result["effort"] == nil {
			result["effort"] = saved["effort"]
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleSessionAgentCommands(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	aid := r.URL.Query().Get("agent_id")
	if aid != "" {
		row, rowErr := h.store.GetSession(ctx, aid, sessionID)
		agent, agentErr := h.store.GetAgent(ctx, aid)
		if rowErr == nil && agentErr == nil && row != nil && stringValue(agent, "kind") == "codex" {
			writeJSON(w, http.StatusOK, map[string]any{"items": []map[string]any{
				{"name": "compact", "description": "压缩当前会话上下文", "input_hint": "可选：压缩说明"},
				{"name": "review", "description": "代码审查 / 审查工作区变更", "input_hint": "可选：审查说明"},
			}, "supported": true, "detail": "Codex app-server 原生操作；不包含仅在 CLI 界面执行的命令。"})
			return
		}
	}
	ref, _, _, ok := h.settingsRef(ctx, w, r, sessionID)
	if !ok {
		return
	}
	resp, err := h.client.Runtime.ListCommands(ctx, &sessiondv1.ListCommandsRequest{
		Session: ref,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "Failed to list commands: "+err.Error())
		return
	}
	items := make([]map[string]any, 0, len(resp.Items))
	for _, opt := range resp.Items {
		item := map[string]any{
			"name": opt.Name,
		}
		if opt.Description != nil {
			item["description"] = *opt.Description
		}
		if opt.InputHint != nil {
			item["input_hint"] = *opt.InputHint
		}
		if opt.Native != nil {
			var extra map[string]any
			if parseErr := parseNativeJSON(opt.Native, &extra); parseErr != nil {
				writeJSONError(w, http.StatusBadGateway, "Failed to parse native command extension: "+parseErr.Error())
				return
			}
			if extra != nil {
				for k, v := range extra {
					if _, exists := item[k]; !exists {
						item[k] = v
					}
				}
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "supported": true})
}

func (h *Handler) handleSessionAgentMentions(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	ref, agentID, _, ok := h.settingsRef(ctx, w, r, sessionID)
	if !ok {
		return
	}
	agent, err := h.store.GetAgent(ctx, agentID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to get agent: "+err.Error())
		return
	}
	if agent == nil {
		writeJSONError(w, http.StatusNotFound, "Agent was not found")
		return
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind, _ := agent["kind"].(string)
	if kind == "codex" {
		queryReq := &sessiondv1.NativeQueryRequest{
			Target: target,
			Query: &sessiondv1.NativeQueryRequest_CodexSkillsList{
				CodexSkillsList: &sessiondv1.CodexSkillsListQuery{
					Cwds: []string{},
				},
			},
		}
		resp, err := h.client.Runtime.QueryNative(ctx, queryReq)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "Failed to query native skills: "+err.Error())
			return
		}
		var parsed struct {
			Data []struct {
				Skills []map[string]any `json:"skills"`
			} `json:"data"`
		}
		if err := parseNativeJSON(resp.Result, &parsed); err != nil {
			writeJSONError(w, http.StatusBadGateway, "Failed to parse native mentions result: "+err.Error())
			return
		}
		items := make([]map[string]any, 0)
		for _, grp := range parsed.Data {
			for _, sk := range grp.Skills {
				if sk["enabled"] != false {
					name, _ := sk["name"].(string)
					pathStr, _ := sk["path"].(string)
					if name != "" && pathStr != "" {
						desc := ""
						if d, ok := sk["shortDescription"].(string); ok && d != "" {
							desc = d
						} else if d, ok := sk["description"].(string); ok && d != "" {
							desc = d
						}
						items = append(items, map[string]any{
							"name":        name,
							"description": desc,
							"path":        pathStr,
							"kind":        "skill",
						})
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	_ = ref
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
}

type setSessionModelPayload struct {
	AgentID  string `json:"agent_id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (h *Handler) handleSetSessionModel(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	var payload setSessionModelPayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid request payload")
		return
	}
	if payload.AgentID == "" || payload.Model == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id and model are required")
		return
	}
	session, err := h.store.GetSession(ctx, payload.AgentID, sessionID)
	if err != nil || session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	if h.client == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon client is not configured")
		return
	}
	connID, _ := session["connection_id"].(string)
	ref := buildSessionRef(payload.AgentID, sessionID, connID)
	meta := newCommandMeta("", "")
	if err := h.ensureNativeBindingLazy(ctx, session, map[string]any{"model": payload.Model}); err != nil {
		writeJSONError(w, http.StatusBadGateway, "Native session binding unavailable: "+err.Error())
		return
	}
	receipt, err := h.client.Runtime.SetModel(ctx, &sessiondv1.SetModelRequest{
		Meta:     meta,
		Session:  ref,
		Provider: payload.Provider,
		Model:    payload.Model,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "SetModel failed: "+err.Error())
		return
	}
	if err := confirmedReceipt(receipt); err != nil {
		writeJSONError(w, http.StatusBadGateway, "SetModel outcome unconfirmed: "+err.Error())
		return
	}
	// Native readback confirmation
	readback, err := h.client.Runtime.ReadModel(ctx, &sessiondv1.ReadModelRequest{
		Session: ref,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "ReadModel verification failed: "+err.Error())
		return
	}
	readProvider := ""
	if readback.Provider != nil {
		readProvider = *readback.Provider
	}
	readModel := ""
	if readback.Model != nil {
		readModel = *readback.Model
	}
	if !modelBindingMatches(payload.Provider, payload.Model, readProvider, readModel) {
		writeJSONError(w, http.StatusBadGateway, "Model switch was not confirmed by native readback")
		return
	}
	confirmedProvider := payload.Provider
	if confirmedProvider == "" {
		confirmedProvider = readProvider
	}
	confirmedModel := payload.Model
	if confirmedModel == "" {
		confirmedModel = readModel
	}
	if _, err := h.store.SetSessionModelBinding(ctx, payload.AgentID, sessionID, confirmedProvider, confirmedModel); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to persist model binding: "+err.Error())
		return
	}
	respBody := map[string]any{
		"provider": confirmedProvider,
		"model":    confirmedModel,
	}
	isAntigravity := confirmedProvider == "antigravity"
	if !isAntigravity {
		if agent, aErr := h.store.GetAgent(ctx, payload.AgentID); aErr == nil && agent != nil {
			kind := stringValue(agent, "kind")
			isAntigravity = kind == "antigravity" || kind == "antigravity-ssh"
		}
	}
	if isAntigravity {
		confirmedEffort := antigravityEffortFromModel(confirmedModel)
		if confirmedEffort == "" && readback.Effort != nil {
			confirmedEffort = strings.TrimSpace(*readback.Effort)
		}
		_, _ = h.store.SetSessionReasoningBinding(ctx, payload.AgentID, sessionID, confirmedEffort)
		if confirmedEffort != "" {
			respBody["effort"] = confirmedEffort
		} else {
			respBody["effort"] = nil
		}
		var native map[string]any
		if readback.Native != nil {
			_ = parseNativeJSON(readback.Native, &native)
		}
		if native != nil {
			respBody["reasoning_read_only"] = native["reasoning_switch_supported"] != true
			if se, ok := native["supported_efforts"]; ok {
				respBody["supported_efforts"] = se
			}
		}
	}
	writeJSON(w, http.StatusOK, respBody)
}

type setSessionReasoningPayload struct {
	AgentID string `json:"agent_id"`
	Effort  string `json:"effort"`
}

func (h *Handler) handleSetSessionReasoning(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	var payload setSessionReasoningPayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid request payload")
		return
	}
	if payload.AgentID == "" || payload.Effort == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id and effort are required")
		return
	}
	session, err := h.store.GetSession(ctx, payload.AgentID, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to get session: "+err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	if h.client == nil || h.client.Runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon client is not configured")
		return
	}
	connID, _ := session["connection_id"].(string)
	ref := buildSessionRef(payload.AgentID, sessionID, connID)
	meta := newCommandMeta("", "")
	if err := h.ensureNativeBinding(ctx, session); err != nil {
		writeJSONError(w, http.StatusBadGateway, "Native session binding unavailable: "+err.Error())
		return
	}
	receipt, err := h.client.Runtime.SetReasoning(ctx, &sessiondv1.SetReasoningRequest{
		Meta:    meta,
		Session: ref,
		Effort:  payload.Effort,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "SetReasoning failed: "+err.Error())
		return
	}
	if err := confirmedReceipt(receipt); err != nil {
		writeJSONError(w, http.StatusBadGateway, "SetReasoning outcome unconfirmed: "+err.Error())
		return
	}
	readback, err := h.client.Runtime.ReadModel(ctx, &sessiondv1.ReadModelRequest{
		Session: ref,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "ReadModel verification failed: "+err.Error())
		return
	}
	readEffort := ""
	if readback.Effort != nil {
		readEffort = *readback.Effort
	}
	if readEffort != payload.Effort {
		writeJSONError(w, http.StatusBadGateway, "Reasoning switch was not confirmed by native readback")
		return
	}
	if _, err := h.store.SetSessionReasoningBinding(ctx, payload.AgentID, sessionID, payload.Effort); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to persist reasoning binding: "+err.Error())
		return
	}
	respBody := map[string]any{
		"effort": readEffort,
	}
	if readback.Model != nil && strings.TrimSpace(*readback.Model) != "" {
		readProvider := ""
		if readback.Provider != nil {
			readProvider = strings.TrimSpace(*readback.Provider)
		}
		isAntigravity := readProvider == "antigravity"
		if !isAntigravity {
			if agent, aErr := h.store.GetAgent(ctx, payload.AgentID); aErr == nil && agent != nil {
				kind := stringValue(agent, "kind")
				isAntigravity = kind == "antigravity" || kind == "antigravity-ssh"
			}
		}
		if isAntigravity {
			if readProvider == "" {
				readProvider = "antigravity"
			}
			_, _ = h.store.SetSessionModelBinding(ctx, payload.AgentID, sessionID, readProvider, strings.TrimSpace(*readback.Model))
			respBody["provider"] = readProvider
			respBody["model"] = strings.TrimSpace(*readback.Model)
		}
	}
	writeJSON(w, http.StatusOK, respBody)
}

func (h *Handler) handleGetSessionApprovalMode(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	ref, _, session, ok := h.settingsRef(ctx, w, r, sessionID)
	if !ok {
		return
	}
	resp, err := h.client.Runtime.ReadApprovalMode(ctx, &sessiondv1.ReadApprovalModeRequest{
		Session: ref,
	})
	if err != nil {
		if _, handled := h.unboundSettings(ctx, w, r, session, err); handled {
			return
		}
		writeJSONError(w, http.StatusBadGateway, "Failed to read approval mode: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": resp.Mode})
}

type setSessionApprovalModePayload struct {
	AgentID string `json:"agent_id"`
	Mode    string `json:"mode"`
}

func (h *Handler) handleSetSessionApprovalMode(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	var payload setSessionApprovalModePayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid request payload")
		return
	}
	if payload.AgentID == "" || payload.Mode == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id and mode are required")
		return
	}
	session, err := h.store.GetSession(ctx, payload.AgentID, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to get session: "+err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	if h.client == nil || h.client.Runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon client is not configured")
		return
	}
	connID, _ := session["connection_id"].(string)
	ref := buildSessionRef(payload.AgentID, sessionID, connID)
	meta := newCommandMeta("", "")
	// An explicit new choice supersedes the saved choice; do not briefly
	// restore the previous access mode before applying it.
	attachPerm := map[string]any{"lazy_open": true}
	switch payload.Mode {
	case "full_access":
		attachPerm["permission_mode"] = "always-proceed"
	case "auto":
		attachPerm["permission_mode"] = "auto"
	case "manual":
		attachPerm["permission_mode"] = "request-review"
	}
	if err := h.bindNativeSessionWithOptions(ctx, session, attachPerm); err != nil {
		writeJSONError(w, http.StatusBadGateway, "Native session binding unavailable: "+err.Error())
		return
	}
	lock := h.approvalSelectionLock(payload.AgentID, sessionID)
	lock.Lock()
	defer lock.Unlock()
	receipt, err := h.client.Runtime.SetApprovalMode(ctx, &sessiondv1.SetApprovalModeRequest{
		Meta:    meta,
		Session: ref,
		Mode:    payload.Mode,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "SetApprovalMode failed: "+err.Error())
		return
	}
	if err := confirmedReceipt(receipt); err != nil {
		writeJSONError(w, http.StatusBadGateway, "SetApprovalMode outcome unconfirmed: "+err.Error())
		return
	}
	readback, err := h.client.Runtime.ReadApprovalMode(ctx, &sessiondv1.ReadApprovalModeRequest{
		Session: ref,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "ReadApprovalMode verification failed: "+err.Error())
		return
	}
	if readback.Mode != payload.Mode {
		writeJSONError(w, http.StatusBadGateway, "Approval mode switch was not confirmed by native readback")
		return
	}
	if _, err := h.store.SetSessionApprovalModeBinding(ctx, payload.AgentID, sessionID, payload.Mode); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to persist approval mode binding: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode": readback.Mode,
	})
}
