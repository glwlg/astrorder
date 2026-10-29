package store

import (
	"context"
	"database/sql"
	"errors"
)

// UpdateQueuedCommandText atomically edits only an undispatched exact command.
func (s *Store) UpdateQueuedCommandText(ctx context.Context, aid, sid, cid, text string) (map[string]any, error) {
	var result map[string]any
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := queryRows(ctx, tx, `SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?`, aid, sid, cid)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errNotFound
		}
		row := rows[0]
		if row["state"] != "queued" {
			return errors.New("Command is no longer queued")
		}
		attachments, _ := row["attachments"].([]any)
		ids := make([]any, 0, len(attachments))
		for _, attachment := range attachments {
			value, ok := attachment.(map[string]any)
			if !ok {
				return errors.New("Invalid command attachments")
			}
			ids = append(ids, value["id"])
		}
		hash, err := payloadHash(map[string]any{"id": cid, "agent_id": aid, "session_id": sid, "action": row["action"], "text": text, "attachment_ids": ids, "target_id": row["target_id"]})
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE commands SET text=?,payload_hash=?,updated_at=? WHERE agent_id=? AND session_id=? AND id=? AND state='queued'`, text, hash, nowDatabaseTime(), aid, sid, cid); err != nil {
			return err
		}
		rows, err = queryRows(ctx, tx, `SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?`, aid, sid, cid)
		if err != nil {
			return err
		}
		result, err = commandWire(rows[0])
		return err
	})
	return result, err
}

// ClaimQueuedCommand elects a single dispatcher without resubmitting a received command.
func (s *Store) ClaimQueuedCommand(ctx context.Context, aid, sid, cid string) (map[string]any, bool, error) {
	var result map[string]any
	claimed := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		mutation, err := tx.ExecContext(ctx, `UPDATE commands SET state='received',updated_at=? WHERE agent_id=? AND session_id=? AND id=? AND state='queued'`, nowDatabaseTime(), aid, sid, cid)
		if err != nil {
			return err
		}
		affected, err := mutation.RowsAffected()
		if err != nil {
			return err
		}
		claimed = affected == 1
		rows, err := queryRows(ctx, tx, `SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?`, aid, sid, cid)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errNotFound
		}
		result, err = commandWire(rows[0])
		return err
	})
	return result, claimed, err
}

// CancelQueuedCommand removes a queue record without cancelling native work.
func (s *Store) CancelQueuedCommand(ctx context.Context, aid, sid, cid string) (map[string]any, bool, error) {
	var result map[string]any
	cancelled := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		mutation, err := tx.ExecContext(ctx, `UPDATE commands SET state='cancelled',updated_at=? WHERE agent_id=? AND session_id=? AND id=? AND (state='queued' OR (action='enqueue' AND state IN ('failed','unknown')))`, nowDatabaseTime(), aid, sid, cid)
		if err != nil {
			return err
		}
		affected, err := mutation.RowsAffected()
		if err != nil {
			return err
		}
		cancelled = affected == 1
		rows, err := queryRows(ctx, tx, `SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?`, aid, sid, cid)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errNotFound
		}
		result, err = commandWire(rows[0])
		return err
	})
	return result, cancelled, err
}
