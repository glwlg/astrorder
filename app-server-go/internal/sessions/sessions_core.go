package sessions

import (
	"net/http"
	"strings"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *Handler) handleListSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agentID := r.URL.Query().Get("agent_id")
	items, err := h.store.ListSessions(ctx, agentID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to list sessions: "+err.Error())
		return
	}
	h.repairSessionTitles(ctx, items)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type createSessionPayload struct {
	AgentID         string `json:"agent_id"`
	Workspace       string `json:"workspace"`
	Title           string `json:"title"`
	ProjectID       string `json:"project_id"`
	ProjectName     string `json:"project_name"`
	ParentSessionID string `json:"parent_session_id"`
	Ephemeral       bool   `json:"ephemeral"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	Effort          string `json:"effort"`
}

func (h *Handler) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var payload createSessionPayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid request payload")
		return
	}
	if payload.AgentID == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id is required")
		return
	}
	agent, err := h.store.GetAgent(ctx, payload.AgentID)
	if err != nil {
		writeJSONError(w, 500, "Agent storage is unavailable")
		return
	}
	if agent == nil {
		writeJSONError(w, http.StatusNotFound, "Agent was not found")
		return
	}
	if h.client == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon client is not configured")
		return
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	workspace := payload.Workspace
	if target.Ssh != nil {
		if !strings.HasPrefix(workspace, "/") {
			if target.Ssh.Workspace != nil && strings.HasPrefix(*target.Ssh.Workspace, "/") {
				workspace = *target.Ssh.Workspace
			} else {
				workspace = "/home/luwei"
			}
		}
	} else {
		if workspace == "" {
			workspace = h.defaultWorkspace
		}
		if workspace == "" {
			writeJSONError(w, 422, "workspace is required")
			return
		}
	}
	req := &sessiondv1.CreateSessionRequest{
		Meta:   newCommandMeta("", ""),
		Target: target,
	}
	kind, _ := agent["kind"].(string)
	switch kind {
	case "codex":
		req.Options = &sessiondv1.CreateSessionRequest_Codex{
			Codex: &sessiondv1.CodexCreateOptions{
				Cwd:             workspace,
				ParentSessionId: optString(payload.ParentSessionID),
				Model:           optString(payload.Model),
				Provider:        optString(payload.Provider),
				Effort:          optString(payload.Effort),
			},
		}
	case "hermes":
		req.Options = &sessiondv1.CreateSessionRequest_Hermes{
			Hermes: &sessiondv1.HermesCreateOptions{
				ParentSessionId: optString(payload.ParentSessionID),
				Model:           optString(payload.Model),
				Provider:        optString(payload.Provider),
				Effort:          optString(payload.Effort),
			},
		}
	case "antigravity":
		if payload.Provider != "" && payload.Provider != "antigravity" {
			writeJSONError(w, 422, "Antigravity requires its native provider")
			return
		}
		req.Options = &sessiondv1.CreateSessionRequest_Antigravity{Antigravity: &sessiondv1.AntigravityCreateOptions{Cwd: workspace, Model: optString(payload.Model), Effort: optString(payload.Effort), ParentSessionId: optString(payload.ParentSessionID)}}
	case "grok":
		req.Options = &sessiondv1.CreateSessionRequest_Grok{
			Grok: &sessiondv1.GrokCreateOptions{
				Cwd:    workspace,
				Model:  optString(payload.Model),
				Effort: optString(payload.Effort),
			},
		}
	default:
		writeJSONError(w, http.StatusBadRequest, "Unsupported agent kind: "+kind)
		return
	}
	receipt, err := h.client.Session.CreateSession(ctx, req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "Daemon create session failed: "+err.Error())
		return
	}
	if err := confirmedReceipt(receipt); err != nil {
		writeJSONError(w, 502, err.Error())
		return
	}
	if receipt.Session == nil || receipt.Session.GetAgentId() != payload.AgentID || receipt.Session.GetConnectionId() != target.GetConnectionId() {
		writeJSONError(w, 502, "Daemon creation identity mismatch")
		return
	}
	sessionID := ""
	if receipt.Session != nil {
		sessionID = receipt.Session.SessionId
	}
	if sessionID == "" {
		writeJSONError(w, http.StatusBadGateway, "Daemon returned no session ID")
		return
	}
	title := payload.Title
	status, err := nativeStatus(receipt.Status)
	if err != nil {
		writeJSONError(w, 502, err.Error())
		return
	}
	if title == "" {
		title = "New Session"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sessionRow := map[string]any{
		"id":            sessionID,
		"agent_id":      payload.AgentID,
		"title":         title,
		"workspace":     workspace,
		"status":        status,
		"project_id":    payload.ProjectID,
		"project_name":  payload.ProjectName,
		"ephemeral":     payload.Ephemeral,
		"connection_id": agent["connection_id"],
		"control_state": "owned",
		"history_state": "available",
		"native_kind":   "rpc",
		"updated_at":    now,
	}
	upserted, err := h.store.UpsertSession(ctx, sessionRow)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to persist session: "+err.Error())
		return
	}
	h.publishEvent(ctx, "session.upsert", upserted)
	writeJSON(w, http.StatusOK, upserted)
}

func (h *Handler) handleUpdateSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	var payload map[string]any
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid request payload")
		return
	}
	agentID := stringValue(payload, "agent_id")
	if _, present := payload["status"]; present {
		writeJSONError(w, 422, "status is native-owned")
		return
	}
	if agentID == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id is required")
		return
	}
	existing, err := h.store.GetSession(ctx, agentID, sessionID)
	if err != nil || existing == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	updates := make(map[string]any)
	if newTitle := stringValue(payload, "title"); newTitle != "" {
		agent, agentErr := h.store.GetAgent(ctx, agentID)
		if agentErr != nil {
			writeJSONError(w, 500, "Agent storage is unavailable")
			return
		}
		// Grok ACP exposes no title mutation. Astrorder titles are local metadata.
		if stringValue(agent, "kind") != "grok" && stringValue(agent, "kind") != "antigravity" {
			if h.client == nil || h.client.Session == nil {
				writeJSONError(w, 503, "Daemon client is not configured")
				return
			}
			if h.client != nil {
				connID, _ := existing["connection_id"].(string)
				ref := buildSessionRef(agentID, sessionID, connID)
				renameReq := &sessiondv1.RenameSessionRequest{
					Meta:    newCommandMeta("", ""),
					Session: ref,
					Title:   newTitle,
				}
				if receipt, err := h.client.Session.RenameSession(ctx, renameReq); err != nil {
					writeJSONError(w, http.StatusBadGateway, "Rename failed: "+err.Error())
					return
				} else if err := confirmedReceipt(receipt); err != nil {
					writeJSONError(w, http.StatusBadGateway, err.Error())
					return
				}
			}
		}
		updates["title"] = newTitle
	}
	if newWS := stringValue(payload, "workspace"); newWS != "" {
		updates["workspace"] = newWS
	}
	if len(updates) == 0 {
		writeJSON(w, http.StatusOK, existing)
		return
	}
	updated, err := h.store.UpdateSession(ctx, agentID, sessionID, updates)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to update session: "+err.Error())
		return
	}
	h.publishEvent(ctx, "session.upsert", updated)
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleDeleteSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	if h.client == nil || h.client.Session == nil {
		writeJSONError(w, 503, "Daemon client is not configured")
		return
	}
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id is required")
		return
	}
	existing, err := h.store.GetSession(ctx, agentID, sessionID)
	if err != nil || existing == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	if h.client != nil {
		agent, agentErr := h.store.GetAgent(ctx, agentID)
		if agentErr != nil {
			writeJSONError(w, http.StatusInternalServerError, "Agent storage is unavailable")
			return
		}
		if kind := stringValue(agent, "kind"); kind == "codex" || kind == "grok" || kind == "antigravity" {
			if err := h.ensureNativeBinding(ctx, existing); err != nil {
				writeJSONError(w, http.StatusBadGateway, "Native session binding unavailable: "+err.Error())
				return
			}
		}
		connID, _ := existing["connection_id"].(string)
		ref := buildSessionRef(agentID, sessionID, connID)
		delReq := &sessiondv1.DeleteSessionRequest{
			Meta:    newCommandMeta("", ""),
			Session: ref,
		}
		if receipt, err := h.client.Session.DeleteSession(ctx, delReq); err != nil {
			if stringValue(agent, "kind") == "grok" || stringValue(agent, "kind") == "antigravity" || status.Code(err) != codes.NotFound && !strings.Contains(err.Error(), "session is not daemon-owned") {
				writeJSONError(w, http.StatusBadGateway, "Delete failed: "+err.Error())
				return
			}
		} else if err := confirmedReceipt(receipt); err != nil {
			if stringValue(agent, "kind") == "grok" || stringValue(agent, "kind") == "antigravity" || !strings.Contains(err.Error(), "session is not daemon-owned") {
				writeJSONError(w, http.StatusBadGateway, err.Error())
				return
			}
		}
	}
	if _, err := h.store.DeleteSession(ctx, agentID, sessionID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to delete session: "+err.Error())
		return
	}
	h.publishEvent(ctx, "session.delete", map[string]any{"id": sessionID, "agent_id": agentID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": sessionID})
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
