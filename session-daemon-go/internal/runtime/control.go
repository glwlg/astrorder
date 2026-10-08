package runtime

import (
	"context"
	"errors"
	"fmt"
)

type Spawner interface {
	Spawn(context.Context, Request) (Result, error)
}
type Commander interface {
	Command(context.Context, Request) (Result, error)
}
type Snapshotter interface{ Snapshot() map[string]Result }

func validStatus(s string) bool {
	return s == "idle" || s == "running" || s == "waiting_approval" || s == "error"
}
func (r *Registry) bind(kind string, result Result) (Result, error) {
	if result.SessionID == "" || len(result.SessionID) > 256 || !validStatus(result.Status) {
		return Result{}, fmt.Errorf("invalid runtime result")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.owners[result.SessionID]; exists {
		return Result{}, fmt.Errorf("runtime returned duplicate session identity")
	}
	r.owners[result.SessionID] = kind
	r.states[result.SessionID] = result
	return result, nil
}
func (r *Registry) Spawn(ctx context.Context, request Request) (Result, error) {
	if request.SessionID == "" || len(request.SessionID) > 256 {
		return Result{}, fmt.Errorf("invalid session identity")
	}
	r.mu.Lock()
	replacing := false
	if r.reloading[request.AgentType] {
		r.mu.Unlock()
		return Result{}, ErrRuntimeBusy
	}
	if owner, exists := r.owners[request.SessionID]; exists {
		if owner != request.AgentType {
			r.mu.Unlock()
			return Result{}, fmt.Errorf("session is owned by another runtime")
		}
		result := r.states[request.SessionID]
		adapter := r.adapters[owner]
		r.mu.Unlock()
		if snapshot, ok := adapter.(Snapshotter); ok {
			current, found := snapshot.Snapshot()[request.SessionID]
			if !found || current.SessionID != request.SessionID || !validStatus(current.Status) {
				return Result{}, fmt.Errorf("native ownership snapshot is unconfirmed")
			}
			result = current
		}
		if result.Status != "error" {
			return result, nil
		}
		r.mu.Lock()
		if r.owners[request.SessionID] != owner {
			r.mu.Unlock()
			return Result{}, fmt.Errorf("session ownership changed during recovery")
		}
		replacing = true
	}
	adapter := r.adapters[request.AgentType]
	spawner, ok := adapter.(Spawner)
	if !ok {
		r.mu.Unlock()
		return Result{}, fmt.Errorf("runtime does not support spawn")
	}
	if r.opening[request.SessionID] {
		r.mu.Unlock()
		return Result{}, fmt.Errorf("session attach is already in progress")
	}
	if err := r.reserveLocked(request.AgentType); err != nil {
		r.mu.Unlock()
		return Result{}, err
	}
	r.opening[request.SessionID] = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.opening, request.SessionID); r.mu.Unlock(); r.release(request.AgentType) }()
	result, err := spawner.Spawn(ctx, request)
	if err != nil {
		return Result{}, err
	}
	if result.SessionID != request.SessionID {
		return Result{}, fmt.Errorf("runtime did not confirm requested session")
	}
	if replacing {
		if !validStatus(result.Status) {
			return Result{}, fmt.Errorf("invalid recovered native status")
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.owners[request.SessionID] != request.AgentType {
			return Result{}, fmt.Errorf("session ownership changed during recovery")
		}
		r.states[request.SessionID] = result
		return result, nil
	}
	return r.bind(request.AgentType, result)
}
func (r *Registry) Command(ctx context.Context, request Request) (Result, error) {
	r.mu.Lock()
	kind, exists := r.owners[request.SessionID]
	adapter := r.adapters[kind]
	if !exists || r.opening[request.SessionID] || (request.AgentType != "" && request.AgentType != kind) {
		r.mu.Unlock()
		return Result{}, fmt.Errorf("session is not owned by this runtime")
	}
	commander, ok := adapter.(Commander)
	if !ok {
		r.mu.Unlock()
		return Result{}, fmt.Errorf("runtime does not support commands")
	}
	if err := r.reserveLocked(kind); err != nil {
		r.mu.Unlock()
		return Result{}, err
	}
	r.mu.Unlock()
	defer r.release(kind)
	result, err := commander.Command(ctx, request)
	if err != nil {
		return Result{}, err
	}
	if result.SessionID != request.SessionID || (result.Status != "" && !validStatus(result.Status)) {
		return Result{}, fmt.Errorf("invalid runtime command result")
	}
	r.mu.Lock()
	if result.Status != "" {
		r.states[request.SessionID] = result
	}
	if result.Payload["deleted"] == request.SessionID || result.Payload["closed"] == true {
		delete(r.owners, request.SessionID)
		delete(r.states, request.SessionID)
	}
	r.mu.Unlock()
	return result, nil
}
func (r *Registry) Statuses() map[string]map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]map[string]any{}
	for kind := range r.adapters {
		out[kind] = map[string]any{"registered": true, "sessions": 0}
	}
	for _, kind := range r.owners {
		out[kind]["sessions"] = out[kind]["sessions"].(int) + 1
	}
	return out
}
func (r *Registry) Sessions() map[string]Result {
	r.mu.RLock()
	states := map[string]Result{}
	owners := map[string]string{}
	adapters := map[string]Adapter{}
	for id, result := range r.states {
		states[id] = result
		owners[id] = r.owners[id]
	}
	for kind, adapter := range r.adapters {
		adapters[kind] = adapter
	}
	r.mu.RUnlock()
	for kind, adapter := range adapters {
		if snapshot, ok := adapter.(Snapshotter); ok {
			for id, state := range snapshot.Snapshot() {
				if _, owned := states[id]; owned && owners[id] == kind {
					states[id] = state
				}
			}
		}
	}
	return states
}
func (r *Registry) Busy() bool {
	r.mu.RLock()
	busy := r.inflight > 0
	r.mu.RUnlock()
	if busy {
		return true
	}
	for _, result := range r.Sessions() {
		// Only reconciled idle proves inactivity; error/unknown is ambiguous.
		if result.Status != "idle" {
			return true
		}
	}
	return false
}
func (r *Registry) Close() error {
	var failures error
	r.mu.RLock()
	adapters := make([]Adapter, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		adapters = append(adapters, adapter)
	}
	r.mu.RUnlock()
	for _, adapter := range adapters {
		if closer, ok := adapter.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				failures = errors.Join(failures, err)
			}
		}
	}
	return failures
}
