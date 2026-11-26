package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestConnectorMessageAndCommandUseEnvelopeSession(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, _, err = st.CreateCommand(ctx, map[string]any{"id": "cmd", "agent_id": "a", "session_id": "conversation", "action": "send", "text": "hello", "attachments": []any{}}, "received"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"message.upsert", "command.upsert"} {
		t.Run(kind, func(t *testing.T) {
			data := map[string]any{"id": "message", "role": "assistant", "text": "hello"}
			if kind == "command.upsert" {
				data = map[string]any{"id": "cmd", "state": "completed"}
			}
			var events []store.Event
			err := st.WithTx(ctx, func(tx *sql.Tx) error {
				var e error
				events, e = projectConnectorEventTx(ctx, tx, "conversation", "a", map[string]any{"agent_id": "a", "session_id": "conversation", "type": kind, "data": data})
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].SessionID != "conversation" {
				t.Fatalf("event identity: %+v", events)
			}
			if kind == "command.upsert" {
				row, e := st.GetCommand(ctx, "a", "conversation", "cmd")
				if e != nil || row["state"] != "completed" {
					t.Fatalf("command: %+v %v", row, e)
				}
			}
		})
	}
}

func TestConnectorCommandUpsertMissingAppRowDoesNotPoisonProjection(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Delegated sends (e.g. sessions spawn / bots invoke) dispatch directly to
	// Session Daemon with an operation-derived ID like "<op>-send" and have no
	// row in App's commands table. Their terminal command.upsert receipt must
	// advance the daemon checkpoint without failing projection.
	var events []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		events, e = projectConnectorEventTx(ctx, tx, "78c0edab-session", "ssh-antigravity", map[string]any{
			"agent_id":   "ssh-antigravity",
			"session_id": "78c0edab-session",
			"type":       "command.upsert",
			"data": map[string]any{
				"id":    "op-delegation-01-send",
				"state": "failed",
				"error": "cancelled",
			},
		})
		return e
	})
	if err != nil {
		t.Fatalf("missing command row must not fail projection: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no emitted command.upsert event for unknown command row, got %+v", events)
	}
}
