package store

import (
	"context"
	"database/sql"
	"errors"
)

// Legacy transcript helpers exist only to seed and verify preserved database rows in tests.
// Production history and projection code cannot call these methods.
// GetMessage returns an exact (agent, session, message) identity.
func (s *Store) GetMessage(ctx context.Context, agentID, sessionID, messageID string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM messages WHERE agent_id=? AND session_id=? AND id=?", agentID, sessionID, messageID)
	if err != nil {
		return nil, err
	}
	return messageWire(row)
}

// GetMessages returns exact IDs within one agent/session scope.
func (s *Store) GetMessages(ctx context.Context, agentID, sessionID string, messageIDs []string) ([]map[string]any, error) {
	if len(messageIDs) == 0 {
		return []map[string]any{}, nil
	}
	query := "SELECT * FROM messages WHERE agent_id=? AND session_id=? AND id IN (" + placeholders(len(messageIDs)) + ")"
	args := []any{agentID, sessionID}
	for _, id := range messageIDs {
		args = append(args, id)
	}
	rows, err := s.QueryRows(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return wireRows(rows, messageWire)
}

// ListMessages returns an ascending page from the newest tail and an older-page cursor.
func (s *Store) ListMessages(ctx context.Context, agentID, sessionID, before string, limit int) ([]map[string]any, string, error) {
	if limit < 1 {
		return nil, "", errors.New("limit must be positive")
	}
	query := "SELECT * FROM messages WHERE agent_id=? AND session_id=?"
	args := []any{agentID, sessionID}
	if before != "" {
		rowID, err := decodeCursor(before)
		if err != nil {
			return nil, "", err
		}
		anchor, err := s.QueryRow(ctx, "SELECT created_at,row_id FROM messages WHERE agent_id=? AND session_id=? AND row_id=?", agentID, sessionID, rowID)
		if err != nil {
			return nil, "", err
		}
		if anchor == nil {
			return nil, "", errors.New("history cursor is outside this session")
		}
		anchorTime, err := databaseTime(anchor["created_at"])
		if err != nil {
			return nil, "", err
		}
		query += " AND (created_at < ? OR (created_at = ? AND row_id < ?))"
		args = append(args, anchorTime, anchorTime, rowID)
	}
	query += " ORDER BY created_at DESC,row_id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.QueryRows(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
		rows[left], rows[right] = rows[right], rows[left]
	}
	next := ""
	if hasMore && len(rows) > 0 {
		rowID, err := integer(rows[0]["row_id"], 0)
		if err != nil {
			return nil, "", err
		}
		next = encodeCursor(rowID)
	}
	converted, err := wireRows(rows, messageWire)
	return converted, next, err
}

// UpsertMessage uses exact message IDs and explicit command correlation only.
// It intentionally does not reproduce the legacy text/role/kind alias heuristic.
func (s *Store) UpsertMessage(ctx context.Context, payload map[string]any) (map[string]any, error) {
	var result map[string]any
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		row, err := upsertMessageTx(ctx, tx, payload)
		result = row
		return err
	})
	return result, err
}

// UpsertMessages applies a batch atomically in input order.
func (s *Store) UpsertMessages(ctx context.Context, payloads []map[string]any) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(payloads))
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, payload := range payloads {
			row, err := upsertMessageTx(ctx, tx, payload)
			if err != nil {
				return err
			}
			result = append(result, row)
		}
		return nil
	})
	return result, err
}

func upsertMessageTx(ctx context.Context, tx *sql.Tx, payload map[string]any) (map[string]any, error) {
	id, err := stringValue(payload, "id", true)
	if err != nil {
		return nil, err
	}
	agentID, err := stringValue(payload, "agent_id", true)
	if err != nil {
		return nil, err
	}
	sessionID, err := stringValue(payload, "session_id", true)
	if err != nil {
		return nil, err
	}
	role, err := stringValue(payload, "role", true)
	if err != nil {
		return nil, err
	}
	kind, err := stringValue(payload, "kind", true)
	if err != nil {
		return nil, err
	}
	createdAt, err := databaseTime(payload["created_at"])
	if err != nil {
		return nil, err
	}
	attachments := payload["attachments"]
	if attachments == nil {
		attachments = []any{}
	}
	attachmentsJSON, err := encodeJSON(attachments)
	if err != nil {
		return nil, err
	}
	toolJSON, err := nullableJSON(payload, "tool")
	if err != nil {
		return nil, err
	}
	text := ""
	if value, ok := payload["text"].(string); ok {
		text = value
	}
	commandID := nullablePayload(payload, "command_id")
	if role == "user" && commandID != nil {
		correlated, err := queryRows(ctx, tx, `SELECT * FROM messages WHERE agent_id=? AND session_id=? AND role='user' AND command_id=? ORDER BY created_at DESC,row_id DESC`, agentID, sessionID, commandID)
		if err != nil {
			return nil, err
		}
		if len(correlated) > 0 {
			canonical := correlated[0]
			canonicalTime, _ := parseTime(canonical["created_at"])
			incomingTime, _ := parseTime(createdAt)
			if !incomingTime.Before(canonicalTime) {
				if _, err := tx.ExecContext(ctx, `UPDATE messages SET role=?,kind=?,text=?,attachments=?,created_at=?,command_id=?,tool=? WHERE row_id=?`,
					role, kind, text, attachmentsJSON, createdAt, commandID, toolJSON, canonical["row_id"]); err != nil {
					return nil, err
				}
			}
			for _, duplicate := range correlated[1:] {
				if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE row_id=?", duplicate["row_id"]); err != nil {
					return nil, err
				}
			}
			rows, err := queryRows(ctx, tx, "SELECT * FROM messages WHERE row_id=?", canonical["row_id"])
			if err != nil {
				return nil, err
			}
			return messageWire(rows[0])
		}
	}
	existing, err := queryRows(ctx, tx, "SELECT * FROM messages WHERE agent_id=? AND session_id=? AND id=?", agentID, sessionID, id)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		if authoritative, _ := boolValue(payload["authoritative_created_at"], false); !authoritative {
			// Preserve the stored creation time, but never echo the driver's
			// raw scan back into the DATETIME column: the sqlite driver may
			// return a time.Time whose fmt.Sprint form is Go's default
			// "2006-01-02 15:04:05.999999999 -0700 MST" layout, which
			// contaminates the column and later fails presence parsing.
			// databaseTime normalizes any parseable stored value to the
			// canonical database layout; if the stored value is unreadable,
			// keep the incoming (already normalized) timestamp instead of
			// writing garbage back.
			if preserved, preserveErr := databaseTime(existing[0]["created_at"]); preserveErr == nil {
				createdAt = preserved
			}
		}
		if commandID == nil {
			commandID = existing[0]["command_id"]
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,agent_id,role,kind,text,attachments,created_at,command_id,tool)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(agent_id,session_id,id) DO UPDATE SET role=excluded.role,kind=excluded.kind,
text=excluded.text,attachments=excluded.attachments,created_at=excluded.created_at,command_id=excluded.command_id,tool=excluded.tool`,
		id, sessionID, agentID, role, kind, text, attachmentsJSON, createdAt, commandID, toolJSON)
	if err != nil {
		return nil, err
	}
	rows, err := queryRows(ctx, tx, "SELECT * FROM messages WHERE agent_id=? AND session_id=? AND id=?", agentID, sessionID, id)
	if err != nil {
		return nil, err
	}
	return messageWire(rows[0])
}
