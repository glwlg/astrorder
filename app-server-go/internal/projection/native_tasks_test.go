package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSubagentsRemainActiveAfterSpawnToolCompletes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, a := range []string{"a", "other"} {
		if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": a, "status": "idle", "updated_at": "2026-10-13T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"codex", "hermes"} {
		for _, state := range []string{"running", "completed"} {
			err := st.WithTx(ctx, func(tx *sql.Tx) error {
				var events []store.Event
				var err error
				if source == "codex" {
					events, err = projectCodexNotificationTx(ctx, tx, "s", "a", map[string]any{"frame": map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "s", "item": map[string]any{"id": "spawn", "type": "collabAgentToolCall", "tool": "spawnAgent", "status": "completed", "senderThreadId": "s", "receiverThreadIds": []any{"child"}, "agentsStates": map[string]any{"child": map[string]any{"status": state, "message": "private subagent transcript"}}, "prompt": "verify", "model": "model-a"}}}})
				} else {
					phase := "subagent.start"
					if state == "completed" {
						phase = "subagent.complete"
					}
					events, err = projectHermesStreamTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s", "frame": map[string]any{"type": phase, "payload": map[string]any{"subagent_id": "child", "child_session_id": "child-session", "goal": "verify", "status": state, "model": "model-a", "tool_count": 3, "result": "private subagent transcript"}}})
				}
				if err != nil {
					return err
				}
				live := false
				for _, event := range events {
					raw, _ := json.Marshal(event.Data)
					if strings.Contains(string(raw), "private subagent transcript") {
						if event.Type == "codex.notification" {
							continue
						}
						if event.Type != "task.live" {
							return fmt.Errorf("subagent transcript entered durable event: %s", event.Type)
						}
						live = true
					}
				}
				if !live {
					return fmt.Errorf("subagent output missing from live event")
				}
				for _, e := range events {
					if e.Type == "task.upsert" && e.Data["kind"] == "subagent" && e.Data["status"] == state && e.Data["target_id"] != nil {
						return nil
					}
				}
				return fmt.Errorf("lost %s subagent lifecycle %s: %+v", source, state, events)
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	rows, err := st.ListTasks(ctx, "a", "s")
	if err != nil || len(rows) != 2 {
		t.Fatal("child identity not stable", rows, err)
	}
	var count int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE progress LIKE '%private subagent transcript%'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("subagent output persisted", count, err)
	}
	rows, err = st.ListTasks(ctx, "other", "s")
	if err != nil || len(rows) != 0 {
		t.Fatal("cross-agent task leak", rows, err)
	}
}
