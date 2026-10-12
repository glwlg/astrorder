package store

import (
	"context"
	"database/sql"
	"time"
)

// AdvanceSessionActivity moves a session's updated_at forward to at. It never
// moves it backwards and never touches status, so a stale native timestamp is a
// no-op. It returns the wire row and true only when the value changed.
func (s *Store) AdvanceSessionActivity(ctx context.Context, agentID, sessionID string, at time.Time) (map[string]any, bool, error) {
	var changed bool
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var current any
		err := tx.QueryRowContext(ctx, "SELECT updated_at FROM sessions WHERE agent_id=? AND id=?", agentID, sessionID).Scan(&current)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		// The column stores microseconds; compare at that precision or a value
		// that was just written would look newer than itself on the next pass.
		at = at.Truncate(time.Microsecond)
		if previous, perr := parseTime(current); perr == nil && !at.After(previous) {
			return nil
		}
		stamp := at.UTC().Format("2006-01-02 15:04:05.000000")
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET updated_at=? WHERE agent_id=? AND id=?", stamp, agentID, sessionID); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil || !changed {
		return nil, false, err
	}
	row, err := s.GetSession(ctx, agentID, sessionID)
	return row, row != nil, err
}
