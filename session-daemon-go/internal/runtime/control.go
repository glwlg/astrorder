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
func (r *Registry) bind(request Request, result Result) (Result, error) {
	return r.bindSlot(request, result, nil)
}
func (r *Registry) bindSlot(request Request, result Result, slot *adapterSlot) (Result, error) {
	if result.SessionID == "" || len(result.SessionID) > 256 || !validStatus(result.Status) {
		return Result{}, fmt.Errorf("invalid runtime result")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.owners[result.SessionID]; exists {
		return Result{}, fmt.Errorf("runtime returned duplicate session identity")
	}
	if slot == nil {
		slot = r.slots[request.AgentType]
	}
	r.owners[result.SessionID] = request.AgentType
	if slot != nil {
		r.ownerSlots[result.SessionID] = slot
	}
	r.identities[result.SessionID] = bindingIdentity(request, result)
	r.states[result.SessionID] = result
	r.nextGeneration++
	r.generations[result.SessionID] = r.nextGeneration
	return result, nil
}
func (r *Registry) Spawn(ctx context.Context, request Request) (Result, error) {
	if request.SessionID == "" || len(request.SessionID) > 256 {
		return Result{}, fmt.Errorf("invalid session identity")
	}
	r.mu.Lock()
	replacing := false
	var previousSlot *adapterSlot
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
		ownerSlot := r.slotForSessionLocked(request.SessionID, owner)
		var adapter Adapter
		if ownerSlot != nil {
			adapter = ownerSlot.adapter
		}
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
		previousSlot = r.ownerSlots[request.SessionID]
	}
	slot := r.slots[request.AgentType]
	if slot == nil && replacing {
		slot = previousSlot
	}
	var adapter Adapter
	if slot != nil {
		adapter = slot.adapter
	}
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
	slot.inflight++
	r.opening[request.SessionID] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.opening, request.SessionID)
		r.mu.Unlock()
		r.releaseSlot(request.AgentType, slot)
	}()
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
		if r.owners[request.SessionID] != request.AgentType {
			r.mu.Unlock()
			return Result{}, fmt.Errorf("session ownership changed during recovery")
		}
		r.ownerSlots[request.SessionID] = slot
		r.states[request.SessionID] = result
		identity := bindingIdentity(request, result)
		previous := r.identities[request.SessionID]
		if identity.AgentID == nil {
			identity.AgentID = previous.AgentID
		}
		if identity.ConnectionID == nil {
			identity.ConnectionID = previous.ConnectionID
		}
		r.identities[request.SessionID] = identity
		// A recovery respawn starts a new binding generation even when the
		// identity values are preserved: authority snapshots captured before
		// this point must not pair their adapter read with this binding.
		r.nextGeneration++
		r.generations[request.SessionID] = r.nextGeneration
		var toClose *adapterSlot
		if previousSlot != nil && previousSlot != slot {
			toClose = r.takeDrainableRetiredLocked(previousSlot)
		}
		r.mu.Unlock()
		if toClose != nil {
			_ = closeSlot(toClose)
		}
		return result, nil
	}
	return r.bindSlot(request, result, slot)
}
func (r *Registry) Command(ctx context.Context, request Request) (Result, error) {
	r.mu.Lock()
	kind, exists := r.owners[request.SessionID]
	slot := r.slotForSessionLocked(request.SessionID, kind)
	var adapter Adapter
	if slot != nil {
		adapter = slot.adapter
	}
	if !exists || r.opening[request.SessionID] || (request.AgentType != "" && request.AgentType != kind) {
		r.mu.Unlock()
		return Result{}, fmt.Errorf("session is not owned by this runtime")
	}
	commander, ok := adapter.(Commander)
	if !ok {
		r.mu.Unlock()
		return Result{}, fmt.Errorf("runtime does not support commands")
	}
	// Closing a native transport must not race any admitted operation,
	// including read-only queries which can still use that transport.
	closing := request.Action == "session.close"
	if closing && (r.operations[kind] != 0 || r.reloading[kind]) {
		r.mu.Unlock()
		return Result{}, ErrRuntimeBusy
	}
	if err := r.reserveLocked(kind); err != nil {
		r.mu.Unlock()
		return Result{}, err
	}
	if slot != nil {
		slot.inflight++
	}
	if closing {
		r.reloading[kind] = true
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if closing {
			delete(r.reloading, kind)
		}
		r.inflight--
		r.operations[kind]--
		var toClose *adapterSlot
		if slot != nil {
			slot.inflight--
			toClose = r.takeDrainableRetiredLocked(slot)
		}
		r.mu.Unlock()
		if toClose != nil {
			_ = closeSlot(toClose)
		}
	}()
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
		delete(r.ownerSlots, request.SessionID)
		delete(r.identities, request.SessionID)
		delete(r.states, request.SessionID)
		delete(r.generations, request.SessionID)
	}
	r.mu.Unlock()
	return result, nil
}
func (r *Registry) Statuses() map[string]map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]map[string]any{}
	for kind := range r.slots {
		out[kind] = map[string]any{"registered": true, "sessions": 0, "busy": r.operations[kind] > 0 || r.reloading[kind]}
	}
	for _, kind := range r.owners {
		entry, exists := out[kind]
		if !exists {
			entry = map[string]any{"registered": false, "sessions": 0, "busy": r.operations[kind] > 0 || r.reloading[kind]}
			out[kind] = entry
		}
		entry["sessions"] = entry["sessions"].(int) + 1
	}
	return out
}
func (r *Registry) Sessions() map[string]Result {
	r.mu.RLock()
	states := map[string]Result{}
	ownerSlots := map[string]*adapterSlot{}
	uniqueSlots := map[*adapterSlot]bool{}
	for id, result := range r.states {
		states[id] = result
		kind := r.owners[id]
		slot := r.slotForSessionLocked(id, kind)
		if slot != nil {
			ownerSlots[id] = slot
			uniqueSlots[slot] = true
		}
	}
	for _, slot := range r.slots {
		if slot != nil {
			uniqueSlots[slot] = true
		}
	}
	r.mu.RUnlock()
	for slot := range uniqueSlots {
		if snapshot, ok := slot.adapter.(Snapshotter); ok {
			for id, state := range snapshot.Snapshot() {
				if _, owned := states[id]; owned && ownerSlots[id] == slot {
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
	r.mu.Lock()
	slots := make([]*adapterSlot, 0, len(r.slots)+len(r.retired))
	for _, slot := range r.slots {
		if slot != nil && !slot.closing {
			slot.closing = true
			slots = append(slots, slot)
		}
	}
	for slot := range r.retired {
		if slot != nil && !slot.closing {
			slot.closing = true
			slots = append(slots, slot)
		}
	}
	r.retired = map[*adapterSlot]bool{}
	r.mu.Unlock()
	for _, slot := range slots {
		if err := closeSlot(slot); err != nil {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}
