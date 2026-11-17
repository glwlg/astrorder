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

// ClaimAutomaticQueuedCommand rechecks FIFO order and conflicting commands at
// the same durable boundary as the claim. Manual sends use a different path.
func (s *Store) ClaimAutomaticQueuedCommand(ctx context.Context, aid, sid, cid, sourceSessionID, connectionID, agentKind string) (map[string]any, bool, error) {
	var result map[string]any
	claimed := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		mutation, err := tx.ExecContext(ctx, `UPDATE commands SET state='received',error=NULL,updated_at=?
WHERE agent_id=? AND session_id=? AND id=? AND action='enqueue' AND state='queued'
AND NOT EXISTS (SELECT 1 FROM commands prior WHERE prior.agent_id=commands.agent_id AND prior.session_id=commands.session_id AND prior.action='enqueue' AND prior.state='queued' AND prior.row_id<commands.row_id)
AND NOT EXISTS (SELECT 1 FROM commands busy WHERE busy.agent_id=commands.agent_id AND busy.session_id=commands.session_id AND (busy.state IN ('received','running','unknown') OR (busy.state='accepted' AND busy.action IN ('send','enqueue'))))
AND EXISTS (SELECT 1 FROM sessions WHERE sessions.agent_id=commands.agent_id AND sessions.id=commands.session_id AND sessions.control_state='owned' AND coalesce(nullif(sessions.source_session_id,''),sessions.id)=? AND coalesce(sessions.connection_id,'')=?)
AND EXISTS (SELECT 1 FROM agents WHERE agents.id=commands.agent_id AND agents.status='ready' AND agents.kind=?)
AND NOT EXISTS (SELECT 1 FROM workspace_preferences p WHERE instr(p."key",'approval:')=1 AND json_extract(p.value,'$.agent_id')=commands.agent_id AND json_extract(p.value,'$.session_id')=commands.session_id AND json_extract(p.value,'$.state')='pending')`,
			nowDatabaseTime(), aid, sid, cid, sourceSessionID, connectionID, agentKind)
		if err != nil {
			return err
		}
		n, err := mutation.RowsAffected()
		if err != nil {
			return err
		}
		claimed = n == 1
		if !claimed {
			return nil
		}
		rows, err := queryRows(ctx, tx, `SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?`, aid, sid, cid)
		if err != nil {
			return err
		}
		result, err = commandWire(rows[0])
		return err
	})
	return result, claimed, err
}

// SetQueuedNotice never changes a command that has crossed the dispatch boundary.
func (s *Store) SetQueuedNotice(ctx context.Context, aid, sid, cid, notice string) (map[string]any, bool, error) {
	var value any
	if notice != "" {
		value = notice
	}
	mutation, err := s.db.ExecContext(ctx, `UPDATE commands SET error=?,updated_at=? WHERE agent_id=? AND session_id=? AND id=? AND state='queued' AND coalesce(error,'')<>?`, value, nowDatabaseTime(), aid, sid, cid, notice)
	if err != nil {
		return nil, false, err
	}
	n, err := mutation.RowsAffected()
	if err != nil || n == 0 {
		return nil, false, err
	}
	row, err := s.GetCommand(ctx, aid, sid, cid)
	return row, true, err
}

// ResolveUnknownCommand only applies authoritative evidence to the exact
// still-unknown command, leaving newer projection events untouched.
func (s *Store) ResolveUnknownCommand(ctx context.Context, aid, sid, cid, state string) (map[string]any, bool, error) {
	if state != "completed" && state != "accepted" {
		return nil, false, errors.New("invalid recovered command state")
	}
	mutation, err := s.db.ExecContext(ctx, `UPDATE commands SET state=?,error=NULL,updated_at=? WHERE agent_id=? AND session_id=? AND id=? AND state='unknown'`, state, nowDatabaseTime(), aid, sid, cid)
	if err != nil {
		return nil, false, err
	}
	n, err := mutation.RowsAffected()
	if err != nil || n == 0 {
		return nil, false, err
	}
	row, err := s.GetCommand(ctx, aid, sid, cid)
	return row, true, err
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
