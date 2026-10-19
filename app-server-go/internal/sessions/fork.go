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
		AgentID         string  `json:"agent_id"`
		Title           string  `json:"title"`
		TargetMessageID *string `json:"target_message_id"`
		TurnIndex       *int    `json:"turn_index"`
		CreateWorktree  bool    `json:"create_worktree"`
		WorktreeBranch  *string `json:"worktree_branch"`
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
	if kind != "hermes" && kind != "codex" && kind != "antigravity" {
		writeJSONError(w, 422, "This Agent does not support native session forks")
		return
	}
	if body.CreateWorktree {
		writeJSONError(w, 422, "Creating a Git worktree during a session fork is unavailable")
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
	fields := map[string]any{"title": title, "cwd": stringValue(parent, "workspace")}
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
		req.Options = &pb.CreateSessionRequest_Antigravity{Antigravity: &pb.AntigravityCreateOptions{Cwd: stringValue(parent, "workspace"), ParentSessionId: &nativeID, TargetMessageId: body.TargetMessageID}}
	} else {
		req.Options = &pb.CreateSessionRequest_Codex{Codex: &pb.CodexCreateOptions{ParentSessionId: &nativeID}}
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
	child, err := h.store.UpsertSession(r.Context(), map[string]any{"id": receipt.Session.SessionId, "agent_id": body.AgentID, "title": title, "workspace": parent["workspace"], "connection_id": parent["connection_id"], "handoff_from_session_id": sid, "handoff_from_agent_id": body.AgentID, "updated_at": time.Now().UTC().Format(time.RFC3339Nano), "project_id": parent["project_id"], "project_name": parent["project_name"], "source_session_id": receipt.Session.SessionId, "status": state, "control_state": "owned", "history_state": "available"})
	if err != nil {
		writeJSONError(w, 500, err.Error())
		return
	}
	h.publishEvent(r.Context(), "session.upsert", child)
	writeJSON(w, 200, child)
}
