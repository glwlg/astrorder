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
	event := liveMessageEvent(aid, sid, id, "assistant", "message", text, commandID, false)
	event.Data["created_at"] = now
	return []store.Event{event}, nil
}
