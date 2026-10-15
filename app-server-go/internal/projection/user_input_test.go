package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestUserInputProjectsQuestionsAndResolution(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	questions := []any{map[string]any{"id": "q", "header": "选择", "question": "你喜欢什么？", "options": []any{map[string]any{"label": "音乐", "description": "放松"}}}}
	var events []store.Event
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		events, e = projectCodexNotificationTx(ctx, tx, "stream", "a", map[string]any{"agent_id": "a", "session_id": "s", "approval_id": "codex:91", "frame": map[string]any{"id": 91, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "s", "questions": questions, "itemId": "item", "turnId": "turn", "isBlocking": true}}})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "approval.upsert" {
			if event.Data["questions"] == nil || event.Data["method"] != "item/tool/requestUserInput" {
				t.Fatal(event.Data)
			}
			if _, err = st.AppendEvent(ctx, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	pending, err := st.PendingApprovals(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %v %v", pending, err)
	}
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		events, e = projectCodexNotificationTx(ctx, tx, "stream", "a", map[string]any{"agent_id": "a", "session_id": "s", "frame": map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "s", "requestId": 91}}})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "approval.upsert" {
			if _, err = st.AppendEvent(ctx, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	pending, err = st.PendingApprovals(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("resolved request remains pending: %v %v", pending, err)
	}
}
