package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestNativeTranscriptPayloadsStayOutsideAppSQLite(t *testing.T) {
	for _, source := range []string{"codex", "hermes", "grok"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "fixture", "status": "running", "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			const body = "private transcript boundary sentinel"
			projector, err := st.RegisterProjector("boundary", func(tx *sql.Tx) ([]store.Event, error) {
				switch source {
				case "codex":
					return projectCodexNotificationTx(ctx, tx, "s", "a", map[string]any{"frame": map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "s", "item": map[string]any{"id": "tool", "type": "commandExecution", "command": "fixture command", "aggregatedOutput": body, "status": "completed"}}}})
				case "hermes":
					return projectHermesStreamTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s", "frame": map[string]any{"type": "tool.complete", "payload": map[string]any{"tool_id": "tool", "name": "terminal", "result_text": body}}})
				default:
					return projectGrokNotificationTx(ctx, tx, "s", map[string]any{"agent_id": "a", "session_id": "s", "frame": map[string]any{"params": map[string]any{"_meta": map[string]any{"promptId": "turn"}, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": body}}}})
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			events, err := st.ApplyFrame(ctx, "epoch", "s", 1, projector)
			if err != nil {
				t.Fatal(err)
			}
			live := false
			for _, event := range events {
				raw, _ := json.Marshal(event.Data)
				if strings.Contains(string(raw), body) {
					if !store.IsTransientEvent(event.Type) || event.Cursor != 0 {
						t.Fatalf("body in durable event: %+v", event)
					}
					live = true
				}
			}
			if !live {
				t.Fatal("live transcript lost")
			}
			for _, query := range []string{"SELECT COUNT(*) FROM messages", "SELECT COUNT(*) FROM events WHERE data LIKE '%" + body + "%'", "SELECT COUNT(*) FROM tasks WHERE progress LIKE '%" + body + "%' OR logs LIKE '%" + body + "%'"} {
				var count int
				if err := st.DB().QueryRowContext(ctx, query).Scan(&count); err != nil || count != 0 {
					t.Fatalf("transcript persisted: %d %v", count, err)
				}
			}
		})
	}
}
