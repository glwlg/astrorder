package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestCodexQueueCompletionUsesExactTurnAndGuidanceIdentity(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "app.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for _, sid := range []string{"s", "other"} {
		if _, err := st.UpsertSession(ctx, map[string]any{"id": sid, "agent_id": "a", "status": "running", "control_state": "owned", "updated_at": "2026-10-28T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range []struct{ id, sid, state string }{
		{"original", "s", "received"},
		{"guidance", "s", "unknown"},
		{"unrelated", "s", "unknown"},
		{"waiting", "s", "queued"},
		{"original", "other", "received"},
	} {
		if _, _, err := st.CreateCommand(ctx, map[string]any{"id": command.id, "agent_id": "a", "session_id": command.sid, "action": "enqueue", "text": "same text"}, command.state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.SetCommandState(ctx, "a", "s", "guidance", "unknown", "delivery unconfirmed", false); err != nil {
		t.Fatal(err)
	}
	notify := func(method, turnID, commandID string, item map[string]any) []store.Event {
		t.Helper()
		params := map[string]any{"threadId": "s", "turn": map[string]any{"id": turnID, "status": "completed"}, "turnId": turnID}
		if item != nil {
			params["item"] = item
		}
		payload := map[string]any{"agent_id": "a", "session_id": "s", "frame": map[string]any{"method": method, "params": params}}
		if commandID != "" {
			payload["command_id"] = commandID
		}
		var events []store.Event
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			events, err = projectCodexNotificationTx(ctx, tx, "s", "a", payload)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return events
	}
	assertState := func(sid, id, want string) {
		t.Helper()
		row, err := st.GetCommand(ctx, "a", sid, id)
		if err != nil || row["state"] != want {
			t.Fatalf("%s/%s state = %v, want %s (err %v)", sid, id, row["state"], want, err)
		}
	}

	notify("turn/started", "turn-1", "original", nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	notify("turn/completed", "older-turn", "", nil)
	assertState("s", "original", "running")
	session, err := st.GetSession(ctx, "a", "s")
	if err != nil || session["status"] != "running" {
		t.Fatalf("old turn changed current session: %v %v", session, err)
	}

	notify("item/completed", "turn-1", "original", map[string]any{"id": "original-item", "type": "userMessage", "clientId": "original"})
	assertState("s", "original", "running")
	notify("item/completed", "turn-1", "original", map[string]any{"id": "unmatched-item", "type": "userMessage", "text": "same text"})
	assertState("s", "unrelated", "unknown")
	notify("item/completed", "turn-1", "guidance", map[string]any{"id": "guidance-item", "type": "userMessage", "clientId": "guidance"})
	assertState("s", "guidance", "completed")
	guidance, err := st.GetCommand(ctx, "a", "s", "guidance")
	if err != nil || guidance["error"] != nil {
		t.Fatalf("confirmed guidance retained an error: %+v %v", guidance, err)
	}
	assertState("s", "original", "running")
	assertState("s", "unrelated", "unknown")

	events := notify("turn/completed", "turn-1", "", nil)
	assertState("s", "original", "completed")
	assertState("other", "original", "received")
	found := false
	for _, event := range events {
		if event.Type == "command.upsert" && event.Data["id"] == "original" && event.Data["state"] == "completed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing completed command event: %+v", events)
	}

	var ownershipEvents []store.Event
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		ownershipEvents, err = projectOwnershipLostTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s"}, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertState("s", "original", "completed")
	assertState("s", "waiting", "unknown")
	found = false
	for _, event := range ownershipEvents {
		if event.Type == "command.upsert" && event.Data["id"] == "waiting" && event.Data["state"] == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing ownership-loss command update: %+v", ownershipEvents)
	}
}
