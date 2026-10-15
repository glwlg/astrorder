package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMessageProjectionDoesNotPersistTranscript(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "running", "updated_at": "2026-10-14T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	projector, err := st.RegisterProjector("transient", func(tx *sql.Tx) ([]store.Event, error) {
		return projectHermesStreamTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s", "command_id": "send", "delta": "private transcript"})
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := st.ApplyFrame(ctx, "epoch", "s", 1, projector)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("live message disappeared")
	}
	var count int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil || count != 0 {
		t.Fatalf("transcript persisted: %d %v", count, err)
	}
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM events WHERE data LIKE '%private transcript%'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("event payload persisted: %d %v", count, err)
	}
	checkpoint, err := st.GetCheckpoint(ctx, "epoch", "s")
	if err != nil || checkpoint != 1 {
		t.Fatalf("checkpoint lost: %d %v", checkpoint, err)
	}
	duplicate, err := st.ApplyFrame(ctx, "epoch", "s", 1, projector)
	if err != nil || len(duplicate) != 0 {
		t.Fatalf("duplicate frame replayed: %+v %v", duplicate, err)
	}
}
