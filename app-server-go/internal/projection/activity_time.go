package projection

import (
	"context"
	"database/sql"
	"time"

	"astrorder.dev/app-server/internal/store"
)

type activityTimeKey struct{}

// updateSessionStatusTx keeps status projection separate from the clock used
// for activity. WAL replay uses the original envelope time, never replay time.
func updateSessionStatusTx(ctx context.Context, tx *sql.Tx, agentID, sessionID, status string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET status=? WHERE agent_id=? AND id=?", status, agentID, sessionID); err != nil {
		return err
	}
	at, bound := ctx.Value(activityTimeKey{}).(time.Time)
	if !bound {
		at = time.Now().UTC()
	}
	if at.IsZero() {
		return nil
	}
	var current any
	if err := tx.QueryRowContext(ctx, "SELECT updated_at FROM sessions WHERE agent_id=? AND id=?", agentID, sessionID).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	previous, err := store.ParseTime(current)
	if err == nil && !at.After(previous) {
		return nil
	}
	_, err = tx.ExecContext(ctx, "UPDATE sessions SET updated_at=? WHERE agent_id=? AND id=?", at.UTC().Format("2006-01-02 15:04:05.000000"), agentID, sessionID)
	return err
}
