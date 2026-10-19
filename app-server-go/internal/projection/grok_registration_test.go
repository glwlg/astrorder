package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestLegacyGrokHelloRetainsEnvironmentInRowAndEvent(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": "saved", "display_name": "Debian", "host": "example.invalid", "user": "tester", "port": 22, "state": "connected"}); err != nil {
		t.Fatal(err)
	}
	for _, existing := range []bool{false, true} {
		if existing {
			if _, err := st.UpsertAgent(ctx, map[string]any{"id": "ssh-grok-saved", "kind": "grok", "name": "Debian Grok", "status": "ready", "connection_id": "saved"}); err != nil {
				t.Fatal(err)
			}
		}
		var events []store.Event
		err := st.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			events, err = projectConnectorHelloTx(ctx, tx, map[string]any{"id": "ssh-grok-saved", "kind": "grok", "name": "Grok Build", "status": "ready"})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		agent, err := st.GetAgent(ctx, "ssh-grok-saved")
		if err != nil {
			t.Fatal(err)
		}
		if agent["connection_id"] != "saved" {
			t.Fatalf("remote Grok classified as local: %+v", agent)
		}
		if len(events) != 1 || events[0].Data["connection_id"] != "saved" {
			t.Fatalf("live event lost connection: %+v", events)
		}
	}
}
