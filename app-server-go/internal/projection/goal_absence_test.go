package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestAbsentNativeGoalDoesNotCreateTaskOrRepeatCancellation(t *testing.T) {
	for _, source := range []string{"codex", "hermes"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "idle", "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			apply := func(goal map[string]any, wantEvents int) {
				t.Helper()
				if err := st.WithTx(ctx, func(tx *sql.Tx) error {
					events, err := projectNativeGoalTx(ctx, tx, "s", "a", source, goal)
					if err != nil {
						return err
					}
					if len(events) != wantEvents {
						t.Errorf("goal=%v: got %d events, want %d: %+v", goal, len(events), wantEvents, events)
					}
					if goal == nil && wantEvents == 1 && (len(events) != 1 || events[0].Data["status"] != "cancelled") {
						t.Errorf("missing cancellation for existing goal: %+v", events)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			apply(nil, 0)
			apply(nil, 0)
			rows, err := st.ListTasks(ctx, "a", "s")
			if err != nil || len(rows) != 0 {
				t.Errorf("empty goal created a task: %+v %v", rows, err)
			}
			goal := map[string]any{"status": "active", "objective": "Build", "title": "Build"}
			apply(goal, 1)
			apply(nil, 1)
			apply(nil, 0)
			rows, err = st.ListTasks(ctx, "a", "s")
			if err != nil || len(rows) != 1 || rows[0]["status"] != "cancelled" {
				t.Fatalf("existing goal was not cleared: %+v %v", rows, err)
			}
			apply(goal, 1)
			rows, err = st.ListTasks(ctx, "a", "s")
			if err != nil || len(rows) != 1 || rows[0]["status"] != "running" {
				t.Fatalf("agent goal did not replace tombstone: %+v %v", rows, err)
			}
		})
	}
}

func TestAbsentNativeGoalDoesNotCancelAnotherScope(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, scope := range [][2]string{{"a", "s"}, {"b", "s"}, {"a", "other"}} {
		if _, err := st.UpsertSession(ctx, map[string]any{"agent_id": scope[0], "id": scope[1], "status": "idle", "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		for _, scope := range [][2]string{{"b", "s"}, {"a", "other"}} {
			if _, err := projectNativeGoalTx(ctx, tx, scope[1], scope[0], "codex", map[string]any{"objective": "Keep", "status": "active"}); err != nil {
				return err
			}
		}
		if _, err := projectNativeGoalTx(ctx, tx, "s", "a", "hermes", map[string]any{"title": "Keep", "status": "active"}); err != nil {
			return err
		}
		events, err := projectNativeGoalTx(ctx, tx, "s", "a", "codex", nil)
		if len(events) != 0 {
			t.Errorf("empty goal crossed source or identity scope: %+v", events)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][2]string{{"a", "s"}, {"b", "s"}, {"a", "other"}} {
		rows, err := st.ListTasks(ctx, scope[0], scope[1])
		if err != nil || len(rows) != 1 || rows[0]["status"] != "running" {
			t.Errorf("changed another goal in %v: %+v %v", scope, rows, err)
		}
	}
}
