package runtime

import (
	"context"
	"fmt"
)

type Querier interface {
	Query(context.Context, Request) (map[string]any, error)
}

func (r *Registry) Query(ctx context.Context, request Request) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	method, ok := request.Fields["method"].(string)
	if !ok || method == "" {
		return nil, fmt.Errorf("runtime method is required")
	}
	r.mu.Lock()
	adapter, ok := r.adapters[request.AgentType].(Querier)
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("agent type does not support runtime requests")
	}
	if err := r.reserveLocked(request.AgentType); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.mu.Unlock()
	defer r.release(request.AgentType)
	result, err := adapter.Query(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("runtime request result must be an object")
	}
	return result, nil
}
