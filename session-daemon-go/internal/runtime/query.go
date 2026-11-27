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
	slot := r.slots[request.AgentType]
	var adapter Querier
	isQuerier := false
	if slot != nil {
		adapter, isQuerier = slot.adapter.(Querier)
	}
	if !isQuerier {
		r.mu.Unlock()
		return nil, fmt.Errorf("agent type does not support runtime requests")
	}
	if err := r.reserveLocked(request.AgentType); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	slot.inflight++
	r.mu.Unlock()
	defer r.releaseSlot(request.AgentType, slot)
	result, err := adapter.Query(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("runtime request result must be an object")
	}
	return result, nil
}
