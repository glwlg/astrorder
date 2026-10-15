package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestCodexMessageKindsStayStableAcrossLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "running", "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ native, kind, role string }{
		{"agentMessage", "message", "assistant"}, {"userMessage", "message", "user"},
		{"reasoning", "thinking", "assistant"}, {"plan", "message", "assistant"},
	} {
		t.Run(tc.native, func(t *testing.T) {
			for _, method := range []string{"item/started", "item/" + tc.native + "/delta", "item/completed"} {
				if tc.native == "userMessage" && method == "item/userMessage/delta" {
					continue
				}
				params := map[string]any{"threadId": "s", "turnId": "original-turn", "itemId": tc.native, "delta": "progress", "item": map[string]any{"id": tc.native, "type": tc.native, "text": "progress"}}
				err = st.WithTx(ctx, func(tx *sql.Tx) error {
					events, e := projectCodexNotificationTx(ctx, tx, "s", "a", map[string]any{"frame": map[string]any{"method": method, "params": params}})
					if e != nil {
						return e
					}
					found := false
					for _, event := range events {
						if event.Type != "message.upsert" && event.Type != "message.delta" {
							continue
						}
						found = true
						if event.Data["turn_id"] != "original-turn" {
							t.Errorf("%s: native turn lost: %v", method, event.Data["turn_id"])
						}
						if event.Data["kind"] != tc.kind || event.Data["role"] != tc.role {
							t.Errorf("%s: got kind=%v role=%v", method, event.Data["kind"], event.Data["role"])
						}
					}
					if !found {
						t.Errorf("%s: live message missing", method)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
