package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
	"strings"
)

// A parent turn finishing does not prove its commands or child agents ended.
// Reject unfamiliar active work rather than terminating it during reclamation.
func sleepSafeThread(thread map[string]any, sid string) bool {
	if thread["id"] != sid {
		return false
	}
	turns, ok := thread["turns"].([]any)
	if !ok {
		return false
	}
	for _, raw := range turns {
		turn, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		if turn["status"] == "inProgress" {
			return false
		}
		items, ok := turn["items"].([]any)
		if !ok {
			return false
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				return false
			}
			if item["status"] == "inProgress" || item["status"] == "running" || item["status"] == "pending" {
				return false
			}
			kind, _ := item["type"].(string)
			// Native collab history cannot establish that every descendant is stopped.
			if strings.HasPrefix(kind, "collab") {
				return false
			}
		}
	}
	return true
}
func (a *Adapter) confirmSleepSafe(ctx context.Context, rt *Runtime, sid string) error {
	rt.mu.Lock()
	done, confirmed := rt.processDone, rt.exitConfirmed
	rt.mu.Unlock()
	if done != nil && confirmed {
		select {
		case <-done:
			return nil
		default:
		}
	}
	if rt.Status() != "idle" {
		return core.ErrRuntimeBusy
	}
	response, err := rt.request(ctx, "thread/read", map[string]any{"threadId": sid, "includeTurns": true})
	if err != nil {
		return fmt.Errorf("confirm idle work: %w", err)
	}
	thread, _ := response["thread"].(map[string]any)
	if !sleepSafeThread(thread, sid) {
		return core.ErrRuntimeBusy
	}
	return nil
}
