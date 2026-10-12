package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestNativeHermesToolsUpdateOneActivity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "fixture", "status": "running", "updated_at": "2026-10-12T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"tool.start", "tool.complete"} {
		err = st.WithTx(ctx, func(tx *sql.Tx) error {
			events, err := projectHermesStreamTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s", "command_id": "c", "frame": map[string]any{"type": phase, "payload": map[string]any{"tool_id": "t", "name": "terminal", "args": map[string]any{"command": "test"}, "result_text": "done"}}})
			if err == nil && (len(events) == 0 || events[0].Type != "message.upsert") {
				t.Fatal("tool activity not projected")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, _, err := st.ListMessages(ctx, "a", "s", "", 50)
	if err != nil || len(rows) != 1 || rows[0]["kind"] != "tool" || rows[0]["text"] != "done" {
		t.Fatalf("tool lifecycle lost: %+v %v", rows, err)
	}
}
