package sessions

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// handleContextBranchSession creates a brand-new session on the same Agent seeded
// with a deterministic local transcript excerpt up to an optional target_message_id.
// This enables message-level branching for runtimes such as Antigravity ACP that do
// not expose native session/fork, without triggering automated AI-to-AI prompts.
func (h *Handler) handleContextBranchSession(w http.ResponseWriter, r *http.Request, sid string) {
	var body struct {
		AgentID           string           `json:"agent_id"`
		Title             string           `json:"title"`
		TargetMessageID   *string          `json:"target_message_id"`
		CreateWorktree    bool             `json:"create_worktree"`
		WorktreeBranch    *string          `json:"worktree_branch"`
		WorktreePath      *string          `json:"worktree_path"`
		WorktreeBaseRef   *string          `json:"worktree_base_ref"`
		WorktreeLayout    string           `json:"worktree_layout"`
		WorktreeCopyFiles []string         `json:"worktree_copy_files"`
		Worktree          *WorktreeOptions `json:"worktree"`
	}
	if decodeJSON(r.Body, &body) != nil || strings.TrimSpace(body.AgentID) == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid context branch request")
		return
	}
	ctx := r.Context()
	parent, err := h.store.GetSession(ctx, body.AgentID, sid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if parent == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	agent, err := h.store.GetAgent(ctx, body.AgentID)
	if err != nil || agent == nil {
		writeJSONError(w, http.StatusNotFound, "Agent was not found")
		return
	}
	if agent["status"] != "ready" {
		writeJSONError(w, http.StatusConflict, "Agent must be ready")
		return
	}
	kind := stringValue(agent, "kind")
	if kind == "antigravity-ssh" {
		kind = "antigravity"
	}
	if kind != "antigravity" && kind != "codex" && kind != "hermes" && kind != "grok" {
		writeJSONError(w, http.StatusUnprocessableEntity, "This Agent does not support context branching")
		return
	}
	if h.client == nil || h.client.Session == nil || h.client.Runtime == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon unavailable")
		return
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if kind != "codex" {
		if err = h.ensureNativeBinding(ctx, parent); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
	}

	nativeID := stringValue(parent, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	connection := normalizeConnectionID(stringValue(agent, "connection_id"))
	sourceRef := buildSessionRef(body.AgentID, nativeID, connection)

	items, err := h.handoffMessages(ctx, agent, kind, sourceRef)
	if err != nil {
		writeQueueError(w, err)
		return
	}

	targetMessageID := ""
	if body.TargetMessageID != nil {
		targetMessageID = strings.TrimSpace(*body.TargetMessageID)
	}
	if targetMessageID != "" {
		cutIdx := -1
		for idx, item := range items {
			if stringValue(item, "id") == targetMessageID {
				cutIdx = idx
			}
		}
		if cutIdx < 0 {
			writeJSONError(w, http.StatusUnprocessableEntity, "Fork message was not found in this session")
			return
		}
		items = items[:cutIdx+1]
	}

	workspace := stringValue(parent, "workspace")
	wtOpts := defaultWorktreeOptionsFromLegacy(
		body.CreateWorktree,
		derefString(body.WorktreeBranch),
		derefString(body.WorktreePath),
		derefString(body.WorktreeBaseRef),
		body.WorktreeLayout,
		body.WorktreeCopyFiles,
		body.Worktree,
	)
	var prepared *preparedWorktree
	if wtOpts != nil {
		var statusCode int
		var detail string
		prepared, statusCode, detail = h.prepareSessionWorktree(ctx, agent, workspace, wtOpts)
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
	title := strings.TrimSpace(body.Title)
	if title == "" {
		baseTitle := strings.TrimSpace(stringValue(parent, "title"))
		if baseTitle == "" {
			baseTitle = "新会话"
		}
		title = baseTitle + " (交接分支)"
	}

	handoffContextValue := buildContextBranchHandoffContext(kind, sid, workspace, items, targetMessageID != "")

	// Best-effort inherit current session model/effort without blocking on read failures.
	provider := strings.TrimSpace(stringValue(parent, "selected_model_provider"))
	modelName := strings.TrimSpace(stringValue(parent, "selected_model"))
	effort := strings.TrimSpace(stringValue(parent, "selected_reasoning_effort"))
	if liveModel, readErr := h.client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: sourceRef}); readErr == nil && liveModel != nil {
		if m := strings.TrimSpace(liveModel.GetModel()); m != "" {
			modelName = m
		}
		if p := strings.TrimSpace(liveModel.GetProvider()); p != "" {
			provider = p
		}
		if e := strings.TrimSpace(liveModel.GetEffort()); e != "" {
			effort = e
		}
	}
	if kind == "antigravity" {
		if isAntigravityInternalWorkerModel(modelName) {
			modelName = ""
		}
		if modelName != "" {
			provider = "antigravity"
			effort = ""
		}
	}

	created, err := h.createHandoffNative(ctx, target, kind, workspace, title, "", provider, modelName, effort, false)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	targetID := created.Session.SessionId
	if targetID == "" || targetID == nativeID {
		writeJSONError(w, http.StatusBadGateway, "Context branch session identity was not confirmed")
		return
	}

	sessionFields := map[string]any{
		"id":                        targetID,
		"source_session_id":         targetID,
		"agent_id":                  body.AgentID,
		"title":                     title,
		"workspace":                 workspace,
		"connection_id":             agent["connection_id"],
		"project_id":                parent["project_id"],
		"project_name":              parent["project_name"],
		"status":                    "idle",
		"control_state":             "owned",
		"history_state":             "available",
		"native_kind":               "rpc",
		"updated_at":                time.Now().UTC().Format(time.RFC3339Nano),
		"selected_model_provider":   provider,
		"selected_model":            modelName,
		"selected_reasoning_effort": effort,
		"handoff_from_agent_id":     body.AgentID,
		"handoff_from_session_id":   sid,
		"handoff_context":           handoffContextValue,
	}
	if prepared != nil && prepared.isLinked() {
		sessionFields["worktree_root_workspace"] = prepared.RootWorkspace
		sessionFields["worktree_branch"] = prepared.Branch
		sessionFields["worktree_managed"] = prepared.Managed
	} else if parent["worktree_root_workspace"] != nil {
		sessionFields["worktree_root_workspace"] = parent["worktree_root_workspace"]
		sessionFields["worktree_branch"] = parent["worktree_branch"]
	}
	row, err := h.store.UpsertSession(ctx, sessionFields)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	err = h.store.WithTx(ctx, func(tx *sql.Tx) error {
		_, txErr := tx.ExecContext(
			ctx,
			`UPDATE sessions SET title=?,handoff_from_agent_id=?,handoff_from_session_id=?,handoff_context=?,selected_model_provider=?,selected_model=?,selected_reasoning_effort=? WHERE agent_id=? AND id=? AND handoff_context IS NULL`,
			title, body.AgentID, sid, handoffContextValue, provider, modelName, effort, body.AgentID, targetID,
		)
		return txErr
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if refreshed, getErr := h.store.GetSession(ctx, body.AgentID, targetID); getErr == nil && refreshed != nil {
		row = refreshed
	}
	if modelName != "" {
		if restoreErr := h.restoreModel(ctx, created.Session); restoreErr != nil {
			slog.Warn("context branch model restore failed", "agent_id", body.AgentID, "session_id", targetID, "model", modelName, "error", restoreErr)
		}
	}
	commitWorktree = true
	h.notifyWorktreesChanged(ctx, prepared)
	h.publishEvent(ctx, "session.upsert", row)
	writeJSON(w, http.StatusOK, row)
}

func buildContextBranchHandoffContext(kind, sourceSessionID, workspace string, items []map[string]any, atMessageBoundary bool) string {
	base := buildDeterministicHandoffContext(kind, sourceSessionID, workspace, items)
	if !atMessageBoundary {
		return base
	}
	excerpt, _ := extractInlineHandoffSummary(base)
	return fmt.Sprintf(
		"以下是由 %s 从指定回复节点分叉的新会话背景（截止到选定回复，之后的消息未包含）。它只提供上下文；按用户的新消息继续工作。\n来源会话：%s\n工作目录：%s\n\n<源会话历史记录摘录>\n%s\n</源会话历史记录摘录>",
		kind, sourceSessionID, workspace, excerpt,
	)
}
