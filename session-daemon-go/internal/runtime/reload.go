package runtime

import (
	"context"
	"errors"
	"fmt"
)

var ErrRuntimeBusy = errors.New("runtime activity is not confirmed idle")

type ConfigReloader interface{ ReloadConfig(context.Context) error }

// reserveLocked gates starts/commands against per-runtime maintenance. Metadata
// locks are never held while the native reload callback performs IO.
func (r *Registry) reserveLocked(kind string) error {
	if r.reloading[kind] {
		return ErrRuntimeBusy
	}
	r.inflight++
	r.operations[kind]++
	return nil
}
func (r *Registry) release(kind string) {
	r.mu.Lock()
	r.inflight--
	r.operations[kind]--
	r.mu.Unlock()
}
func (r *Registry) BusyFor(kind string) bool {
	r.mu.RLock()
	busy := r.operations[kind] > 0 || r.reloading[kind]
	owners := map[string]bool{}
	for id, owner := range r.owners {
		if owner == kind {
			owners[id] = true
		}
	}
	r.mu.RUnlock()
	if busy {
		return true
	}
	states := r.Sessions()
	for id := range owners {
		state, ok := states[id]
		if !ok || state.Status != "idle" {
			return true
		}
	}
	return false
}
func (r *Registry) ReloadConfig(ctx context.Context, kind string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	adapter := r.adapters[kind]
	reloader, ok := adapter.(ConfigReloader)
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("runtime %q has no native config reloader", kind)
	}
	if r.operations[kind] > 0 || r.reloading[kind] {
		r.mu.Unlock()
		return ErrRuntimeBusy
	}
	r.reloading[kind] = true
	r.inflight++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.reloading, kind); r.inflight--; r.mu.Unlock() }()
	// The lease prevents new native work between snapshot and reload.
	r.mu.RLock()
	owned := map[string]Result{}
	for id, owner := range r.owners {
		if owner == kind {
			owned[id] = r.states[id]
		}
	}
	r.mu.RUnlock()
	if snapshotter, ok := adapter.(Snapshotter); ok {
		snapshot := snapshotter.Snapshot()
		for id := range owned {
			current, found := snapshot[id]
			if !found || current.SessionID != id {
				return ErrRuntimeBusy
			}
			owned[id] = current
		}
	}
	for _, state := range owned {
		if state.Status != "idle" {
			return ErrRuntimeBusy
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return reloader.ReloadConfig(ctx)
}
