package sessions

import (
	"context"
	"net/http"
	"strings"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

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
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return nil, "", nil, false
	}
	if h.client == nil || h.client.Runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon client is not configured")
		return nil, "", nil, false
	}
	connID, _ := session["connection_id"].(string)
	ref := buildSessionRef(agentID, sessionID, connID)
	if err := h.ensureNativeBinding(ctx, session); err != nil {
		if r.Method == http.MethodGet {
			if controlRef, handled := h.unboundSettings(ctx, w, r, session, err); handled {
				return controlRef, agentID, session, controlRef != nil
			}
		}
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
	ref, agentID, _, ok := h.settingsRef(ctx, w, r, sessionID)
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
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "supported": false, "detail": "Codex 未提供原生命令列表接口。"})
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
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
	if err := h.ensureNativeBinding(ctx, session); err != nil {
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
	modelMatches := (readModel == payload.Model || strings.HasSuffix(readModel, "/"+payload.Model) || strings.HasSuffix(payload.Model, "/"+readModel))
	providerMatches := (payload.Provider == "" || readProvider == "" || payload.Provider == readProvider)
	if !modelMatches || !providerMatches {
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
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": confirmedProvider,
		"model":    confirmedModel,
	})
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
	writeJSON(w, http.StatusOK, map[string]any{
		"effort": readEffort,
	})
}

func (h *Handler) handleGetSessionApprovalMode(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	ref, _, _, ok := h.settingsRef(ctx, w, r, sessionID)
	if !ok {
		return
	}
	resp, err := h.client.Runtime.ReadApprovalMode(ctx, &sessiondv1.ReadApprovalModeRequest{
		Session: ref,
	})
	if err != nil {
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
	if err := h.ensureNativeBinding(ctx, session); err != nil {
		writeJSONError(w, http.StatusBadGateway, "Native session binding unavailable: "+err.Error())
		return
	}
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
