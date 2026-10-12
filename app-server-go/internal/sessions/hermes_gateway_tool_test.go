package sessions

import "testing"

func TestHermesGatewayToolWithoutRowIDSurvives(t *testing.T) {
	rows := projectHistoryItem(map[string]any{"role": "tool", "name": "terminal", "context": "run command", "tool_call_id": "call-1"}, "a", "s", "s", "a", "hermes")
	if len(rows) != 1 {
		t.Fatal("gateway tool dropped without row_id")
	}
	tool := rows[0]["tool"].(map[string]any)
	if tool["name"] != "terminal" || rows[0]["kind"] != "tool" {
		t.Fatalf("invalid projection: %+v", rows)
	}
}
