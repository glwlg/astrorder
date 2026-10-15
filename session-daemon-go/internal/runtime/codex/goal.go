package codex

import (
	"context"
	"time"
)

// refreshGoal recovers state when attaching to an existing native thread.
// Older native servers may not implement goals; a failed read must never clear
// a previously projected goal or prevent the conversation from opening.
func (runtime *Runtime) refreshGoal(ctx context.Context, sessionID string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	response, err := runtime.request(ctx, "thread/goal/get", map[string]any{"threadId": sessionID})
	if err != nil {
		return
	}
	raw, present := response["goal"]
	if !present {
		return
	}
	method := "thread/goal/cleared"
	params := map[string]any{"threadId": sessionID}
	if raw != nil {
		goal, ok := raw.(map[string]any)
		if !ok || goal["threadId"] != sessionID {
			return
		}
		method = "thread/goal/updated"
		params["goal"] = goal
	}
	runtime.handleNotification(method, map[string]any{"method": method, "params": params})
}
