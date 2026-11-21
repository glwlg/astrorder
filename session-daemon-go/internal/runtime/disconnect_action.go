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

	r.mu.RLock()
	adapter, exists := r.adapters[agentType]
	r.mu.RUnlock()
	if !exists {
		return nil, RejectBeforeDispatch(fmt.Sprintf("runtime %q is not registered", agentType))
	}

	if _, ok := adapter.(ConnectionDisconnector); ok {
		if connectionID == "" || len(connectionID) > 128 {
			return nil, RejectBeforeDispatch("connection_id is invalid")
		}
	} else if connectionID != "" {
		return nil, RejectBeforeDispatch(fmt.Sprintf("runtime %q does not accept connection_id", agentType))
	}

	// Lock runtime first to prevent new requests from being scheduled or starting
	r.mu.Lock()
	if r.draining || r.operations[agentType] > 0 || r.reloading[agentType] {
		r.mu.Unlock()
		return nil, RejectBeforeDispatch(ErrRuntimeBusy.Error())
	}
	r.reloading[agentType] = true
	r.inflight++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.reloading, agentType)
		r.inflight--
		r.mu.Unlock()
	}()

	// Enumerate only after maintenance admission is closed. Otherwise a new
	// session can bind after this snapshot and escape the active-state check.
	var targetSessions []string
	if multi, ok := adapter.(ConnectionDisconnector); ok {
		targetSessions = multi.SessionsForConnection(connectionID)
	} else {
		r.mu.RLock()
		for sid, owner := range r.owners {
			if owner == agentType {
				targetSessions = append(targetSessions, sid)
			}
		}
		r.mu.RUnlock()
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
		if err := before(ctx, adapter); err != nil {
			return nil, err
		}
	}

	var released []string
	var err error
	if force {
		if multi, ok := adapter.(forcedConnectionDisconnector); ok {
			released, err = multi.ForceDisconnectConnection(ctx, connectionID)
		} else if single, ok := adapter.(ForcedRuntimeDisconnector); ok {
			released, err = single.ForceShutdown(ctx)
		} else {
			return nil, RejectBeforeDispatch("runtime does not support forced restart")
		}
	} else if multi, ok := adapter.(ConnectionDisconnector); ok {
		released, err = multi.DisconnectConnection(ctx, connectionID)
	} else if single, ok := adapter.(RuntimeDisconnector); ok {
		released, err = single.Shutdown(ctx)
	} else if closer, ok := adapter.(interface{ Close() error }); ok {
		err = closer.Close()
		released = targetSessions
	} else {
		released = targetSessions
	}
	if err != nil {
		return nil, err
	}

	// Unbind released sessions from Registry
	r.mu.Lock()
	for _, sid := range released {
		delete(r.owners, sid)
		delete(r.identities, sid)
		delete(r.states, sid)
		delete(r.generations, sid)
	}
	r.mu.Unlock()

	if after != nil {
		err = after(ctx, adapter)
	}
	return released, err
}
