package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
)

func (a *Adapter) backgroundProcesses(ctx context.Context, rt *Runtime, r core.Request) (core.Result, error) {
	response, err := rt.request(ctx, "thread/read", map[string]any{"threadId": r.SessionID, "includeTurns": true})
	if err != nil {
		return core.Result{}, err
	}
	thread, ok := response["thread"].(map[string]any)
	if !ok {
		return core.Result{}, fmt.Errorf("native thread is absent")
	}
	if id, _ := thread["id"].(string); id != r.SessionID {
		return core.Result{}, fmt.Errorf("native thread identity mismatch")
	}
	processes := []any{}
	turns, ok := thread["turns"].([]any)
	if !ok {
		return core.Result{}, fmt.Errorf("native turns are absent")
	}
	for _, value := range turns {
		turn, _ := value.(map[string]any)
		items, _ := turn["items"].([]any)
		for _, value := range items {
			item, _ := value.(map[string]any)
			pid, _ := item["processId"].(string)
			if item["type"] != "commandExecution" || item["status"] != "inProgress" || pid == "" {
				continue
			}
			output, _ := item["aggregatedOutput"].(string)
			if chars := []rune(output); len(chars) > 4000 {
				output = string(chars[len(chars)-4000:])
			}
			processes = append(processes, map[string]any{"session_id": pid, "command": item["command"], "cwd": item["cwd"], "status": "running", "output_tail": output, "item_id": item["id"]})
		}
	}
	return core.Result{SessionID: r.SessionID, Payload: map[string]any{"processes": processes}}, nil
}
