package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Fork uses the native parent's history; the App never copies projected messages.
func (h *Handler) handleForkSession(w http.ResponseWriter, r *http.Request, sid string) {
	var body struct {
		AgentID           string           `json:"agent_id"`
		Title             string           `json:"title"`
		TargetMessageID   *string          `json:"target_message_id"`
		TurnIndex         *int             `json:"turn_index"`
		CreateWorktree    bool             `json:"create_worktree"`
		WorktreeBranch    *string          `json:"worktree_branch"`
		WorktreePath      *string          `json:"worktree_path"`
		WorktreeBaseRef   *string          `json:"worktree_base_ref"`
		WorktreeLayout    string           `json:"worktree_layout"`
		WorktreeCopyFiles []string         `json:"worktree_copy_files"`
		Worktree          *WorktreeOptions `json:"worktree"`
	}
	if decodeJSON(r.Body, &body) != nil || body.AgentID == "" {
		writeJSONError(w, 422, "Invalid fork request")
		return
	}
	parent, err := h.store.GetSession(r.Context(), body.AgentID, sid)
	if err != nil {
		writeJSONError(w, 500, err.Error())
		return
	}
	if parent == nil {
		writeJSONError(w, 404, "Session was not found")
		return
	}
	agent, err := h.store.GetAgent(r.Context(), body.AgentID)
	if err != nil || agent == nil {
		writeJSONError(w, 404, "Agent was not found")
		return
	}
	kind := stringValue(agent, "kind")
	if kind == "antigravity-ssh" {
		kind = "antigravity"
	}
	if kind != "hermes" && kind != "codex" {
		writeJSONError(w, 422, "This Agent does not support native session forks")
		return
	}
	wtOpts := defaultWorktreeOptionsFromLegacy(
		body.CreateWorktree,
		derefString(body.WorktreeBranch),
		derefString(body.WorktreePath),
		derefString(body.WorktreeBaseRef),
		body.WorktreeLayout,
		body.WorktreeCopyFiles,
		body.Worktree,
	)
	if wtOpts != nil && kind == "hermes" {
		writeJSONError(w, 422, "Hermes 原生分叉不支持切换工作树目录，请改用交接分支或在新建会话时创建 Worktree")
		return
	}
	if body.TurnIndex != nil {
		writeJSONError(w, 422, "Fork requires a native message identity")
		return
	}
	if h.client == nil || h.client.Session == nil {
		writeJSONError(w, 503, "Daemon unavailable")
		return
	}
	if err = h.ensureNativeBinding(r.Context(), parent); err != nil {
		writeJSONError(w, 503, err.Error())
		return
	}
	target, err := h.resolveRuntimeTarget(r.Context(), agent)
	if err != nil {
		writeJSONError(w, 422, err.Error())
		return
	}
	nativeID := stringValue(parent, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = stringValue(parent, "title") + " (分叉)"
	}
	effectiveWorkspace := stringValue(parent, "workspace")
	var prepared *preparedWorktree
	if wtOpts != nil {
		var statusCode int
		var detail string
		prepared, statusCode, detail = h.prepareSessionWorktree(r.Context(), agent, effectiveWorkspace, wtOpts)
		if statusCode != 0 {
			writeJSONError(w, statusCode, detail)
			return
		}
		effectiveWorkspace = prepared.Workspace
	}
	commitWorktree := false
	defer func() {
		if !commitWorktree && prepared != nil {
			h.rollbackPreparedWorktree(r.Context(), prepared)
		}
	}()
	fields := map[string]any{"title": title, "cwd": effectiveWorkspace}
	if body.TargetMessageID != nil && *body.TargetMessageID != "" {
		if kind == "antigravity" {
			prefix := "antigravity:" + nativeID + ":"
			if !strings.HasPrefix(*body.TargetMessageID, prefix) {
				writeJSONError(w, 422, "Fork message belongs to another session")
				return
			}
			index, e := strconv.ParseInt(strings.TrimPrefix(*body.TargetMessageID, prefix), 10, 32)
			if e != nil || index < 0 {
				writeJSONError(w, 422, "Invalid native fork message identity")
				return
			}
			fields["target_message_id"] = *body.TargetMessageID
		} else {
			if kind != "hermes" {
				writeJSONError(w, 422, "This Agent does not support forks at a message boundary")
				return
			}
			prefix := "native:" + sid + ":"
			if !strings.HasPrefix(*body.TargetMessageID, prefix) {
				writeJSONError(w, 422, "Fork message belongs to another session")
				return
			}
			rowID, parseErr := strconv.ParseInt(strings.TrimPrefix(*body.TargetMessageID, prefix), 10, 64)
			if parseErr != nil || rowID <= 0 {
				writeJSONError(w, 422, "Invalid native fork message identity")
				return
			}
			fields["target_message_id"] = rowID
		}
	}
	req := &pb.CreateSessionRequest{Meta: newCommandMeta("", ""), Target: target}
	if kind == "hermes" {
		native, _ := encodeNativeJSON("hermes.create.v1", fields)
		req.Options = &pb.CreateSessionRequest_Hermes{Hermes: &pb.HermesCreateOptions{ParentSessionId: &nativeID, NativeOptions: native}}
	} else if kind == "antigravity" {
		req.Options = &pb.CreateSessionRequest_Antigravity{Antigravity: &pb.AntigravityCreateOptions{Cwd: effectiveWorkspace, ParentSessionId: &nativeID, TargetMessageId: body.TargetMessageID}}
	} else {
		req.Options = &pb.CreateSessionRequest_Codex{Codex: &pb.CodexCreateOptions{Cwd: effectiveWorkspace, ParentSessionId: &nativeID}}
	}
	receipt, err := h.client.Session.CreateSession(r.Context(), req)
	if err == nil {
		err = confirmedReceipt(receipt)
	}
	if err != nil {
		writeJSONError(w, 502, err.Error())
		return
	}
	if receipt.Session == nil || receipt.Session.SessionId == "" || receipt.Session.SessionId == nativeID || receipt.Session.GetAgentId() != body.AgentID || receipt.Session.GetConnectionId() != target.GetConnectionId() {
		writeJSONError(w, 502, "Native fork identity was not confirmed; reconcile before retry")
		return
	}
	state, err := nativeStatus(receipt.Status)
	if err != nil {
		writeJSONError(w, 502, err.Error())
		return
	}
	childPayload := map[string]any{
		"id":                      receipt.Session.SessionId,
		"agent_id":                body.AgentID,
		"title":                   title,
		"workspace":               effectiveWorkspace,
		"connection_id":           parent["connection_id"],
		"handoff_from_session_id": sid,
		"handoff_from_agent_id":   body.AgentID,
		"updated_at":              time.Now().UTC().Format(time.RFC3339Nano),
		"project_id":              parent["project_id"],
		"project_name":            parent["project_name"],
		"source_session_id":       receipt.Session.SessionId,
		"status":                  state,
		"control_state":           "owned",
		"history_state":           "available",
	}
	if prepared != nil && prepared.isLinked() {
		childPayload["worktree_root_workspace"] = prepared.RootWorkspace
		childPayload["worktree_branch"] = prepared.Branch
		childPayload["worktree_managed"] = prepared.Managed
	} else if parent["worktree_root_workspace"] != nil {
		childPayload["worktree_root_workspace"] = parent["worktree_root_workspace"]
		childPayload["worktree_branch"] = parent["worktree_branch"]
	}
	child, err := h.store.UpsertSession(r.Context(), childPayload)
	if err != nil {
		writeJSONError(w, 500, err.Error())
		return
	}
	commitWorktree = true
	h.notifyWorktreesChanged(r.Context(), prepared)
	h.publishEvent(r.Context(), "session.upsert", child)
	writeJSON(w, 200, child)
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
