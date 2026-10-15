package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DuplicateCommandError indicates an ID reused with a different canonical payload.
type DuplicateCommandError struct{}

func (DuplicateCommandError) Error() string { return "command already exists with a different payload" }

// GetCommand returns an exact command composite identity.
func (s *Store) GetCommand(ctx context.Context, agentID, sessionID, commandID string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", agentID, sessionID, commandID)
	if err != nil {
		return nil, err
	}
	return commandWire(row)
}

// ListCommands returns commands in insertion order for one scope.
func (s *Store) ListCommands(ctx context.Context, agentID, sessionID string) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? ORDER BY row_id", agentID, sessionID)
	if err != nil {
		return nil, err
	}
	return wireRows(rows, commandWire)
}

// CreateCommand creates an idempotent command from payload and initial state.
func (s *Store) CreateCommand(ctx context.Context, payload map[string]any, initialState string) (map[string]any, bool, error) {
	id, err := stringValue(payload, "id", true)
	if err != nil {
		return nil, false, err
	}
	agentID, err := stringValue(payload, "agent_id", true)
	if err != nil {
		return nil, false, err
	}
	sessionID, err := stringValue(payload, "session_id", true)
	if err != nil {
		return nil, false, err
	}
	action, err := stringValue(payload, "action", true)
	if err != nil {
		return nil, false, err
	}
	text, _ := stringValue(payload, "text", false)
	attachments := payload["attachments"]
	if attachments == nil {
		attachments = []any{}
	}
	attachmentsList, ok := attachments.([]any)
	if !ok {
		return nil, false, errors.New("attachments must be an array")
	}
	attachmentIDs := make([]any, 0, len(attachmentsList))
	for _, item := range attachmentsList {
		object, ok := item.(map[string]any)
		if !ok || object["id"] == nil {
			return nil, false, errors.New("attachment IDs are required")
		}
		attachmentIDs = append(attachmentIDs, object["id"])
	}
	canonical := map[string]any{
		"id": id, "agent_id": agentID, "session_id": sessionID, "action": action,
		"text": text, "attachment_ids": attachmentIDs, "target_id": nullablePayload(payload, "target_id"),
	}
	hash, err := payloadHash(canonical)
	if err != nil {
		return nil, false, err
	}
	attachmentsJSON, err := encodeJSON(attachmentsList)
	if err != nil {
		return nil, false, err
	}
	now := nowDatabaseTime()
	result, err := s.db.ExecContext(ctx, `INSERT INTO commands
(id,session_id,agent_id,action,state,text,attachments,target_id,created_at,updated_at,error,payload_hash)
VALUES(?,?,?,?,?,?,?,?,?,?,NULL,?) ON CONFLICT(agent_id,session_id,id) DO NOTHING`,
		id, sessionID, agentID, action, initialState, text, attachmentsJSON, nullablePayload(payload, "target_id"), now, now, hash)
	if err != nil {
		return nil, false, err
	}
	affected, _ := result.RowsAffected()
	row, err := s.QueryRow(ctx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", agentID, sessionID, id)
	if err != nil {
		return nil, false, err
	}
	if affected == 0 && row["payload_hash"] != hash {
		return nil, false, DuplicateCommandError{}
	}
	wire, err := commandWire(row)
	return wire, affected > 0, err
}

// SetCommandState updates one exact command and optionally preserves terminal progress.
func (s *Store) SetCommandState(ctx context.Context, agentID, sessionID, commandID, state string, errorText any, preserveProgress bool) (map[string]any, error) {
	query := "UPDATE commands SET state=?,error=?,updated_at=? WHERE agent_id=? AND session_id=? AND id=?"
	args := []any{state, errorText, nowDatabaseTime(), agentID, sessionID, commandID}
	if preserveProgress {
		blocked := []string{"completed", "failed", "cancelled"}
		if state == "accepted" {
			blocked = append(blocked, "running")
		}
		query += " AND state NOT IN (" + placeholders(len(blocked)) + ")"
		for _, value := range blocked {
			args = append(args, value)
		}
	}
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return nil, err
	}
	row, err := s.GetCommand(ctx, agentID, sessionID, commandID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errNotFound
	}
	return row, nil
}

