package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openStatusRefreshStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func seedStatusRefreshSession(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "a", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id": "s", "agent_id": "a", "title": "keep", "workspace": "C:/keep", "status": "running",
		"control_state": "owned", "updated_at": "2026-10-09T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshSessionStatusWritesOnlyGuardedStatus(t *testing.T) {
	ctx := context.Background()
	st := openStatusRefreshStore(t)
	defer st.Close()
	seedStatusRefreshSession(t, st)

	if _, _, err := st.RefreshSessionStatus(ctx, "a", "s", RefreshSessionStatusExpectation{}, "idle"); err == nil {
		t.Fatal("missing native expectation was accepted")
	}

	row, outcome, err := st.RefreshSessionStatus(ctx, "a", "s", RefreshSessionStatusExpectation{NativeID: "s", ControlState: "owned"}, "idle")
	if err != nil || outcome != RefreshSessionStatusApplied {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	if row["status"] != "idle" || row["title"] != "keep" || row["workspace"] != "C:/keep" {
		t.Fatalf("row = %+v", row)
	}
	stored, err := st.GetSession(ctx, "a", "s")
	if err != nil || stored["status"] != "idle" || stored["title"] != "keep" || stored["workspace"] != "C:/keep" {
		t.Fatalf("stored = %+v err=%v", stored, err)
	}
	if stored["updated_at"] == "2026-10-09T00:00:00.000Z" {
		t.Fatalf("applied refresh did not advance updated_at: %+v", stored)
	}

	// Re-applying the same status must stay a read-only no-op.
	updatedAt := stored["updated_at"]
	row, outcome, err = st.RefreshSessionStatus(ctx, "a", "s", RefreshSessionStatusExpectation{NativeID: "s", ControlState: "owned"}, "idle")
	if err != nil || outcome != RefreshSessionStatusCurrent || row["status"] != "idle" {
		t.Fatalf("current outcome=%v row=%+v err=%v", outcome, row, err)
	}
	stored, _ = st.GetSession(ctx, "a", "s")
	if stored["updated_at"] != updatedAt {
		t.Fatalf("no-op refresh advanced updated_at: %+v", stored)
	}
}

func TestRefreshSessionStatusNeverResurrectsDeletedSession(t *testing.T) {
	ctx := context.Background()
	st := openStatusRefreshStore(t)
	defer st.Close()
	seedStatusRefreshSession(t, st)
	if deleted, err := st.DeleteSession(ctx, "a", "s"); err != nil || !deleted {
		t.Fatalf("delete: deleted=%v err=%v", deleted, err)
	}
	row, outcome, err := st.RefreshSessionStatus(ctx, "a", "s", RefreshSessionStatusExpectation{NativeID: "s", ControlState: "owned"}, "idle")
	if err != nil || outcome != RefreshSessionStatusGone || row != nil {
		t.Fatalf("outcome=%v row=%+v err=%v", outcome, row, err)
	}
	if stored, err := st.GetSession(ctx, "a", "s"); err != nil || stored != nil {
		t.Fatalf("deleted session resurrected: %+v err=%v", stored, err)
	}
}

func TestRefreshSessionStatusRejectsReboundBinding(t *testing.T) {
	ctx := context.Background()
	mutations := []struct {
		name     string
		update   string
		expected RefreshSessionStatusExpectation
	}{
		{"native id rebound", "UPDATE sessions SET source_session_id='native-other' WHERE agent_id='a' AND id='s'", RefreshSessionStatusExpectation{NativeID: "s", ControlState: "owned"}},
		{"connection rebound", "UPDATE sessions SET connection_id='saved' WHERE agent_id='a' AND id='s'", RefreshSessionStatusExpectation{NativeID: "s", ControlState: "owned"}},
		{"control state rebound", "UPDATE sessions SET control_state='readonly' WHERE agent_id='a' AND id='s'", RefreshSessionStatusExpectation{NativeID: "s", ControlState: "owned"}},
		{"native kind rebound", "UPDATE sessions SET native_kind='codex' WHERE agent_id='a' AND id='s'", RefreshSessionStatusExpectation{NativeID: "s", ControlState: "owned"}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			st := openStatusRefreshStore(t)
			defer st.Close()
			seedStatusRefreshSession(t, st)
			if _, err := st.DB().ExecContext(ctx, tc.update); err != nil {
				t.Fatal(err)
			}
			row, outcome, err := st.RefreshSessionStatus(ctx, "a", "s", tc.expected, "idle")
			if err != nil || outcome != RefreshSessionStatusRebound || row != nil {
				t.Fatalf("outcome=%v row=%+v err=%v", outcome, row, err)
			}
			stored, err := st.GetSession(ctx, "a", "s")
			if err != nil || stored["status"] != "running" {
				t.Fatalf("rebound row mutated: %+v err=%v", stored, err)
			}
		})
	}
}

func TestRefreshSessionStatusFoldsLocalConnectionAlias(t *testing.T) {
	ctx := context.Background()
	st := openStatusRefreshStore(t)
	defer st.Close()
	seedStatusRefreshSession(t, st)
	row, outcome, err := st.RefreshSessionStatus(ctx, "a", "s", RefreshSessionStatusExpectation{NativeID: "s", ConnectionID: "local", ControlState: "owned"}, "idle")
	if err != nil || outcome != RefreshSessionStatusApplied || row["status"] != "idle" {
		t.Fatalf("outcome=%v row=%+v err=%v", outcome, row, err)
	}
}
