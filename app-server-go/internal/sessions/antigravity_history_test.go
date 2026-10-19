package sessions

import "testing"

func TestAntigravityHistoryProjectsThinkingBeforeResponse(t *testing.T) {
	item := map[string]any{"id": "agy-step-1", "role": "assistant", "kind": "message", "text": "answer", "thinking": "native thought", "created_at": "2026-10-18T01:00:00Z"}
	rows := projectHistoryItem(item, "agy", "session", "session", "agy", "antigravity")
	if len(rows) != 2 {
		t.Fatalf("native thought dropped: %+v", rows)
	}
	if rows[0]["kind"] != "thinking" || rows[0]["text"] != "native thought" || rows[0]["id"] == rows[1]["id"] {
		t.Fatalf("thinking identity: %+v", rows)
	}
	if rows[1]["id"] != item["id"] || rows[1]["text"] != "answer" {
		t.Fatalf("response changed: %+v", rows)
	}
	if again := projectHistoryItem(item, "agy", "session", "session", "agy", "antigravity"); again[0]["id"] != rows[0]["id"] {
		t.Fatal("unstable thought identity")
	}
}

func TestAntigravityEmptyThinkingDoesNotAddMessage(t *testing.T) {
	rows := projectHistoryItem(map[string]any{"id": "step", "role": "assistant", "text": "answer", "thinking": ""}, "agy", "session", "session", "agy", "antigravity")
	if len(rows) != 1 {
		t.Fatalf("empty thinking row: %+v", rows)
	}
}
