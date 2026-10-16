package sessions

import (
	"errors"
	"net/http"
	"strings"
)

type queueError struct {
	status int
	msg    string
}

func (e *queueError) Error() string   { return e.msg }
func (e *queueError) HTTPStatus() int { return e.status }

type statusError interface {
	HTTPStatus() int
}

func writeQueueError(w http.ResponseWriter, err error) {
	var se statusError
	if errors.As(err, &se) {
		writeJSONError(w, se.HTTPStatus(), err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, err.Error())
}

func (h *Handler) handleListCommands(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id is required")
		return
	}
	session, err := h.store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	if err := h.reconcileHermesAttachmentRejections(ctx); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := h.store.ListCommands(ctx, agentID, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleListTasks(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id is required")
		return
	}
	session, err := h.store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	items, err := h.store.ListTasks(ctx, agentID, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type editQueuedPayload struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

func (h *Handler) handleEditQueuedCommand(w http.ResponseWriter, r *http.Request, commandID string) {
	ctx := r.Context()
	var payload editQueuedPayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid JSON payload")
		return
	}
	aid := payload.AgentID
	sid := payload.SessionID
	if aid == "" || sid == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id and session_id are required")
		return
	}
	session, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	updated, err := h.store.UpdateQueuedCommandText(ctx, aid, sid, commandID, payload.Text)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSONError(w, http.StatusNotFound, "Command was not found")
			return
		}
		if strings.Contains(err.Error(), "no longer queued") {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.publisher.Publish(ctx, "command.upsert", updated); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleDeleteQueuedCommand(w http.ResponseWriter, r *http.Request, commandID string) {
	ctx := r.Context()
	aid := r.URL.Query().Get("agent_id")
	sid := r.URL.Query().Get("session_id")
	if aid == "" || sid == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id and session_id are required")
		return
	}
	session, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	updated, cancelled, err := h.store.CancelQueuedCommand(ctx, aid, sid, commandID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updated == nil {
		writeJSONError(w, http.StatusNotFound, "Command was not found")
		return
	}
	if !cancelled {
		writeJSONError(w, http.StatusConflict, "Command is no longer queued")
		return
	}

	if err := h.publisher.Publish(ctx, "command.upsert", updated); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

type sendQueuedPayload struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

func (h *Handler) handleSendQueuedCommand(w http.ResponseWriter, r *http.Request, commandID string) {
	ctx := r.Context()
	var payload sendQueuedPayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid JSON payload")
		return
	}
	aid := payload.AgentID
	sid := payload.SessionID
	if aid == "" || sid == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id and session_id are required")
		return
	}
	session, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	claimedCmd, claimed, err := h.store.ClaimQueuedCommand(ctx, aid, sid, commandID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSONError(w, http.StatusNotFound, "Command was not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !claimed {
		writeJSONError(w, http.StatusConflict, "Command is no longer queued")
		return
	}
	res, err := h.submitStored(ctx, claimedCmd)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
