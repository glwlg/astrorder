package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCodexAsyncQuestionsSurviveLiveProjection(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, method := range []string{"item/started", "item/completed"} {
		err = st.WithTx(context.Background(), func(tx *sql.Tx) error {
			events, e := projectCodexNotificationTx(context.Background(), tx, "stream", "a", map[string]any{"agent_id": "a", "session_id": "s", "frame": map[string]any{"method": method, "params": map[string]any{"turnId": "turn", "item": map[string]any{"id": "question", "type": "agentMessage", "text": "问题", "delivery": "async", "questions": []any{map[string]any{"title": "问题", "options": []any{"甲", "乙"}}}}}}})
			if e != nil {
				return e
			}
			found := false
			for _, ev := range events {
				if ev.Type == "approval.upsert" {
					t.Fatal("async question became synchronous approval")
				}
				if ev.Type == "message.upsert" {
					found = true
					if ev.Data["delivery"] != "async" || ev.Data["questions"] == nil {
						t.Fatalf("lost question: %#v", ev.Data)
					}
				}
			}
			if !found {
				t.Fatal("no live message")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
