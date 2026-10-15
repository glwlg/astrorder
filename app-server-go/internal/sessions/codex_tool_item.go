package sessions

import (
	"encoding/json"
	"fmt"
	"strings"
)

// codexToolFromItem converts a native Codex thread item (commandExecution,
// fileChange, mcpToolCall, webSearch, dynamicToolCall) into the browser tool
// shape {name, arguments, status} plus a display output. Codex items carry no
// generic name/args keys, so reading those yields an empty tool card.
func codexToolFromItem(item map[string]any) (map[string]any, string) {
	itemType := stringValue(item, "type")
	status := codexToolStatus(stringValue(item, "status"))
	switch itemType {
	case "collabAgentToolCall", "subAgentActivity":
		name := stringValue(item, "tool")
		if name == "" {
			name = itemType
		}
		return map[string]any{"name": name, "arguments": item, "status": status}, codexJSONText(item["agentsStates"])
	case "commandExecution":
		args := map[string]any{"command": stringValue(item, "command")}
		if cwd := stringValue(item, "cwd"); cwd != "" {
			args["cwd"] = cwd
		}
		tool := map[string]any{"name": "commandExecution", "arguments": args, "status": status}
		if code, ok := item["exitCode"]; ok && code != nil {
			tool["exit_code"] = code
		}
		output, _ := item["aggregatedOutput"].(string)
		return tool, output
	case "fileChange":
		changes, _ := item["changes"].([]any)
		if changes == nil {
			changes = []any{}
		}
		return map[string]any{"name": "fileChange", "arguments": map[string]any{"changes": changes}, "status": status}, ""
	case "mcpToolCall":
		name := "mcp:" + stringValue(item, "tool")
		if server := stringValue(item, "server"); server != "" {
			name = "mcp:" + server + "/" + stringValue(item, "tool")
		}
		args := item["arguments"]
		if args == nil {
			args = map[string]any{}
		}
		output := ""
		if result, ok := item["result"]; ok && result != nil {
			output = codexJSONText(result)
		} else if failure, ok := item["error"]; ok && failure != nil {
			output = codexJSONText(failure)
		}
		return map[string]any{"name": name, "arguments": args, "status": status}, output
	case "webSearch":
		args := map[string]any{}
		if query := stringValue(item, "query"); query != "" {
			args["query"] = query
		}
		return map[string]any{"name": "web_search", "arguments": args, "status": status}, ""
	case "dynamicToolCall":
		name := stringValue(item, "tool")
		if name == "" {
			name = "tool"
		}
		args := item["arguments"]
		if args == nil {
			args = map[string]any{}
		}
		output := ""
		if content, ok := item["contentItems"]; ok && content != nil {
			output = codexJSONText(content)
		}
		return map[string]any{"name": name, "arguments": args, "status": status}, output
	}
	return nil, ""
}

func codexToolStatus(native string) string {
	switch native {
	case "inProgress":
		return "running"
	case "failed", "declined":
		return "failed"
	case "completed":
		return "completed"
	}
	return native
}

func codexJSONText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSpace(string(raw))
}
