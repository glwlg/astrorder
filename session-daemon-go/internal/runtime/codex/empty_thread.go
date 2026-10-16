package codex

import "context"

// freshThreadEmpty is only for the thread/start result held by this transport.
// Metadata-only turns:[] cannot establish emptiness for resumed/external threads.
func (runtime *Runtime) freshThreadEmpty(ctx context.Context, id string) bool {
	runtime.mu.Lock()
	valid := runtime.freshEmpty && runtime.sessionID == id && !runtime.closed && !runtime.transportFailed
	revision := runtime.emptyRevision
	runtime.mu.Unlock()
	if !valid {
		return false
	}
	response, err := runtime.request(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false})
	if err != nil {
		return false
	}
	thread, _ := response["thread"].(map[string]any)
	status, _ := thread["status"].(map[string]any)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return ctx.Err() == nil && thread["id"] == id && status["type"] == "idle" && runtime.freshEmpty && runtime.emptyRevision == revision && !runtime.closed && !runtime.transportFailed
}
