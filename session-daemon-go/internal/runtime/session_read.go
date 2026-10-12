package runtime

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrSessionNotOwned        = errors.New("session is not daemon-owned")
	ErrSessionIdentity        = errors.New("session owner identity is unconfirmed or mismatched")
	ErrSessionReadUnsupported = errors.New("runtime does not support this session read")
)

// SessionReadIdentity retains binding identity separately from transient
// command receipts, which may omit identity metadata.
type SessionReadIdentity struct {
	AgentID      *string
	ConnectionID *string
}

func bindingIdentity(request Request, result Result) SessionReadIdentity {
	identity := SessionReadIdentity{}
	if value, ok := result.Payload["agent_id"].(string); ok {
		identity.AgentID = &value
	}
	if value, ok := result.Payload["connection_id"].(string); ok {
		identity.ConnectionID = &value
	}
	if identity.ConnectionID == nil {
		if params, ok := request.Fields["params"].(map[string]any); ok {
			if value, ok := params["connection_id"].(string); ok {
				identity.ConnectionID = &value
			}
		}
	}
	return identity
}

func sessionReadSupported(kind, action string) bool {
	switch action {
	case "session.models":
		return kind == "codex" || kind == "codex-ssh" || kind == "hermes" || kind == "ssh" || kind == "grok" || kind == "grok-ssh"
	case "session.commands", "session.model.read":
		return kind == "codex" || kind == "codex-ssh" || kind == "hermes" || kind == "ssh" || kind == "grok" || kind == "grok-ssh"
	case "session.history_page":
		return kind == "codex" || kind == "codex-ssh" || kind == "hermes" || kind == "ssh"
	case "session.approval.read":
		return kind == "hermes" || kind == "ssh"
	}
	return false
}

// ReadSession shares existing native Commander and maintenance admission.
// It does not treat a read receipt as ownership/status or mutate the ledger.
func (r *Registry) ReadSession(ctx context.Context, request Request, expected SessionReadIdentity) (Result, string, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, "", err
	}
	r.mu.Lock()
	kind, exists := r.owners[request.SessionID]
	if !exists || r.opening[request.SessionID] {
		r.mu.Unlock()
		return Result{}, "", ErrSessionNotOwned
	}
	identity := r.identities[request.SessionID]
	if expected.AgentID != nil && (identity.AgentID == nil || *expected.AgentID != *identity.AgentID) || expected.ConnectionID != nil && (identity.ConnectionID == nil || *expected.ConnectionID != *identity.ConnectionID) {
		r.mu.Unlock()
		return Result{}, "", ErrSessionIdentity
	}
	if !sessionReadSupported(kind, request.Action) {
		r.mu.Unlock()
		return Result{}, "", ErrSessionReadUnsupported
	}
	commander, ok := r.adapters[kind].(Commander)
	if !ok {
		r.mu.Unlock()
		return Result{}, "", ErrSessionReadUnsupported
	}
	if err := r.reserveLocked(kind); err != nil {
		r.mu.Unlock()
		return Result{}, "", err
	}
	r.mu.Unlock()
	defer r.release(kind)
	request.AgentType = kind
	result, err := commander.Command(ctx, request)
	if err != nil {
		return Result{}, kind, err
	}
	if result.SessionID != request.SessionID || result.Status != "" && !validStatus(result.Status) || result.Payload == nil {
		return Result{}, kind, fmt.Errorf("invalid runtime read result")
	}
	return result, kind, nil
}
