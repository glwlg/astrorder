package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
)

// Query is the native catalog channel. It never creates or resumes a thread.
func (a *Adapter) Query(ctx context.Context, request core.Request) (map[string]any, error) {
	method, _ := request.Fields["method"].(string)
	switch method {
	case "initialize", "account/read", "hooks/list", "model/list", "skills/list", "thread/items/list", "thread/list", "thread/loaded/list", "thread/read":
	default:
		return nil, fmt.Errorf("Codex runtime request is not read-only: %s", method)
	}
	params := map[string]any{}
	if raw, exists := request.Fields["request_params"]; exists {
		var ok bool
		params, ok = raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Codex runtime request params must be an object")
		}
	}
	a.catalogMu.Lock()
	defer a.catalogMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, fmt.Errorf("codex adapter is closed")
	}
	rt := a.catalog
	var retired *Runtime
	if rt != nil {
		rt.mu.Lock()
		failed := rt.closed || rt.transportFailed
		rt.mu.Unlock()
		if failed {
			retired = rt
			a.catalog = nil
			rt = nil
		}
	}
	fresh := rt == nil
	if fresh {
		config := a.config
		config.OnNotification = nil
		if a.starter != nil {
			rt = NewWithSSHStarter(config, a.starter)
		} else {
			rt = New(config)
		}
		// Register before startup so Close also retires an initializing catalog.
		a.catalog = rt
	}
	a.mu.Unlock()
	if retired != nil {
		retired.Close()
	}
	if fresh {
		workspace, err := rt.workspace(a.config.Workspace)
		if err == nil {
			err = rt.start(ctx, workspace, nil)
		}
		if err != nil {
			rt.Close()
			a.mu.Lock()
			if a.catalog == rt {
				a.catalog = nil
			}
			a.mu.Unlock()
			return nil, err
		}
	}
	if method == "initialize" {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if rt.closed || rt.transportFailed {
			return nil, fmt.Errorf("codex catalog transport is closed")
		}
		result := make(map[string]any, len(rt.initialized))
		for key, value := range rt.initialized {
			result[key] = value
		}
		return result, nil
	}
	result, err := rt.request(ctx, method, params)
	if err != nil {
		rt.mu.Lock()
		failed := rt.closed || rt.transportFailed
		rt.mu.Unlock()
		if failed {
			rt.Close()
			a.mu.Lock()
			if a.catalog == rt {
				a.catalog = nil
			}
			a.mu.Unlock()
		}
	}
	return result, err
}
