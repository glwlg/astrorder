package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAdvanceSessionActivityOnlyMovesForwardAndKeepsStatus(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "app.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": "s1", "agent_id": "agent", "title": "t", "status": "running", "updated_at": "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	later := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	row, changed, err := st.AdvanceSessionActivity(ctx, "agent", "s1", later)
	if err != nil || !changed {
		t.Fatalf("advance = %v %v", changed, err)
	}
	if row["status"] != "running" {
		t.Fatalf("status was changed to %v", row["status"])
	}
	if got := row["updated_at"]; got != "2026-01-02T03:04:05.000Z" {
		t.Fatalf("updated_at = %v, want RFC3339 wire form", got)
	}

	// An older or equal native time must be a no-op.
	for _, stale := range []time.Time{later, later.Add(-time.Hour)} {
		if _, changed, err := st.AdvanceSessionActivity(ctx, "agent", "s1", stale); err != nil || changed {
			t.Fatalf("stale advance = %v %v", changed, err)
		}
	}
	// An unknown session is ignored, not created.
	if row, changed, err := st.AdvanceSessionActivity(ctx, "agent", "ghost", later); err != nil || changed || row != nil {
		t.Fatalf("unknown session = %v %v %v", row, changed, err)
	}
	if got, _ := st.GetSession(ctx, "agent", "ghost"); got != nil {
		t.Fatal("a session was created for an unknown id")
	}
}

func TestAdvanceSessionActivityIsIdempotentForSubMicrosecondTimes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "app.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": "s1", "agent_id": "agent", "title": "t", "status": "idle", "updated_at": "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	native := time.Unix(1790000000, 123456789).UTC() // nanoseconds, as a float Unix time yields
	if _, changed, err := st.AdvanceSessionActivity(ctx, "agent", "s1", native); err != nil || !changed {
		t.Fatalf("first = %v %v", changed, err)
	}
	if _, changed, err := st.AdvanceSessionActivity(ctx, "agent", "s1", native); err != nil || changed {
		t.Fatalf("second pass changed the session again: %v %v", changed, err)
	}
}
