package sessions

import (
	"net/http"
	"sort"
	"strings"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// Presence wire shape is preserved from the existing contract:
// {"items":[{agent_id,id,last_user_at}],"open":[{agent_id,id}],"live":[{agent_id,id}]}.
func (h *Handler) handlePresence(w http.ResponseWriter, r *http.Request) {
	projection, err := h.collectPresence(r)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Presence is unavailable: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": projection.items,
		"open":  projection.open,
		"live":  projection.live,
	})
}

// handleUserActivity preserves {"items":[...],"live":[...]} without the
// open-session subset.
func (h *Handler) handleUserActivity(w http.ResponseWriter, r *http.Request) {
	projection, err := h.collectPresence(r)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "User activity is unavailable: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": projection.items,
		"live":  projection.live,
	})
}

type presenceProjection struct {
	items []map[string]any
	open  []map[string]any
	live  []map[string]any
}

// collectPresence projects App-owned metadata only: the last projected user
// message per exact (agent, session) composite, plus open/live ownership read
// from the daemon's authoritative GetStatus. A daemon read that cannot be
// trusted (failure, epoch mismatch, storage error) fails closed with an error
// instead of being reported as known-idle. Native Hermes full history is not
// read here; the coverage limit is that this reflects only App-projected rows.
func (h *Handler) collectPresence(r *http.Request) (presenceProjection, error) {
	ctx := r.Context()
	projection := presenceProjection{items: []map[string]any{}, open: []map[string]any{}, live: []map[string]any{}}

	rows, err := h.store.PresenceActivityRows(ctx)
	if err != nil {
		return projection, err
	}
	for _, row := range rows {
		projection.items = append(projection.items, row)
	}
	sort.Slice(projection.items, func(i, j int) bool {
		left, right := projection.items[i], projection.items[j]
		if left["agent_id"] != right["agent_id"] {
			return left["agent_id"].(string) < right["agent_id"].(string)
		}
		return left["id"].(string) < right["id"].(string)
	})

	if h.client == nil || h.client.Daemon == nil {
		return projection, errDaemonPresenceUnavailable
	}
	status, err := h.client.Daemon.GetStatus(ctx, &sessiondv1.GetStatusRequest{})
	if err != nil || status == nil {
		return projection, errDaemonPresenceUnavailable
	}
	if status.GetDaemonId() != h.client.DaemonID() {
		return projection, errDaemonPresenceUnavailable
	}
	if status.GetStorageError() != nil {
		return projection, errDaemonPresenceUnavailable
	}
	agents, err := h.store.ListAgents(ctx)
	if err != nil {
		return projection, err
	}
	agentByID := make(map[string]map[string]any, len(agents))
	for _, agent := range agents {
		agentByID[stringValue(agent, "id")] = agent
	}
	sessions, err := h.store.ListSessions(ctx, "")
	if err != nil {
		return projection, err
	}
	for _, snapshot := range status.Sessions {
		if snapshot == nil || snapshot.Session == nil {
			continue
		}
		if isControlSessionID(snapshot.Session.SessionId) {
			continue
		}
		agentID := snapshot.Session.GetAgentId()
		if agentID == "" {
			continue
		}
		row := matchLiveOwnedSession(sessions, agentByID[agentID], snapshot)
		if row == nil {
			continue
		}
		ref := map[string]any{"agent_id": agentID, "id": stringValue(row, "id")}
		switch snapshot.GetStatus() {
		case sessiondv1.SessionStatus_SESSION_STATUS_RUNNING, sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
			// Confirmed active work; never inferred from updated_at.
			projection.open = append(projection.open, ref)
			projection.live = append(projection.live, ref)
		case sessiondv1.SessionStatus_SESSION_STATUS_IDLE:
			projection.open = append(projection.open, ref)
		default:
			// An unconfirmed status must not be reported as open or live.
		}
	}
	return projection, nil
}

var errDaemonPresenceUnavailable = &presenceError{"Daemon status is unavailable"}

type presenceError struct{ detail string }

func (e *presenceError) Error() string { return e.detail }

// matchLiveOwnedSession resolves the exact App composite for one live-owned
// snapshot: the snapshot's native id must equal the stored source_session_id
// (falling back to the row id), and ownership requires explicit
// RuntimeOwned=true with the typed runtime matching the agent kind and the
// connection identity agreeing on both sides. Two connections sharing one
// native id resolve independently by connection.
func matchLiveOwnedSession(sessions []map[string]any, agent map[string]any, snapshot *sessiondv1.SessionSnapshot) map[string]any {
	if snapshot.RuntimeOwned == nil || !snapshot.GetRuntimeOwned() || snapshot.Runtime == nil || agent == nil {
		return nil
	}
	agentID := snapshot.Session.GetAgentId()
	nativeID := snapshot.Session.SessionId
	for _, row := range sessions {
		if stringValue(row, "agent_id") != agentID {
			continue
		}
		rowNative := stringValue(row, "source_session_id")
		if rowNative == "" {
			rowNative = stringValue(row, "id")
		}
		if rowNative != nativeID {
			continue
		}
		connectionID := stringValue(row, "connection_id")
		if normalizeConnectionID(snapshot.Session.GetConnectionId()) != normalizeConnectionID(connectionID) {
			continue
		}
		for _, kind := range expectedRuntimeKinds(stringValue(agent, "kind"), connectionID) {
			if snapshot.GetRuntime() == kind {
				return row
			}
		}
	}
	return nil
}

// expectedRuntimeKinds maps an App agent kind to the typed runtime kinds that
// may own its sessions, honoring the local/SSH split.
func expectedRuntimeKinds(kind, connectionID string) []sessiondv1.RuntimeKind {
	ssh := connectionID != "" && connectionID != "local"
	switch kind {
	case "codex":
		if ssh {
			return []sessiondv1.RuntimeKind{sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX_SSH}
		}
		return []sessiondv1.RuntimeKind{sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX}
	case "hermes":
		if ssh {
			return []sessiondv1.RuntimeKind{sessiondv1.RuntimeKind_RUNTIME_KIND_SSH_HERMES}
		}
		return []sessiondv1.RuntimeKind{sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES}
	case "antigravity":
		if ssh { return nil }
		return []sessiondv1.RuntimeKind{sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY}
	case "grok":
		if ssh {
			return []sessiondv1.RuntimeKind{sessiondv1.RuntimeKind_RUNTIME_KIND_GROK_SSH}
		}
		return []sessiondv1.RuntimeKind{sessiondv1.RuntimeKind_RUNTIME_KIND_GROK}
	default:
		return nil
	}
}

// normalizeConnectionID folds the local connection aliases so "local" and ""
// compare equal on both the store and daemon sides.
func normalizeConnectionID(value string) string {
	if value == "local" {
		return ""
	}
	return value
}

// isControlSessionID rejects daemon control/probe sessions; they are not chat
// sessions and must never be reported as user activity or live chat.
func isControlSessionID(sessionID string) bool {
	return strings.HasPrefix(sessionID, "probe-") || strings.HasPrefix(sessionID, "control-")
}
