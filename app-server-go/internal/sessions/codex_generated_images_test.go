package sessions

import "testing"

func TestCodexGeneratedImageHistory(t *testing.T) {
	item := map[string]any{"type": "imageGeneration", "id": "image-call", "savedPath": "/home/user/.codex/generated_images/result.png", "status": "completed"}
	for _, kind := range []string{"codex", "codex-ssh"} {
		rows := projectHistoryItem(item, "a", "s", "s", "a", kind)
		if len(rows) != 1 || rows[0]["role"] != "assistant" || rows[0]["kind"] != "message" {
			t.Fatalf("missing image message: %#v", rows)
		}
		attachments := rows[0]["attachments"].([]any)
		if len(attachments) != 1 {
			t.Fatalf("missing image: %#v", rows)
		}
	}
}
