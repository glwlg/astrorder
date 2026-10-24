package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestDeletedAntigravityCatalogSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"agent_id": "agy", "id": "11111111-1111-4111-8111-111111111111", "title": "fixture", "status": "idle", "updated_at": "2026-10-20T00:00:00Z", "control_state": "unbound"}
	if _, err := st.UpsertSession(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if deleted, err := st.DeleteSession(ctx, "agy", payload["id"].(string)); err != nil || !deleted {
		t.Fatalf("delete=%v err=%v", deleted, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, aid := range []string{"agy", "other-agy"} {
		payload["agent_id"] = aid
		var events []store.Event
		err := st.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			events, err = projectConnectorEventTx(ctx, tx, "probe-antigravity-local", aid, map[string]any{"agent_id": aid, "type": "session.upsert", "data": payload})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		row, err := st.GetSession(ctx, aid, payload["id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if aid == "agy" {
			if row != nil || len(events) != 0 {
				t.Errorf("deleted catalog resurrected: row=%v events=%d", row, len(events))
			}
		} else if row == nil || len(events) != 1 {
			t.Errorf("unrelated agent catalog suppressed: row=%v events=%d", row, len(events))
		}
	}
}
