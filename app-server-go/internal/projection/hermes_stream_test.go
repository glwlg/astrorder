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
			if event.Type == "message.upsert" {
				found = true
			}
		}
		if !found {
			t.Fatal("delta did not reach product message events")
		}
	}
	messages, _, err := st.ListMessages(ctx, "a", "s", "", 50)
	if err != nil || len(messages) != 1 || messages[0]["text"] != "native response" || messages[0]["command_id"] != "exact" {
		t.Fatalf("live reply lost: %+v %v", messages, err)
	}
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := projectHermesCommandCompleteTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s", "command_id": "exact", "text": "native response", "persisted_turn": map[string]any{"final_assistant_row_id": json.Number("91")}}, pb.SessionStatus_SESSION_STATUS_IDLE)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	messages, _, err = st.ListMessages(ctx, "a", "s", "", 50)
	if err != nil || len(messages) != 1 || messages[0]["id"] == "hermes-stream-exact" {
		t.Fatalf("final reply did not replace live message: %+v %v", messages, err)
	}
}
