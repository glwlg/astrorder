package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRepairRemovedAntigravityCatalog(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, aid := range []string{"agy", "codex"} {
		if _, err := st.UpsertAgent(ctx, map[string]any{"id": aid, "kind": map[string]string{"agy": "antigravity", "codex": "codex"}[aid], "name": aid, "status": "ready"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		aid, id, status, control string
		removed                  bool
	}{
		{"agy", "removed", "idle", "unbound", true},
		{"agy", "live", "running", "owned", true},
		{"agy", "kept", "idle", "unbound", false},
		{"codex", "removed", "idle", "unbound", true},
	} {
		row := map[string]any{"agent_id": tc.aid, "id": tc.id, "status": tc.status, "control_state": tc.control, "title": "fixture", "updated_at": "2026-10-20T00:00:00Z"}
		if _, err := st.UpsertSession(ctx, row); err != nil {
			t.Fatal(err)
		}
		if tc.removed {
			if _, err := st.DeleteSession(ctx, tc.aid, tc.id); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpsertSession(ctx, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := st.RepairRemovedAntigravityCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListSessions(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("reappeared catalog retained or unrelated rows removed: %+v", rows)
	}
	row, err := st.GetSession(ctx, "agy", "removed")
	if err != nil || row != nil {
		t.Fatalf("removed row=%v err=%v", row, err)
	}
	if _, inserted, err := st.InsertSessionIfAbsent(ctx, map[string]any{"agent_id": "agy", "id": "removed", "status": "idle", "updated_at": "2026-10-20T00:00:00Z"}); err != nil || inserted {
		t.Fatalf("removal marker lost: inserted=%v err=%v", inserted, err)
	}
	if err := st.RepairRemovedAntigravityCatalog(ctx); err != nil {
		t.Fatal(err)
	}
}
