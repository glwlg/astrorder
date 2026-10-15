package hermes

import (
	"context"
	"time"
)

// refreshGoal reads the native control API. Unsupported/failed reads preserve
// the last projection instead of manufacturing an empty goal.
func (a *Adapter) refreshGoal(ctx context.Context, handle string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := a.rpc(ctx, "session.control.read", map[string]any{"session_id": handle})
	if err != nil {
		return
	}
	control, ok := result["control"].(map[string]any)
	if !ok {
		return
	}
	raw, present := control["goal"]
	if !present {
		return
	}
	if raw != nil {
		if _, ok := raw.(map[string]any); !ok {
			return
		}
	}
	a.handleGatewayEvent(map[string]any{"type": "session.control.update", "session_id": handle, "payload": map[string]any{"control": control}})
}