// PendingCommands returns queued commands for an agent in insertion order.
func (s *Store) PendingCommands(ctx context.Context, agentID string) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, "SELECT * FROM commands WHERE agent_id=? AND state='queued' ORDER BY row_id", agentID)
	if err != nil {
		return nil, err
	}
	return wireRows(rows, commandWire)
}

// ActiveCommands returns commands whose dispatch result remains non-terminal.
func (s *Store) ActiveCommands(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, "SELECT * FROM commands WHERE state IN ('received','queued','accepted','running') ORDER BY row_id")
	if err != nil {
		return nil, err
	}
	return wireRows(rows, commandWire)
}

// QueueCommands includes unresolved and failed queue receipts for cross-device recovery.
func (s *Store) QueueCommands(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, "SELECT * FROM commands WHERE state IN ('received','queued','accepted','running') OR (action='enqueue' AND state IN ('failed','unknown')) ORDER BY row_id")
	if err != nil {
		return nil, err
	}
	return wireRows(rows, commandWire)
}

// GetTask returns an exact task composite identity.
func (s *Store) GetTask(ctx context.Context, agentID, sessionID, taskID string) (map[string]any, error) {
	row, err := s.QueryRow(ctx, "SELECT * FROM tasks WHERE agent_id=? AND session_id=? AND id=?", agentID, sessionID, taskID)
	if err != nil {
		return nil, err
	}
	return taskWire(row)
}

// ListTasks returns tasks newest-first within one exact scope.
func (s *Store) ListTasks(ctx context.Context, agentID, sessionID string) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, "SELECT * FROM tasks WHERE agent_id=? AND session_id=? ORDER BY updated_at DESC,row_id DESC", agentID, sessionID)
	if err != nil {
		return nil, err
	}
	return wireRows(rows, taskWire)
}

// UpsertTask writes an exact task projection.
func (s *Store) UpsertTask(ctx context.Context, payload map[string]any) (map[string]any, error) {
	var result map[string]any
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		row, err := upsertTaskTx(ctx, tx, payload)
		result = row
		return err
	})
	return result, err
}

func upsertTaskTx(ctx context.Context, tx *sql.Tx, payload map[string]any) (map[string]any, error) {
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
	kind, err := stringValue(payload, "kind", true)
	if err != nil {
		return nil, err
	}
	status, err := stringValue(payload, "status", true)
	if err != nil {
		return nil, err
	}
	createdAt, err := databaseTime(payload["created_at"])
	if err != nil {
		return nil, err
	}
	updatedAt, err := databaseTime(payload["updated_at"])
	if err != nil {
		return nil, err
	}
	logs := payload["logs"]
	if logs == nil {
		logs = []any{}
	}
	logsJSON, err := encodeJSON(logs)
	if err != nil {
		return nil, err
	}
	progressJSON, err := nullableJSON(payload, "progress")
	if err != nil {
		return nil, err
	}
	title := truncateCodepoints(fmt.Sprint(payload["title"]), 512)
	_, err = tx.ExecContext(ctx, `INSERT INTO tasks
(id,session_id,agent_id,kind,title,status,progress,command,logs,target_id,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(agent_id,session_id,id) DO UPDATE SET kind=excluded.kind,title=excluded.title,
status=excluded.status,progress=excluded.progress,command=excluded.command,logs=excluded.logs,target_id=excluded.target_id,
created_at=excluded.created_at,updated_at=excluded.updated_at`,
		id, sessionID, agentID, kind, title, status, progressJSON,
		nullablePayload(payload, "command"), logsJSON, nullablePayload(payload, "target_id"), createdAt, updatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := queryRows(ctx, tx, "SELECT * FROM tasks WHERE agent_id=? AND session_id=? AND id=?", agentID, sessionID, id)
	if err != nil {
		return nil, err
	}
	return taskWire(rows[0])
}

// ActiveTasks returns pending, running, or waiting-approval tasks.
func (s *Store) ActiveTasks(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.QueryRows(ctx, "SELECT * FROM tasks WHERE status IN ('pending','running','waiting_approval')")
	if err != nil {
		return nil, err
	}
	return wireRows(rows, taskWire)
}
