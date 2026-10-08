package runtime

import (
	"context"
	"fmt"
	"sync"
)

type Request struct {
	AgentType string
	SessionID string
	Action    string
	Fields    map[string]any
}

type Result struct {
	SessionID string
	Status    string
	Payload   map[string]any
}

type Adapter interface {
	Create(context.Context, Request) (Result, error)
}

type Registry struct {
	mu         sync.RWMutex
	adapters   map[string]Adapter
	owners     map[string]string
	states     map[string]Result
	opening    map[string]bool
	inflight   int
	operations map[string]int
	reloading  map[string]bool
}

func NewRegistry() *Registry {
	return &Registry{adapters: map[string]Adapter{}, owners: map[string]string{}, states: map[string]Result{}, opening: map[string]bool{}, operations: map[string]int{}, reloading: map[string]bool{}}
}

func (registry *Registry) Register(agentType string, adapter Adapter) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.adapters[agentType] = adapter
}

func (registry *Registry) Create(ctx context.Context, request Request) (Result, error) {
	registry.mu.RLock()
	adapter := registry.adapters[request.AgentType]
	registry.mu.RUnlock()
	if adapter == nil {
		return Result{}, fmt.Errorf("runtime %q is not registered", request.AgentType)
	}
	registry.mu.Lock()
	if err := registry.reserveLocked(request.AgentType); err != nil {
		registry.mu.Unlock()
		return Result{}, err
	}
	registry.mu.Unlock()
	defer registry.release(request.AgentType)
	result, err := adapter.Create(ctx, request)
	if err != nil {
		return Result{}, err
	}
	return registry.bind(request.AgentType, result)
}
