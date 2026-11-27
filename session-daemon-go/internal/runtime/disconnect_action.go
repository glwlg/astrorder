package runtime

import (
	"context"
	"fmt"
)

// DisconnectRuntime handles "runtime.disconnect" for a registered agent type and optional connectionID.
// It verifies that no active sessions (running, waiting_approval) exist for the target scope before disconnecting.
func (r *Registry) DisconnectRuntime(ctx context.Context, agentType, connectionID string) ([]string, error) {
	return r.disconnectRuntime(ctx, agentType, connectionID, false, nil, nil)
}

func (r *Registry) ForceDisconnectRuntime(ctx context.Context, agentType, connectionID string) ([]string, error) {
	if agentType != "antigravity" && agentType != "antigravity-ssh" {
		return nil, RejectBeforeDispatch("forced restart is only supported for Antigravity")
	}
	return r.disconnectRuntime(ctx, agentType, connectionID, true, nil, nil)
}

type runtimeMaintenance func(context.Context, Adapter) error

func (r *Registry) disconnectRuntime(ctx context.Context, agentType, connectionID string, force bool, before, after runtimeMaintenance) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if agentType != "codex" && agentType != "codex-ssh" && agentType != "ssh" && agentType != "hermes" && agentType != "grok" && agentType != "grok-ssh" && agentType != "antigravity" && agentType != "antigravity-ssh" {
		return nil, RejectBeforeDispatch(fmt.Sprintf("unsupported runtime type: %s", agentType))
	}

	// Lock runtime first to prevent new requests from being scheduled or starting
	r.mu.Lock()
	activeSlot, exists := r.slots[agentType]
	var retiredSlots []*adapterSlot
	for s := range r.retired {
		if s.kind == agentType {
			retiredSlots = append(retiredSlots, s)
		}
	}
	if !exists && len(retiredSlots) == 0 {
		r.mu.Unlock()
		return nil, RejectBeforeDispatch(fmt.Sprintf("runtime %q is not registered", agentType))
	}
	var primary Adapter
	if activeSlot != nil {
		primary = activeSlot.adapter
	} else {
		primary = retiredSlots[0].adapter
	}
	if _, ok := primary.(ConnectionDisconnector); ok {
		if connectionID == "" || len(connectionID) > 128 {
			r.mu.Unlock()
			return nil, RejectBeforeDispatch("connection_id is invalid")
		}
	} else if connectionID != "" {
		r.mu.Unlock()
		return nil, RejectBeforeDispatch(fmt.Sprintf("runtime %q does not accept connection_id", agentType))
	}
	if r.draining || r.operations[agentType] > 0 || r.reloading[agentType] {
		r.mu.Unlock()
		return nil, RejectBeforeDispatch(ErrRuntimeBusy.Error())
	}
	r.reloading[agentType] = true
	r.inflight++
	targets := make([]*adapterSlot, 0, 1+len(retiredSlots))
	if activeSlot != nil {
		activeSlot.inflight++
		targets = append(targets, activeSlot)
	}
	for _, s := range retiredSlots {
		s.inflight++
		targets = append(targets, s)
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.reloading, agentType)
		r.inflight--
		var toClose []*adapterSlot
		for _, s := range targets {
			s.inflight--
			if drained := r.takeDrainableRetiredLocked(s); drained != nil {
				toClose = append(toClose, drained)
			}
		}
		r.mu.Unlock()
		for _, s := range toClose {
			_ = closeSlot(s)
		}
	}()

	// Enumerate only after maintenance admission is closed. Otherwise a new
	// session can bind after this snapshot and escape the active-state check.
	slotSessions := make(map[*adapterSlot][]string, len(targets))
	var targetSessions []string
	for _, s := range targets {
		var sids []string
		if multi, ok := s.adapter.(ConnectionDisconnector); ok {
			sids = multi.SessionsForConnection(connectionID)
		} else {
			r.mu.RLock()
			for sid, owner := range r.owners {
				if owner == agentType && r.slotForSessionLocked(sid, owner) == s {
					sids = append(sids, sid)
				}
			}
			r.mu.RUnlock()
		}
		slotSessions[s] = sids
		targetSessions = append(targetSessions, sids...)
	}

	// Active check: reject if any target session is running or waiting_approval or non-idle
	states := r.Sessions()
	var activeCount int
	for _, sid := range targetSessions {
		st, ok := states[sid]
		if !ok || st.Status != "idle" {
			activeCount++
		}
	}
	if activeCount > 0 && !force {
		return nil, RejectBeforeDispatch(fmt.Sprintf("%s has %d non-idle, running, or unconfirmed session(s); wait until all target sessions are idle before disconnecting", agentType, activeCount))
	}
	if before != nil {
		if err := before(ctx, primary); err != nil {
			return nil, err
		}
	}

	var released []string
	for _, s := range targets {
		adapter := s.adapter
		var part []string
		var err error
		if force {
			if multi, ok := adapter.(forcedConnectionDisconnector); ok {
				part, err = multi.ForceDisconnectConnection(ctx, connectionID)
			} else if single, ok := adapter.(ForcedRuntimeDisconnector); ok {
				part, err = single.ForceShutdown(ctx)
			} else {
				return nil, RejectBeforeDispatch("runtime does not support forced restart")
			}
		} else if multi, ok := adapter.(ConnectionDisconnector); ok {
			part, err = multi.DisconnectConnection(ctx, connectionID)
		} else if single, ok := adapter.(RuntimeDisconnector); ok {
			part, err = single.Shutdown(ctx)
		} else if closer, ok := adapter.(interface{ Close() error }); ok {
			err = closer.Close()
			part = slotSessions[s]
		} else {
			part = slotSessions[s]
		}
		if err != nil {
			return nil, err
		}
		released = append(released, part...)
	}

	// Unbind released sessions from Registry
	r.mu.Lock()
	for _, sid := range released {
		delete(r.owners, sid)
		delete(r.ownerSlots, sid)
		delete(r.identities, sid)
		delete(r.states, sid)
		delete(r.generations, sid)
	}
	r.mu.Unlock()

	var err error
	if after != nil {
		err = after(ctx, primary)
	}
	return released, err
}
