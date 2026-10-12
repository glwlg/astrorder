package sessions

import (
	"net/http"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"

	"astrorder.dev/app-server/internal/store"
)

// handleSyncSession performs one read-only daemon status refresh for the exact
// composite session. It never attaches, prompts, or clears state based on
// command heuristics: only an explicitly owned, exactly identified snapshot
// may update the projected status, and the write itself is guarded inside the
// store transaction so a deletion or rebinding that raced the authority read
// is rejected instead of overwritten.
func (h *Handler) handleSyncSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "agent_id is required")
		return
	}
	row, err := h.store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Session storage is unavailable")
		return
	}
	if row == nil {
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	}
	if h.client == nil || h.client.Daemon == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon client is not configured")
		return
	}
	status, err := h.client.Daemon.GetStatus(ctx, &sessiondv1.GetStatusRequest{})
	if err != nil || status == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon status is unavailable; session state was not changed")
		return
	}
	if status.GetDaemonId() != h.client.DaemonID() {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon identity changed; session state was not changed")
		return
	}
	if status.GetStorageError() != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon storage is unavailable; session state was not changed")
		return
	}
	agent, err := h.store.GetAgent(ctx, agentID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Agent storage is unavailable")
		return
	}
	nativeID := stringValue(row, "source_session_id")
	if nativeID == "" {
		nativeID = sessionID
	}
	var matched *sessiondv1.SessionSnapshot
	for _, snapshot := range status.Sessions {
		if snapshotMatchesOwnedSession(snapshot, agent, agentID, nativeID, stringValue(row, "connection_id")) {
			matched = snapshot
			break
		}
	}
	if matched == nil {
		if stringValue(row, "control_state") == "owned" {
			writeJSONError(w, http.StatusServiceUnavailable, "Daemon does not confirm live ownership for this session; state was not changed")
			return
		}
		// A non-owned historical projection is returned as-is: the daemon
		// reports no live owner, and the App row keeps its stored state.
		writeJSON(w, http.StatusOK, row)
		return
	}
	latest, err := nativeStatus(matched.Status)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Daemon session status is unconfirmed; state was not changed")
		return
	}
	// The status write is guarded by the composite binding observed above and
	// re-checked inside the store transaction: a row deleted or rebound while
	// GetStatus was in flight is never resurrected or overwritten, and only
	// status/updated_at change, so concurrent metadata edits survive.
	expect := store.RefreshSessionStatusExpectation{
		NativeID:     nativeID,
		ConnectionID: stringValue(row, "connection_id"),
		ControlState: stringValue(row, "control_state"),
		NativeKind:   stringValue(row, "native_kind"),
	}
	updated, outcome, err := h.store.RefreshSessionStatus(ctx, agentID, sessionID, expect, latest)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to persist session: "+err.Error())
		return
	}
	switch outcome {
	case store.RefreshSessionStatusApplied:
		h.publishEvent(ctx, "session.upsert", updated)
	case store.RefreshSessionStatusGone:
		writeJSONError(w, http.StatusNotFound, "Session was not found")
		return
	case store.RefreshSessionStatusRebound:
		writeJSONError(w, http.StatusConflict, "Session binding changed while refreshing; state was not changed")
		return
	case store.RefreshSessionStatusCurrent:
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "Session refresh outcome is unconfirmed; state was not changed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// snapshotMatchesOwnedSession accepts only a snapshot that explicitly reports
// live ownership, carries the exact composite identity (agent, native session
// id, connection), and whose typed runtime corresponds to the agent kind.
// Control/probe sessions are never chat sessions.
func snapshotMatchesOwnedSession(snapshot *sessiondv1.SessionSnapshot, agent map[string]any, agentID, nativeID, connectionID string) bool {
	if snapshot == nil || snapshot.Session == nil {
		return false
	}
	if snapshot.RuntimeOwned == nil || !snapshot.GetRuntimeOwned() {
		return false
	}
	if snapshot.Runtime == nil {
		return false
	}
	if isControlSessionID(snapshot.Session.SessionId) {
		return false
	}
	if snapshot.Session.GetAgentId() != agentID || snapshot.Session.SessionId != nativeID {
		return false
	}
	if normalizeConnectionID(snapshot.Session.GetConnectionId()) != normalizeConnectionID(connectionID) {
		return false
	}
	if agent == nil {
		return false
	}
	for _, kind := range expectedRuntimeKinds(stringValue(agent, "kind"), connectionID) {
		if snapshot.GetRuntime() == kind {
			return true
		}
	}
	return false
}
