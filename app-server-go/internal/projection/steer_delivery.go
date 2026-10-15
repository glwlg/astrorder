package projection

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"astrorder.dev/app-server/internal/store"
)

// Guidance completes input delivery while the original turn remains active.
func projectCodexCommandCompleteTx(ctx context.Context, tx *sql.Tx, streamID string, payload map[string]any) ([]store.Event, error) {
	aid, _ := payload["agent_id"].(string)
	sid, _ := payload["session_id"].(string)
	identity, err := resolveSessionIdentity(ctx, tx, streamID, aid, sid)
	if err != nil {
		return nil, err
	}
	commandID, _ := payload["command_id"].(string)
	if identity == nil || commandID == "" {
		return nil, fmt.Errorf("%w: guidance delivery identity missing", ErrCorruptedFrame)
	}
	aid, sid = identity.AgentID, identity.SessionID
	result, err := tx.ExecContext(ctx, `UPDATE commands SET state='completed',error=NULL,updated_at=datetime('now') WHERE agent_id=? AND session_id=? AND id=? AND state NOT IN ('completed','failed','cancelled')`, aid, sid, commandID)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return nil, err
	}
	rows, err := queryRows(ctx, tx, "SELECT * FROM commands WHERE agent_id=? AND session_id=? AND id=?", aid, sid, commandID)
	if err != nil {
		return nil, err
	}
	return []store.Event{{ID: fmt.Sprintf("cmd-%s-%d", commandID, time.Now().UnixNano()), Type: "command.upsert", AgentID: aid, SessionID: sid, Data: rows[0]}}, nil
}
