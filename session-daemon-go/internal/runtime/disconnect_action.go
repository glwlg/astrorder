package runtime

import (
	"context"
	"fmt"
)

// DisconnectRuntime handles "runtime.disconnect" for a registered agent type and optional connectionID.
// It verifies that no active sessions (running, waiting_approval) exist for the target scope before disconnecting.
func (r *Registry) DisconnectRuntime(ctx context.Context, agentType, connectionID string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if agentType != "codex" && agentType != "codex-ssh" && agentType != "ssh" && agentType != "hermes" && agentType != "grok" && agentType != "grok-ssh" {
		return nil, fmt.Errorf("unsupported runtime type: %s", agentType)
	}

	r.mu.RLock()
	adapter, exists := r.adapters[agentType]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("runtime %q is not registered", agentType)
	}

	if _, ok := adapter.(ConnectionDisconnector); ok {
		if connectionID == "" || len(connectionID) > 128 {
			return nil, fmt.Errorf("connection_id is invalid")
		}
	} else if connectionID != "" {
		return nil, fmt.Errorf("runtime %q does not accept connection_id", agentType)
	}

	// Lock runtime first to prevent new requests from being scheduled or starting
	r.mu.Lock()
	if r.draining || r.operations[agentType] > 0 || r.reloading[agentType] {
		r.mu.Unlock()
		return nil, ErrRuntimeBusy
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
	if activeCount > 0 {
		return nil, fmt.Errorf("%s has %d non-idle, running, or unconfirmed session(s); stop them before disconnecting", agentType, activeCount)
	}

	var released []string
	if multi, ok := adapter.(ConnectionDisconnector); ok {
		rel, err := multi.DisconnectConnection(ctx, connectionID)
		if err != nil {
			return nil, err
		}
		released = rel
	} else if single, ok := adapter.(RuntimeDisconnector); ok {
		rel, err := single.Shutdown(ctx)
		if err != nil {
			return nil, err
		}
		released = rel
	} else if closer, ok := adapter.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			return nil, err
		}
		released = targetSessions
	} else {
		released = targetSessions
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

	return released, nil
}
