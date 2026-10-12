package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// This executable is a private synthetic native stdio peer, never an Agent.
func TestGroupNativeProcessHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "astrorder-group-helper" {
		return
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	threadID := fmt.Sprintf("native-fixture-%d", os.Getpid())
	for {
		var request map[string]any
		if decoder.Decode(&request) != nil {
			os.Exit(0)
		}
		id, hasID := request["id"]
		if !hasID {
			continue
		}
		method, _ := request["method"].(string)
		params, _ := request["params"].(map[string]any)
		result := map[string]any{}
		switch method {
		case "initialize":
			result = map[string]any{"serverInfo": map[string]any{"name": "synthetic"}}
		case "thread/start":
			result = map[string]any{"thread": map[string]any{"id": threadID}}
		case "thread/resume":
			result = map[string]any{"thread": map[string]any{"id": params["threadId"]}}
		case "turn/start":
			result = map[string]any{"turn": map[string]any{"id": "group-turn"}}
			_ = encoder.Encode(map[string]any{"id": id, "result": result})
			for _, frame := range []map[string]any{
				{"method": "turn/started", "params": map[string]any{"threadId": threadID, "turn": map[string]any{"id": "group-turn"}}},
				{"method": "item/completed", "params": map[string]any{"threadId": threadID, "turnId": "group-turn", "item": map[string]any{"id": "group-answer", "type": "agentMessage", "text": "合成原生回复"}}},
				{"method": "turn/completed", "params": map[string]any{"threadId": threadID, "turn": map[string]any{"id": "group-turn", "status": "completed"}}},
			} {
				_ = encoder.Encode(frame)
			}
			continue
		case "thread/items/list":
			result = map[string]any{"data": []any{map[string]any{"id": "native-history-item", "type": "agentMessage", "text": "原生分页记录"}}, "nextCursor": nil}
		}
		if strings.Contains(method, "interrupt") {
			result["ok"] = true
		}
		_ = encoder.Encode(map[string]any{"id": id, "result": result})
	}
}
