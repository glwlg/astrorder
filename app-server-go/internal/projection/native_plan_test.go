package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestNativePlansReplaceSnapshotsAndPreserveScope(t *testing.T) {
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
	emit := func(source string, revision int, steps []any) {
		t.Helper()
		err := st.WithTx(ctx, func(tx *sql.Tx) error {
			if source == "hermes" {
				_, err := projectHermesStreamTx(ctx, tx, "s", map[string]any{"agent_id": "a", "frame": map[string]any{"type": "todo.updated", "payload": map[string]any{"revision": revision, "todos": steps}}})
				return err
			}
			_, err := projectCodexNotificationTx(ctx, tx, "s", "a", map[string]any{"frame": map[string]any{"method": "turn/plan/updated", "params": map[string]any{"threadId": "s", "turnId": "turn", "plan": steps}}})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"hermes", "codex"} {
		key := "content"
		running := "in_progress"
		if source == "codex" {
			key = "step"
			running = "inProgress"
		}
		emit(source, 2, []any{map[string]any{"id": "one", key: "analyse", "status": "completed"}, map[string]any{"id": "two", key: "verify", "status": running}})
		rows, _ := st.ListTasks(ctx, "a", "s")
		active := 0
		for _, r := range rows {
			if r["status"] == "running" {
				active++
			}
		}
		if active < 1 {
			t.Fatal("missing active plan", rows)
		}
		emit(source, 3, []any{map[string]any{"id": "one", key: "analyse", "status": "completed"}})
		if source == "hermes" {
			emit(source, 1, []any{map[string]any{"id": "two", key: "stale", "status": running}})
		}
	}
	rows, _ := st.ListTasks(ctx, "a", "s")
	if len(rows) != 4 {
		t.Fatal("unstable plan identity", rows)
	}
	for _, r := range rows {
		if r["title"] == "verify" && r["status"] != "cancelled" {
			t.Fatal("removed step still active", r)
		}
		if r["title"] == "stale" {
			t.Fatal("older revision applied", r)
		}
	}
	rows, _ = st.ListTasks(ctx, "other", "s")
	if len(rows) != 0 {
		t.Fatal("cross-agent leak", rows)
	}
}

func TestNativeToolFailureIsNotCompleted(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "idle", "updated_at": "2026-10-13T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	var events []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		events, err = projectCodexNotificationTx(ctx, tx, "s", "a", map[string]any{"frame": map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "s", "item": map[string]any{"id": "tool", "type": "commandExecution", "command": "go test ./...", "status": "failed", "exitCode": 1, "aggregatedOutput": "test failed"}}}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := st.ListTasks(ctx, "a", "s")
	if len(rows) != 1 || rows[0]["status"] != "failed" || rows[0]["command"] != nil {
		t.Fatal("tool failure/details lost", rows)
	}
	found := false
	for _, event := range events {
		if event.Type == "task.live" && event.Data["command"] == "go test ./..." && event.Data["progress"].(map[string]any)["output"] == "test failed" {
			found = true
		}
	}
	if !found {
		t.Fatal("tool details did not reach live clients")
	}
}
