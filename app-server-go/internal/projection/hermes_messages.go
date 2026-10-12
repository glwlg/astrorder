package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Native row addresses use the same projection key as the history endpoint.
func projectHermesFinalMessageTx(ctx context.Context, tx *sql.Tx, aid, sid string, payload map[string]any) ([]store.Event, error) {
	text, _ := payload["text"].(string)
	if text == "" {
		return nil, nil
	}
	commandID, _ := payload["command_id"].(string)
	receipt, _ := payload["persisted_turn"].(map[string]any)
	identity := receipt["final_assistant_row_id"]
	if identity == nil {
		return nil, nil
	}
	rows, err := queryRows(ctx, tx, "SELECT source_id,source_session_id FROM sessions WHERE agent_id=? AND id=?", aid, sid)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("Hermes final message session missing")
	}
	source, _ := rows[0]["source_id"].(string)
	if source == "" {
		source = aid
	}
	nativeID, _ := rows[0]["source_session_id"].(string)
	if nativeID == "" {
		nativeID = sid
	}
	sum := sha256.Sum256([]byte(source + "\x00" + nativeID + "\x00" + fmt.Sprint(identity)))
	id := "history-message-" + hex.EncodeToString(sum[:])[:48]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,agent_id,role,kind,text,attachments,created_at,command_id) VALUES(?,?,?,'assistant','message',?,'[]',?,?) ON CONFLICT(agent_id,session_id,id) DO UPDATE SET text=excluded.text,command_id=excluded.command_id`, id, sid, aid, text, now, commandID)
	if err != nil {
		return nil, err
	}
	if commandID != "" {
		if _, err = tx.ExecContext(ctx, "DELETE FROM messages WHERE agent_id=? AND session_id=? AND id=?", aid, sid, "hermes-stream-"+commandID); err != nil {
			return nil, err
		}
	}
	messages, err := queryRows(ctx, tx, "SELECT * FROM messages WHERE agent_id=? AND session_id=? AND id=?", aid, sid, id)
	if err != nil {
		return nil, err
	}
	return []store.Event{{ID: fmt.Sprintf("hermes-final-%s-%d", id, time.Now().UnixNano()), Type: "message.upsert", AgentID: aid, SessionID: sid, Data: messages[0]}}, nil
}
