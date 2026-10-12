package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// nativeRemovedKey marks a session the user removed from Astrorder.
func nativeRemovedKey(agentID, sessionID string) string {
	encoded, _ := json.Marshal([]string{agentID, sessionID})
	return "native_removed:" + string(encoded)
}

// InsertSessionIfAbsent records a session discovered in a native store. It never
// touches an existing row, so it cannot overwrite the title, status or kind of a
// session Astrorder created itself while the same thread was being discovered.
// A session the user removed from Astrorder is not added back. The second
// result reports whether a row was inserted.
func (s *Store) InsertSessionIfAbsent(ctx context.Context, payload map[string]any) (map[string]any, bool, error) {
	id, err := stringValue(payload, "id", true)
	if err != nil {
		return nil, false, err
	}
	agentID, err := stringValue(payload, "agent_id", true)
	if err != nil {
		return nil, false, err
	}
	status, err := stringValue(payload, "status", true)
	if err != nil {
		return nil, false, err
	}
	title, _ := stringValue(payload, "title", false)
	title = truncateCodepoints(title, 512)
	updatedAt, err := databaseTime(payload["updated_at"])
	if err != nil {
		return nil, false, fmt.Errorf("updated_at: %w", err)
	}
	sourceID := payloadStringDefault(payload, "source_id", agentID)
	sourceSessionID := payloadStringDefault(payload, "source_session_id", id)
	historyState := payloadStringDefault(payload, "history_state", "available")
	controlState := payloadStringDefault(payload, "control_state", "owned")
	inserted := false
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		var removed int
		if scanErr := tx.QueryRowContext(ctx, `SELECT count(*) FROM workspace_preferences WHERE "key"=?`, nativeRemovedKey(agentID, id)).Scan(&removed); scanErr != nil {
			return scanErr
		}
		if removed > 0 {
			return nil
		}
		result, execErr := tx.ExecContext(ctx, `INSERT INTO sessions
(id,agent_id,title,workspace,status,source_id,connection_id,source_session_id,project_id,project_name,
history_state,native_kind,ephemeral,control_state,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(agent_id,id) DO NOTHING`,
			id, agentID, title, nullablePayload(payload, "workspace"), status, sourceID,
			nullablePayload(payload, "connection_id"), sourceSessionID, nullablePayload(payload, "project_id"),
			nullablePayload(payload, "project_name"), historyState, nullablePayload(payload, "native_kind"),
			false, controlState, updatedAt)
		if execErr != nil {
			return execErr
		}
		affected, execErr := result.RowsAffected()
		inserted = affected > 0
		return execErr
	})
	if err != nil || !inserted {
		return nil, false, err
	}
	row, err := s.GetSession(ctx, agentID, id)
	return row, row != nil, err
}
