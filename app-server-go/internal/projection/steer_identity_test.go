package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestCodexGuidanceEchoUsesConfirmedCommandIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "running", "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, command := range []struct{ id, state string }{{"original", "running"}, {"guidance", "completed"}} {
		if _, _, err := st.CreateCommand(ctx, map[string]any{"id": command.id, "agent_id": "a", "session_id": "s", "action": "send", "text": command.id}, command.state); err != nil {
			t.Fatal(err)
		}
	}
	var events []store.Event
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		events, err = projectCodexNotificationTx(ctx, tx, "s", "a", map[string]any{"agent_id": "a", "session_id": "s", "command_id": "guidance", "frame": map[string]any{"method": "item/started", "params": map[string]any{"threadId": "s", "turnId": "turn", "item": map[string]any{"id": "guide-echo", "type": "userMessage", "text": "follow-up"}}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "message.upsert" && event.Data["id"] == "guide-echo" && event.Data["command_id"] == "guidance" {
			found = true
		}
	}
	if !found {
		t.Fatalf("guidance misbound to original command: %+v", events)
	}
}
