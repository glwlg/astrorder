package projection

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestCodexGoalLifecycleProjectsPersistentTasks(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, agent := range []string{"a", "other"} {
		if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": agent, "status": "idle", "updated_at": "2026-10-12T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []struct{ native, want string }{
		{"active", "running"}, {"paused", "pending"}, {"blocked", "failed"},
		{"usageLimited", "pending"}, {"budgetLimited", "pending"}, {"complete", "completed"}, {"cleared", "cancelled"},
	} {
		t.Run(state.native, func(t *testing.T) {
			params := map[string]any{"threadId": "s"}
			method := "thread/goal/updated"
			if state.native == "cleared" {
				method = "thread/goal/cleared"
			} else {
				params["goal"] = map[string]any{"threadId": "s", "objective": "Build candidate", "status": state.native, "tokensUsed": 120, "tokenBudget": 500, "timeUsedSeconds": 10}
			}
			err := st.WithTx(ctx, func(tx *sql.Tx) error {
				events, err := projectCodexNotificationTx(ctx, tx, "s", "a", map[string]any{"frame": map[string]any{"method": method, "params": params}})
				if err != nil {
					return err
				}
				if len(events) != 1 || events[0].Type != "task.upsert" {
					return fmt.Errorf("goal missing from task events: %+v", events)
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
			if row["id"] != "codex:goal:current" || row["kind"] != "todo" || row["status"] != state.want {
				t.Fatalf("wrong goal task: %+v", row)
			}
			if state.native != "cleared" {
				progress, ok := row["progress"].(map[string]any)
				if !ok || progress["native_status"] != state.native || fmt.Sprint(progress["tokensUsed"]) != "120" || progress["blocking"] != false {
					t.Fatalf("native progress lost: %+v", row)
				}
			}
			foreign, err := st.ListTasks(ctx, "other", "s")
			if err != nil || len(foreign) != 0 {
				t.Fatalf("cross-agent goal leak: %+v %v", foreign, err)
			}
		})
	}
}
