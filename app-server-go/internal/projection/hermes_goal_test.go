package projection

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestHermesControlGoalLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "running", "updated_at": "2026-10-12T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct{ native, want string }{{"active", "running"}, {"waiting", "running"}, {"paused", "pending"}, {"blocked", "failed"}, {"done", "completed"}, {"cleared", "cancelled"}} {
		t.Run(state.native, func(t *testing.T) {
			var goal any = map[string]any{"title": "Build", "status": state.native, "turns_used": 3, "max_turns": 20}
			if state.native == "cleared" {
				goal = nil
			}
			err := st.WithTx(ctx, func(tx *sql.Tx) error {
				events, err := projectHermesStreamTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s", "frame": map[string]any{"type": "session.control.update", "payload": map[string]any{"control": map[string]any{"goal": goal, "revision": "r"}}}})
				if err != nil {
					return err
				}
				if len(events) != 1 || events[0].Type != "task.upsert" {
					return fmt.Errorf("goal event lost: %+v", events)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := st.ListTasks(ctx, "a", "s")
			if err != nil || len(rows) != 1 {
				t.Fatalf("goal not persisted: %+v %v", rows, err)
			}
			row := rows[0]
			if row["id"] != "hermes:goal:current" || row["status"] != state.want {
				t.Fatalf("wrong goal: %+v", row)
			}
			progress := row["progress"].(map[string]any)
			if state.native != "cleared" && (fmt.Sprint(progress["turns_used"]) != "3" || fmt.Sprint(progress["max_turns"]) != "20") {
				t.Fatalf("turn progress lost: %+v", progress)
			}
		})
	}
}
