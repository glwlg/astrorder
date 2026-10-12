package runtime

import (
	"strings"
)

// LiveSessionBinding is one authoritative live-ownership record from the
// registry: a session that is currently bound to a live runtime adapter.
// Journal history is deliberately excluded; a released session must not be
// reported here even if its durable journal rows still exist.
type LiveSessionBinding struct {
	SessionID string
	// Kind is the registered runtime kind (agent type) that owns the session.
	Kind string
	// Identity is the immutable binding identity captured at bind time.
	Identity SessionReadIdentity
	// State is the latest known runtime state for the session.
	State Result
}

// LiveSessionBindings returns a consistent snapshot of every session that is
// currently owned by a live runtime, keyed by session ID.
//
// The snapshot is taken under a single read lock of owners, identities and
// states; sessions that are still opening are excluded so a half-bound
// session can never be reported as live. When an adapter exposes its own
// Snapshot (the same safe pattern used by Sessions()), the newest state is
// merged without holding the registry lock, and the owner and identity are
// re-checked afterwards so a release that raced the adapter read never
// produces a live claim for a session the registry no longer owns; a
// release/re-bind that raced the adapter read drops the row entirely instead
// of pairing one binding generation's state with another's identity. The
// generation re-check uses the registry's binding-generation counter (which
// bind/replace/close always advance, even when identity fields are nil or
// unchanged) plus raw identity pointer/value coherence as defense in depth.
//
// The returned identities are deep copies; callers may retain them freely.
func (r *Registry) LiveSessionBindings() map[string]LiveSessionBinding {
	r.mu.RLock()
	owners := make(map[string]string, len(r.owners))
	identities := make(map[string]SessionReadIdentity, len(r.identities))
	baselines := make(map[string]SessionReadIdentity, len(r.identities))
	generations := make(map[string]uint64, len(r.owners))
	states := make(map[string]Result, len(r.states))
	for id, kind := range r.owners {
		if r.opening[id] {
			continue
		}
		owners[id] = kind
		// Capture the identity twice under the begin lock: the raw struct
		// keeps the live field pointers as the binding generation token,
		// and the clone is the exact value baseline the closing re-check
		// compares against (and the copy the caller may retain).
		baseline := r.identities[id]
		baselines[id] = baseline
		identities[id] = cloneIdentity(baseline)
		generations[id] = r.generations[id]
		states[id] = r.states[id]
	}
	adapters := make(map[string]Adapter, len(r.adapters))
	for kind, adapter := range r.adapters {
		adapters[kind] = adapter
	}
	r.mu.RUnlock()

	bindings := make(map[string]LiveSessionBinding, len(owners))
	for id, kind := range owners {
		bindings[id] = LiveSessionBinding{SessionID: id, Kind: kind, Identity: identities[id], State: states[id]}
	}

	// Merge the newest adapter state without holding the registry lock. The
	// same ownership re-check as Sessions(): a session released during the
	// adapter read must not reappear here.
	for kind, adapter := range adapters {
		snapshotter, ok := adapter.(Snapshotter)
		if !ok {
			continue
		}
		for id, state := range snapshotter.Snapshot() {
			binding, owned := bindings[id]
			if !owned || binding.Kind != kind {
				continue
			}
			if state.SessionID != id || !validStatus(state.Status) {
				// An unconfirmed adapter row cannot upgrade or replace the
				// registry's recorded state; keep the registry copy.
				continue
			}
			binding.State = state
			bindings[id] = binding
		}
	}

	// Re-check under a closing lock. A session released during the adapter
	// read must not reappear, and a release/re-bind that raced the adapter
	// read must not pair the previous binding's state with the new binding's
	// identity: the merged state belongs to the old generation, so the whole
	// row is dropped. Omitting a raced row is acceptable; reporting an
	// incoherent authority row is not.
	r.mu.RLock()
	for id := range bindings {
		if _, stillOwned := r.owners[id]; !stillOwned || r.opening[id] {
			delete(bindings, id)
			continue
		}
		if owner := r.owners[id]; owner != bindings[id].Kind {
			delete(bindings, id)
			continue
		}
		// The current identity must be the exact same binding generation as
		// the captured baseline. The registry's generation counter is the
		// authoritative proof (a close+rebind or recovery respawn always
		// advances it, even when identity fields are nil or unchanged); the
		// pointer and value comparisons additionally pin the identity
		// coherence so a future identity write without a generation bump
		// cannot silently pair old state with a new identity.
		if r.generations[id] != generations[id] ||
			!sameReadIdentityPointers(baselines[id], r.identities[id]) || !sameReadIdentity(bindings[id].Identity, r.identities[id]) {
			delete(bindings, id)
			continue
		}
	}
	r.mu.RUnlock()
	return bindings
}

// sameReadIdentityPointers reports whether two identities are carried by the
// exact same field pointers: nil matches only nil, and both non-nil fields
// must be the identical pointer. It is the binding-generation proof used by
// the closing re-check.
func sameReadIdentityPointers(left, right SessionReadIdentity) bool {
	return sameReadIdentityPointerField(left.AgentID, right.AgentID) && sameReadIdentityPointerField(left.ConnectionID, right.ConnectionID)
}

func sameReadIdentityPointerField(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left == right
}

// sameReadIdentity reports whether two binding identities are the exact same
// nil/value pair: nil never matches non-nil, and non-nil fields must carry
// equal values.
func sameReadIdentity(left, right SessionReadIdentity) bool {
	return sameReadIdentityField(left.AgentID, right.AgentID) && sameReadIdentityField(left.ConnectionID, right.ConnectionID)
}

func sameReadIdentityField(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneIdentity(identity SessionReadIdentity) SessionReadIdentity {
	result := SessionReadIdentity{}
	if identity.AgentID != nil {
		value := strings.Clone(*identity.AgentID)
		result.AgentID = &value
	}
	if identity.ConnectionID != nil {
		value := strings.Clone(*identity.ConnectionID)
		result.ConnectionID = &value
	}
	return result
}
