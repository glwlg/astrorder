package projection

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestNativeHermesCommandDeltasCreateLiveAssistantMessage(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "fixture", "status": "running", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, delta := range []string{"native ", "response"} {
		var events []store.Event
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			events, err = projectHermesStreamTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s", "command_id": "exact", "delta": delta})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, event := range events {
			if event.Type == "message.delta" && event.Data["command_id"] == "exact" {
				found = true
				text += event.Data["text"].(string)
			}
		}
		if !found {
			t.Fatal("delta did not reach product message events")
		}
	}
	messages, err := st.QueryRows(ctx, "SELECT * FROM messages WHERE agent_id='a' AND session_id='s'")
	if err != nil || len(messages) != 0 || text != "native response" {
		t.Fatalf("live reply lost: %+v %v", messages, err)
	}
	var completed []store.Event
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		completed, err = projectHermesCommandCompleteTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s", "command_id": "exact", "text": "native response", "persisted_turn": map[string]any{"final_assistant_row_id": json.Number("91")}}, pb.SessionStatus_SESSION_STATUS_IDLE)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	messages, err = st.QueryRows(ctx, "SELECT * FROM messages WHERE agent_id='a' AND session_id='s'")
	if err != nil || len(messages) != 0 {
		t.Fatalf("final reply did not replace live message: %+v %v", messages, err)
	}
	found := false
	for _, event := range completed {
		if event.Type == "message.upsert" && event.Data["text"] == text && event.Data["id"] != "hermes-stream-exact" {
			found = true
		}
	}
	if !found {
		t.Fatal("native final message did not reach live clients")
	}
}
