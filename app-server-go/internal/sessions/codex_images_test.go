package sessions

import (
	"net/url"
	"testing"
)

func TestCodexHistoryPreservesImages(t *testing.T) {
	data := "data:image/png;base64,iVBORw0KGgo="
	for _, nested := range []bool{false, true} {
		for _, agentKind := range []string{"codex", "codex-ssh"} {
			item := map[string]any{"id": "user-image", "type": "userMessage", "content": []any{
				map[string]any{"type": "text", "text": "这个要怎么弄？"},
				map[string]any{"type": "image", "url": data},
				map[string]any{"type": "localImage", "path": `C:\截图\one #1.png`},
			}}
			if nested {
				item = map[string]any{"item": item}
			}
			rows := projectHistoryItem(item, "codex", "thread &1", "thread &1", "codex", agentKind)
			if len(rows) != 1 || rows[0]["text"] != "这个要怎么弄？" {
				t.Fatalf("bad text: %+v", rows)
			}
			attachments, _ := rows[0]["attachments"].([]any)
			if len(attachments) != 2 {
				t.Fatalf("lost native images: %+v", rows[0])
			}
			first := attachments[0].(map[string]any)
			second := attachments[1].(map[string]any)
			if first["url"] != data || first["media_type"] != "image/png" {
				t.Fatalf("bad data image: %+v", first)
			}
			link, err := url.Parse(second["url"].(string))
			if err != nil || link.Path != "/api/v1/files/raw" || link.Query().Get("path") != `C:\截图\one #1.png` || link.Query().Get("session_id") != "thread &1" || second["name"] != "one #1.png" {
				t.Fatalf("bad local image: %+v", second)
			}
			again := projectHistoryItem(item, "codex", "thread &1", "thread &1", "codex", agentKind)
			if again[0]["attachments"].([]any)[0].(map[string]any)["id"] != first["id"] {
				t.Fatal("unstable attachment identity")
			}
		}
	}
}
