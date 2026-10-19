package antigravity

import (
	"testing"
)

func TestToolErrorDoesNotAbortTurn(t *testing.T) {
	var items []map[string]any
	a := New(Config{Emit: func(id, event string, p map[string]any, status string) error {
		if p["type"] == "message.upsert" {
			items = append(items, p["data"].(map[string]any))
		}
		return nil
	}})
	s := &session{id: "11111111-1111-4111-8111-111111111111", status: "running", command: "test-command", texts: map[int]string{}}
	// Captured from the installed agy CLI against a local fixture gateway.
	err := a.step(s, map[string]any{"step_index": float64(2), "state": "ERROR", "step_type": "tool", "tool_name": "run_command", "tool_info": map[string]any{"name": "run_command", "error": map[string]any{"type": "TOOL_ERROR", "message": "user denied permission to run command"}}})
	if err != nil {
		t.Fatalf("valid native tool failure aborted conversation: %v", err)
	}
	if s.status != "running" || s.command != "test-command" {
		t.Fatal("tool failure settled the turn before native result")
	}
	tool := items[0]["tool"].(map[string]any)
	if tool["status"] != "error" || tool["error"] != "user denied permission to run command" {
		t.Fatalf("tool error lost: %+v", tool)
	}
	if err = a.step(s, map[string]any{"step_index": float64(3), "state": "DONE", "step_type": "agent_response", "text_delta": "continued response"}); err != nil {
		t.Fatal(err)
	}
	if items[1]["text"] != "continued response" {
		t.Fatal("response after failed tool missing")
	}
}
