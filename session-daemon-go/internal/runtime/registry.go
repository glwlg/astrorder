package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

// SessionOwnerChecker may be implemented by adapters (e.g. Codex) that track
// live session ownership internally for observer suppression.
type SessionOwnerChecker interface {
	OwnsSession(id string) bool
}

type adapterSlot struct {
	kind     string
	adapter  Adapter
	onRetire func() error
	inflight int
	retired  bool
	closing  bool
}

func closeSlot(slot *adapterSlot) error {
	if slot == nil {
		return nil
	}
	var err error
	if slot.onRetire != nil {
		err = errors.Join(err, slot.onRetire())
	}
	if closer, ok := slot.adapter.(interface{ Close() error }); ok {
		err = errors.Join(err, closer.Close())
	}
	return err
}

type Registry struct {
	mu         sync.RWMutex
	adapters   map[string]Adapter
	slots      map[string]*adapterSlot
	owners     map[string]string
	ownerSlots map[string]*adapterSlot
	retired    map[*adapterSlot]bool
	identities map[string]SessionReadIdentity
	states     map[string]Result
	// generations records each session's binding generation: a strictly
	// increasing token assigned when the session is bound to a live runtime.
	// It lets authority snapshots detect a close+rebind that raced their
	// adapter read even when the identity fields are unchanged or all nil.
	generations    map[string]uint64
	nextGeneration uint64
	opening        map[string]bool
	inflight       int
	operations     map[string]int
	reloading      map[string]bool
	draining       bool
}

func NewRegistry() *Registry {
	return &Registry{
		adapters:   map[string]Adapter{},
		slots:      map[string]*adapterSlot{},
		owners:     map[string]string{},
		ownerSlots: map[string]*adapterSlot{},
		retired:    map[*adapterSlot]bool{},
		identities: map[string]SessionReadIdentity{},
		states:     map[string]Result{},
		generations: map[string]uint64{},
		opening:    map[string]bool{},
		operations: map[string]int{},
		reloading:  map[string]bool{},
	}
}

func (r *Registry) slotForSessionLocked(sessionID, kind string) *adapterSlot {
	if slot := r.ownerSlots[sessionID]; slot != nil {
		return slot
	}
	return r.slots[kind]
}

func (r *Registry) slotHasOwnersLocked(slot *adapterSlot) bool {
	for _, s := range r.ownerSlots {
		if s == slot {
			return true
		}
	}
	return false
}

func (r *Registry) countSlotOwnersLocked(slot *adapterSlot) int {
	count := 0
	for _, s := range r.ownerSlots {
		if s == slot {
			count++
		}
	}
	return count
}

func (r *Registry) takeDrainableRetiredLocked(slot *adapterSlot) *adapterSlot {
	if slot == nil || !slot.retired || slot.closing || slot.inflight > 0 || r.slotHasOwnersLocked(slot) {
		return nil
	}
	slot.closing = true
	delete(r.retired, slot)
	return slot
}

func (r *Registry) releaseSlot(kind string, slot *adapterSlot) {
	r.mu.Lock()
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
}

func (registry *Registry) Register(agentType string, adapter Adapter) {
	_, _ = registry.SwapRuntime(agentType, adapter, nil)
}

// SwapRuntime atomically registers or replaces the active adapter for kind
// without interrupting or killing any live sessions bound to the previous
// adapter. If the previous adapter still owns live sessions or has in-flight
// operations, it is moved to the retired pool and closed automatically once
// its final session unbinds and in-flight operations complete.
func (r *Registry) SwapRuntime(kind string, adapter Adapter, onRetire func() error) (int, error) {
	if kind == "" {
		return 0, fmt.Errorf("runtime kind is required")
	}
	if adapter == nil {
		return 0, fmt.Errorf("runtime adapter is required")
	}
	r.mu.Lock()
	if r.draining {
		r.mu.Unlock()
		return 0, ErrRuntimeBusy
	}
	old := r.slots[kind]
	slot := &adapterSlot{kind: kind, adapter: adapter, onRetire: onRetire}
	r.slots[kind] = slot
	r.adapters[kind] = adapter
	if old == nil {
		r.mu.Unlock()
		return 0, nil
	}
	preserved := r.countSlotOwnersLocked(old)
	old.retired = true
	var toClose *adapterSlot
	if preserved == 0 && old.inflight == 0 && !old.closing {
		old.closing = true
		toClose = old
	} else if !old.closing {
		r.retired[old] = true
	}
	r.mu.Unlock()
	if toClose != nil {
		_ = closeSlot(toClose)
	}
	return preserved, nil
}

// UnregisterRuntime removes kind from new session admission without killing or
// interrupting existing live sessions owned by the previous adapter.
func (r *Registry) UnregisterRuntime(kind string) (int, bool, error) {
	if kind == "" {
		return 0, false, fmt.Errorf("runtime kind is required")
	}
	r.mu.Lock()
	if r.draining {
		r.mu.Unlock()
		return 0, false, ErrRuntimeBusy
	}
	old, exists := r.slots[kind]
	if !exists {
		r.mu.Unlock()
		return 0, false, nil
	}
	delete(r.slots, kind)
	delete(r.adapters, kind)
	preserved := r.countSlotOwnersLocked(old)
	old.retired = true
	var toClose *adapterSlot
	if preserved == 0 && old.inflight == 0 && !old.closing {
		old.closing = true
		toClose = old
	} else if !old.closing {
		r.retired[old] = true
	}
	r.mu.Unlock()
	if toClose != nil {
		_ = closeSlot(toClose)
	}
	return preserved, true, nil
}

// HasRuntime reports whether kind currently has an active registered adapter.
func (r *Registry) HasRuntime(kind string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.slots[kind] != nil
}

// RegisteredKinds returns the sorted list of currently active runtime kinds.
func (r *Registry) RegisteredKinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]string, 0, len(r.slots))
	for kind := range r.slots {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// TotalOwnedSessions returns the total count of live owned sessions across
// active and retired adapters.
func (r *Registry) TotalOwnedSessions() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.owners)
}

// OwnsAdapterSession reports whether the active adapter or any retired adapter
// for kind currently holds a live native session for id.
func (r *Registry) OwnsAdapterSession(kind, id string) bool {
	if kind == "" || id == "" {
		return false
	}
	r.mu.RLock()
	var adapters []Adapter
	if slot := r.slots[kind]; slot != nil {
		adapters = append(adapters, slot.adapter)
	}
	for slot := range r.retired {
		if slot.kind == kind {
			adapters = append(adapters, slot.adapter)
		}
	}
	r.mu.RUnlock()
	for _, adapter := range adapters {
		if checker, ok := adapter.(SessionOwnerChecker); ok && checker.OwnsSession(id) {
			return true
		}
	}
	return false
}

func (registry *Registry) Create(ctx context.Context, request Request) (Result, error) {
	registry.mu.Lock()
	slot := registry.slots[request.AgentType]
	if slot == nil || slot.adapter == nil {
		registry.mu.Unlock()
		return Result{}, fmt.Errorf("runtime %q is not registered", request.AgentType)
	}
	if err := registry.reserveLocked(request.AgentType); err != nil {
		registry.mu.Unlock()
		return Result{}, err
	}
	slot.inflight++
	registry.mu.Unlock()
	defer registry.releaseSlot(request.AgentType, slot)
	result, err := slot.adapter.Create(ctx, request)
	if err != nil {
		return Result{}, err
	}
	return registry.bindSlot(request, result, slot)
}
