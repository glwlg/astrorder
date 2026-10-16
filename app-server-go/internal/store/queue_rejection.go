package store

import "context"

// ResolveRejectedQueueCommand compares the persisted evidence atomically so a
// concurrent completion/cancellation can never be overwritten by reconciliation.
func (s *Store) ResolveRejectedQueueCommand(ctx context.Context, aid, sid, cid, evidence, detail string) (map[string]any, bool, error) {
	mutation, err := s.db.ExecContext(ctx, `UPDATE commands SET state='failed',error=?,updated_at=? WHERE agent_id=? AND session_id=? AND id=? AND action='enqueue' AND state='unknown' AND error=? AND EXISTS(SELECT 1 FROM agents WHERE agents.id=commands.agent_id AND agents.kind='hermes')`, detail, nowDatabaseTime(), aid, sid, cid, evidence)
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
