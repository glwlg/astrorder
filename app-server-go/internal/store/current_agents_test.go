package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCurrentAgentsHidesOnlyUnusedDisconnectedOrphan(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, id := range []string{"orphan", "used", "live"} {
		status := "disconnected"
		if id == "live" {
			status = "ready"
		}
		if _, err := st.UpsertAgent(ctx, map[string]any{"id": id, "kind": "hermes", "name": id, "status": status, "connection_id": "removed"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "used", "title": "test", "status": "idle", "updated_at": "2026-10-12T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListCurrentAgents(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("unexpected catalog: %+v %v", rows, err)
	}
	for _, row := range rows {
		if row["id"] == "orphan" {
			t.Fatal("orphan visible")
		}
	}
	row, err := st.GetAgent(ctx, "orphan")
	if err != nil || row == nil {
		t.Fatal("diagnostic record deleted")
	}
}
