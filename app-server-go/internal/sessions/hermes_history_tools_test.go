package sessions

import "testing"

func TestHermesHistoryRetainsNativeToolName(t *testing.T) {
	rows := projectHistoryItem(map[string]any{"id": "1", "role": "tool", "content": "done", "tool_name": "terminal", "tool_call_id": "call-1"}, "a", "s", "s", "a", "hermes")
	tool, _ := rows[0]["tool"].(map[string]any)
	if tool["name"] != "terminal" {
		t.Fatalf("native tool name lost: %+v", tool)
	}
}
