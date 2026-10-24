package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func sleepPreferenceKey(aid, sid string) string {
	encoded, _ := json.Marshal([]string{aid, sid})
	return "session_sleep:" + string(encoded)
}

// RecoverDisconnectedSessions resumes failed owned bindings only after their
// Agent connection has been confirmed. It never sends or replays a command.
func (h *Handler) RecoverDisconnectedSessions(ctx context.Context, connectionID, kind string) error {
	agents, err := h.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, agent := range agents {
		if normalizeConnectionID(stringValue(agent, "connection_id")) != normalizeConnectionID(connectionID) || stringValue(agent, "kind") != kind {
			continue
		}
		if err := h.requireAgentConnection(ctx, agent); err != nil {
			return err
		}
		aid := stringValue(agent, "id")
		rows, err := h.store.ListSessions(ctx, aid)
		if err != nil {
			return err
		}
		for _, row := range rows {
			pref, prefErr := h.store.GetPreference(ctx, sleepPreferenceKey(aid, stringValue(row, "id")))
			if prefErr != nil {
				return prefErr
			}
			if pref.Value == true {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if stringValue(row, "control_state") != "owned" || stringValue(row, "status") != "error" || normalizeConnectionID(stringValue(row, "connection_id")) != normalizeConnectionID(connectionID) {
				continue
			}
			// Recheck before attaching: another recovery or a user action may already
			// have changed the row since this scan began.
			row, err = h.store.GetSession(ctx, aid, stringValue(row, "id"))
			if err != nil {
				return err
			}
			if row == nil || stringValue(row, "status") != "error" || stringValue(row, "control_state") != "owned" || normalizeConnectionID(stringValue(row, "connection_id")) != normalizeConnectionID(connectionID) {
				continue
			}
			sessionCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err = h.recoverDisconnectedSession(sessionCtx, agent, row)
			cancel()
			if err != nil {
				failures = append(failures, fmt.Errorf("session %s/%s: %w", aid, stringValue(row, "id"), err))
			}
		}
	}
	return errors.Join(failures...)
}

func (h *Handler) recoverDisconnectedSession(ctx context.Context, agent, row map[string]any) error {
	if err := h.bindNativeSession(ctx, row); err != nil {
		return err
	}
	snapshot, err := h.client.Daemon.GetStatus(ctx, &pb.GetStatusRequest{})
	if err != nil {
		return err
	}
	if snapshot == nil || snapshot.GetDaemonId() != h.client.DaemonID() || snapshot.StorageError != nil {
		return fmt.Errorf("daemon ownership is unconfirmed")
	}
	aid, sid := stringValue(row, "agent_id"), stringValue(row, "id")
	nativeID := stringValue(row, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	for _, live := range snapshot.Sessions {
		if !snapshotMatchesOwnedSession(live, agent, aid, nativeID, stringValue(row, "connection_id")) {
			continue
		}
		state, err := nativeStatus(live.Status)
		if err != nil {
			return err
		}
		if err := h.requireAgentConnection(ctx, agent); err != nil {
			return err
		}
		updated, outcome, err := h.store.RefreshSessionStatus(ctx, aid, sid, store.RefreshSessionStatusExpectation{NativeID: nativeID, ConnectionID: stringValue(row, "connection_id"), ControlState: stringValue(row, "control_state"), NativeKind: stringValue(row, "native_kind"), Status: "error"}, state)
		if err != nil {
			return err
		}
		if outcome == store.RefreshSessionStatusApplied {
			h.publishEvent(ctx, "session.upsert", updated)
		}
		return nil
	}
	return fmt.Errorf("live session ownership is unconfirmed")
}
