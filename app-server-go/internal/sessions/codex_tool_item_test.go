package sessions

import "testing"

func TestProjectHistoryItemCodexToolsCarryNameAndArguments(t *testing.T) {
	command := projectHistoryItem(map[string]any{
		"id": "cmd-1", "type": "commandExecution", "command": "git status", "cwd": "/work",
		"status": "completed", "aggregatedOutput": "clean", "exitCode": float64(0),
	}, "a", "s", "native", "a", "codex")
	if len(command) != 1 {
		t.Fatalf("rows = %d", len(command))
	}
	tool, _ := command[0]["tool"].(map[string]any)
	args, _ := tool["arguments"].(map[string]any)
	if tool["name"] != "commandExecution" || args["command"] != "git status" || tool["status"] != "completed" {
		t.Fatalf("command tool = %+v", tool)
	}
	if command[0]["text"] != "clean" {
		t.Fatalf("command output = %v", command[0]["text"])
	}

	change := projectHistoryItem(map[string]any{
		"id": "fc-1", "type": "fileChange", "status": "failed",
		"changes": []any{map[string]any{"path": "a.go", "diff": "@@"}},
	}, "a", "s", "native", "a", "codex")
	tool, _ = change[0]["tool"].(map[string]any)
	args, _ = tool["arguments"].(map[string]any)
	if tool["name"] != "fileChange" || tool["status"] != "failed" || len(args["changes"].([]any)) != 1 {
		t.Fatalf("fileChange tool = %+v", tool)
	}

	mcp := projectHistoryItem(map[string]any{
		"id": "m-1", "type": "mcpToolCall", "server": "docs", "tool": "search", "status": "inProgress",
		"arguments": map[string]any{"q": "x"},
	}, "a", "s", "native", "a", "codex")
	tool, _ = mcp[0]["tool"].(map[string]any)
	if tool["name"] != "mcp:docs/search" || tool["status"] != "running" {
		t.Fatalf("mcp tool = %+v", tool)
	}
}
