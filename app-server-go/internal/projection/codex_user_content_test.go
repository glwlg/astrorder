package projection

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestCodexUserEchoReadsNativeContent(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "running", "updated_at": "2026-10-14T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"item/started", "item/completed"} {
		err := st.WithTx(ctx, func(tx *sql.Tx) error {
			events, err := projectCodexNotificationTx(ctx, tx, "s", "a", map[string]any{"command_id": "send", "frame": map[string]any{"method": method, "params": map[string]any{"threadId": "s", "item": map[string]any{"id": "native-user", "type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "这个要怎么弄？"}, map[string]any{"type": "image", "url": "data:image/png;base64,iVBORw0KGgo="}}}}}})
			if err != nil {
				return err
			}
			found := false
			for _, event := range events {
				if event.Type == "message.upsert" {
					found = true
					if event.Data["text"] != "这个要怎么弄？" || event.Data["command_id"] != "send" {
						t.Errorf("%s: incomplete user echo: %+v", method, event.Data)
					}
					images, _ := event.Data["attachments"].([]any)
					if len(images) != 1 || images[0].(map[string]any)["url"] != "data:image/png;base64,iVBORw0KGgo=" {
						t.Errorf("%s: lost user image: %+v", method, images)
					}
				}
			}
			if !found {
				t.Error("missing user echo")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
