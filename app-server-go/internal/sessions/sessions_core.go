package sessions

import (
	"context"
	"net/http"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/connections"
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
	AgentID         string           `json:"agent_id"`
	Workspace       string           `json:"workspace"`
	Title           string           `json:"title"`
	ProjectID       string           `json:"project_id"`
	ProjectName     string           `json:"project_name"`
	ParentSessionID string           `json:"parent_session_id"`
	Ephemeral       bool             `json:"ephemeral"`
	Provider        string           `json:"provider"`
	Model           string           `json:"model"`
	Effort          string           `json:"effort"`
	Worktree        *WorktreeOptions `json:"worktree,omitempty"`
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
		workspace = connections.RemoteWorkspace(workspace, target.Ssh.GetWorkspace())
		if workspace == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "an absolute SSH workspace path is required")
			return
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
	var prepared *preparedWorktree
	if payload.Worktree != nil && payload.Worktree.activeMode() != "" {
		var statusCode int
		var detail string
		prepared, statusCode, detail = h.prepareSessionWorktree(ctx, agent, workspace, payload.Worktree)
		if statusCode != 0 {
			writeJSONError(w, statusCode, detail)
			return
		}
		workspace = prepared.Workspace
	}
	commitWorktree := false
	defer func() {
		if !commitWorktree && prepared != nil {
			h.rollbackPreparedWorktree(ctx, prepared)
		}
	}()
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
		hermesFields := map[string]any{"cwd": workspace}
		if t := strings.TrimSpace(payload.Title); t != "" {
			hermesFields["title"] = t
		}
		native, _ := encodeNativeJSON("hermes.create.v1", hermesFields)
		req.Options = &sessiondv1.CreateSessionRequest_Hermes{
			Hermes: &sessiondv1.HermesCreateOptions{
				ParentSessionId: optString(payload.ParentSessionID),
				Model:           optString(payload.Model),
				Provider:        optString(payload.Provider),
				Effort:          optString(payload.Effort),
				NativeOptions:   native,
			},
		}
	case "antigravity", "antigravity-ssh":
		if payload.Provider != "" && payload.Provider != "antigravity" {
			writeJSONError(w, 422, "Antigravity requires its native provider")
			return
		}
		agyEffort := payload.Effort
		if payload.Model != "" {
			agyEffort = ""
		}
		req.Options = &sessiondv1.CreateSessionRequest_Antigravity{Antigravity: &sessiondv1.AntigravityCreateOptions{Cwd: workspace, Model: optString(payload.Model), Effort: optString(agyEffort), ParentSessionId: optString(payload.ParentSessionID)}}
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
	projectName := payload.ProjectName
	if projectName == "" && prepared != nil && prepared.isLinked() {
		projectName = workspaceBase(prepared.RootWorkspace)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sessionRow := map[string]any{
		"id":            sessionID,
		"agent_id":      payload.AgentID,
		"title":         title,
		"workspace":     workspace,
		"status":        status,
		"project_id":    payload.ProjectID,
		"project_name":  projectName,
		"ephemeral":     payload.Ephemeral,
		"connection_id": agent["connection_id"],
		"control_state": "owned",
		"history_state": "available",
		"native_kind":   "rpc",
		"updated_at":    now,
	}
	if prepared != nil && prepared.isLinked() {
		sessionRow["worktree_root_workspace"] = prepared.RootWorkspace
		sessionRow["worktree_branch"] = prepared.Branch
		sessionRow["worktree_managed"] = prepared.Managed
	}
	upserted, err := h.store.UpsertSession(ctx, sessionRow)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to persist session: "+err.Error())
		return
	}
	commitWorktree = true
	h.notifyWorktreesChanged(ctx, prepared)
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
		agentKind := stringValue(agent, "kind")
		if agentKind != "grok" && agentKind != "grok-ssh" && agentKind != "antigravity" && agentKind != "antigravity-ssh" {
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
	if newWS := strings.TrimSpace(stringValue(payload, "workspace")); newWS != "" {
		currentWS := strings.TrimSpace(stringValue(existing, "workspace"))
		if !sameWorkspacePath(currentWS, newWS) {
			agent, agentErr := h.store.GetAgent(ctx, agentID)
			if agentErr != nil {
				writeJSONError(w, http.StatusInternalServerError, "Agent storage is unavailable")
				return
			}
			agentKind := stringValue(agent, "kind")
			if supportsNativeWorkspaceMove(agentKind) {
				if h.client == nil || h.client.Session == nil {
					writeJSONError(w, http.StatusServiceUnavailable, "Daemon client is not configured")
					return
				}
				currentStatus := stringValue(existing, "status")
				if currentStatus == "running" || currentStatus == "waiting_approval" {
					writeJSONError(w, http.StatusConflict, "请等待会话空闲后再移动到其他项目")
					return
				}
				if agentKind == "antigravity" || agentKind == "antigravity-ssh" {
					if err := h.ensureNativeBindingForDelete(ctx, existing); err != nil {
						writeJSONError(w, http.StatusBadGateway, "无法建立原生会话连接："+err.Error())
						return
					}
				} else {
					if err := h.ensureNativeBinding(ctx, existing); err != nil {
						writeJSONError(w, http.StatusBadGateway, "无法建立原生会话连接："+err.Error())
						return
					}
				}
				connID, _ := existing["connection_id"].(string)
				nativeID := stringValue(existing, "source_session_id")
				if nativeID == "" {
					nativeID = sessionID
				}
				ref := buildSessionRef(agentID, nativeID, connID)
				moveReq := &sessiondv1.MoveSessionWorkspaceRequest{
					Meta:      newCommandMeta("", ""),
					Session:   ref,
					Workspace: newWS,
				}
				receipt, moveErr := h.client.Session.MoveSessionWorkspace(ctx, moveReq)
				if moveErr != nil {
					code := status.Code(moveErr)
					switch code {
					case codes.Unimplemented:
						writeJSONError(w, http.StatusConflict, "当前运行的小内核尚未激活移动项目能力，请先通过系统托盘「小内核 → 更新小内核」完成更新后重试")
					case codes.InvalidArgument, codes.FailedPrecondition:
						writeJSONError(w, http.StatusConflict, status.Convert(moveErr).Message())
					default:
						writeJSONError(w, http.StatusBadGateway, "移动会话项目失败："+moveErr.Error())
					}
					return
				}
				if receiptErr := confirmedReceipt(receipt); receiptErr != nil {
					writeJSONError(w, http.StatusConflict, receiptErr.Error())
					return
				}
				canonicalWS := newWS
				var nativePayload map[string]any
				if parseNativeJSON(receipt.GetCompleted().GetNative(), &nativePayload) == nil {
					if ws, ok := nativePayload["workspace"].(string); ok && strings.TrimSpace(ws) != "" {
						canonicalWS = strings.TrimSpace(ws)
					}
				}
				updates["workspace"] = canonicalWS
				updates["control_state"] = "unbound"
				updates["clear_worktree"] = true
				projectID, projectName := h.matchCatalogProjectForWorkspace(ctx, connID, canonicalWS)
				if projectID != "" {
					updates["project_id"] = projectID
					updates["project_name"] = projectName
				} else {
					updates["project_id"] = nil
					updates["project_name"] = nil
				}
			} else {
				updates["workspace"] = newWS
			}
		}
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
		agentKind := stringValue(agent, "kind")
		isAntigravity := agentKind == "antigravity" || agentKind == "antigravity-ssh"
		isGrok := agentKind == "grok" || agentKind == "grok-ssh"
		if isAntigravity {
			if err := h.ensureNativeBindingForDelete(ctx, existing); err != nil {
				writeJSONError(w, http.StatusBadGateway, "Native session binding unavailable: "+err.Error())
				return
			}
		} else if agentKind == "codex" || agentKind == "codex-ssh" || isGrok {
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
			if isGrok || isAntigravity || status.Code(err) != codes.NotFound && !strings.Contains(err.Error(), "session is not daemon-owned") {
				writeJSONError(w, http.StatusBadGateway, "Delete failed: "+err.Error())
				return
			}
		} else if err := confirmedReceipt(receipt); err != nil {
			if isGrok || isAntigravity || !strings.Contains(err.Error(), "session is not daemon-owned") {
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
	resp := map[string]any{"ok": true, "id": sessionID}
	q := r.URL.Query()
	if q.Get("remove_worktree") == "1" || strings.EqualFold(q.Get("remove_worktree"), "true") {
		connID := stringValue(existing, "connection_id")
		ws := stringValue(existing, "workspace")
		otherCount, countErr := h.store.CountOtherSessionsUsingWorkspace(ctx, agentID, sessionID, connID, ws)
		if countErr != nil {
			resp["worktree_removed"] = false
			resp["worktree_error"] = countErr.Error()
		} else if otherCount > 0 {
			resp["worktree_removed"] = false
			resp["worktree_error"] = "仍有其他会话绑定该 Worktree，已保留目录"
		} else {
			forceWT := q.Get("force_remove_worktree") == "1" || strings.EqualFold(q.Get("force_remove_worktree"), "true")
			deleteBranch := q.Get("delete_worktree_branch") == "1" || strings.EqualFold(q.Get("delete_worktree_branch"), "true")
			removed, branchDeleted, wtErr := h.removeSessionWorktree(ctx, existing, forceWT, deleteBranch)
			resp["worktree_removed"] = removed
			resp["worktree_branch_deleted"] = branchDeleted
			if wtErr != "" {
				resp["worktree_error"] = wtErr
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func supportsNativeWorkspaceMove(agentKind string) bool {
	switch agentKind {
	case "antigravity", "antigravity-ssh", "codex", "codex-ssh", "hermes", "ssh", "grok", "grok-ssh":
		return true
	default:
		return false
	}
}

func normalizeWorkspaceCompare(raw string) string {
	ws := strings.TrimSpace(raw)
	if ws == "" {
		return ""
	}
	slashed := strings.ReplaceAll(ws, "\\", "/")
	isWin := (len(slashed) >= 2 && ((slashed[0] >= 'a' && slashed[0] <= 'z') || (slashed[0] >= 'A' && slashed[0] <= 'Z')) && slashed[1] == ':') || strings.HasPrefix(slashed, "//")
	slashed = strings.TrimRight(slashed, "/")
	if slashed == "" {
		return "/"
	}
	if isWin {
		return strings.ToLower(slashed)
	}
	return slashed
}

func (h *Handler) matchCatalogProjectForWorkspace(ctx context.Context, connectionID, workspace string) (string, string) {
	projects, err := h.store.ListProjects(ctx)
	if err != nil {
		return "", ""
	}
	targetNorm := normalizeWorkspaceCompare(workspace)
	if targetNorm == "" {
		return "", ""
	}
	connNorm := strings.TrimSpace(connectionID)
	if connNorm == "local" {
		connNorm = ""
	}
	for _, p := range projects {
		pid := stringValue(p, "project_id")
		if pid == "" || pid == "__no_project__" {
			continue
		}
		pConn := stringValue(p, "connection_id")
		if pConn == "local" {
			pConn = ""
		}
		if pConn != connNorm {
			continue
		}
		if normalizeWorkspaceCompare(stringValue(p, "workspace")) == targetNorm {
			return pid, stringValue(p, "project_name")
		}
	}
	return "", ""
}
