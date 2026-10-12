package runtime

import (
	"context"
	"strings"
)

// SessionIdentity returns a copy of the immutable native binding.
func (r *Registry) SessionIdentity(id string) (SessionReadIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, exists := r.owners[id]; !exists || r.opening[id] {
		return SessionReadIdentity{}, ErrSessionNotOwned
	}
	identity := r.identities[id]
	result := SessionReadIdentity{}
	if identity.AgentID != nil {
		value := strings.Clone(*identity.AgentID)
		result.AgentID = &value
	}
	if identity.ConnectionID != nil {
		value := strings.Clone(*identity.ConnectionID)
		result.ConnectionID = &value
	}
	return result, nil
}

// ResolveSessionOwner validates immutable binding metadata before mutation.
func (r *Registry) ResolveSessionOwner(id string, expected SessionReadIdentity) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kind, exists := r.owners[id]
	if !exists || r.opening[id] {
		return "", ErrSessionNotOwned
	}
	identity := r.identities[id]
	if expected.AgentID != nil && (identity.AgentID == nil || *expected.AgentID != *identity.AgentID) || expected.ConnectionID != nil && (identity.ConnectionID == nil || *expected.ConnectionID != *identity.ConnectionID) {
		return "", ErrSessionIdentity
	}
	return kind, nil
}

func (r *Registry) ObserveSession(id string, expected SessionReadIdentity, activity string) (Result, error) {
	kind, err := r.ResolveSessionOwner(id, expected)
	if err != nil {
		return Result{}, err
	}
	if kind != "hermes" && kind != "ssh" {
		return Result{}, ErrSessionReadUnsupported
	}
	return r.Command(context.Background(), Request{AgentType: kind, SessionID: id, Action: "session.observe_status", Fields: map[string]any{"status": activity}})
}
