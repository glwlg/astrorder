package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

// ConnectorEventStreamID names a journal-only stream for an external connector
// that has no native runtime control session. It is bound to one exact Agent.
func ConnectorEventStreamID(agentID string) string {
	digest := sha256.Sum256([]byte(agentID))
	return fmt.Sprintf("connector-%x", digest[:24])
}

const (
	DefaultMaxConnectorPendingEvents = 256
)

var (
	ErrConnectorAlreadyRegistered = errors.New("connector agent is already registered")
	ErrConnectorNotRegistered     = errors.New("connector agent is not registered")
	ErrConnectorIdentityMismatch  = errors.New("connector event agent identity does not match hello")
	ErrConnectorEventInvalid      = errors.New("connector event is invalid")
	ErrConnectorBufferOverflow    = errors.New("connector event buffer overflow before control session bind")
)

// ConnectorRegistration represents a registered connector agent session.
type ConnectorRegistration struct {
	AgentID string
	Agent   map[string]any
}

// RegisterConnectorWithRollback admits an agent with a daemon-owned stream token.
// Duplicate registration on an already-registered agentID returns ErrConnectorAlreadyRegistered.
func (d *Daemon) RegisterConnectorWithRollback(agentID string, agent map[string]any) (func(), error) {
	if agentID == "" {
		return nil, errors.New("agent id cannot be empty")
	}

	if err := d.beginControl(); err != nil {
		return nil, err
	}
	defer d.endControl()

	token := new(byte)

	d.mu.Lock()
	if _, exists := d.connectorTokens[agentID]; exists {
		d.mu.Unlock()
		return nil, ErrConnectorAlreadyRegistered
	}

	d.connectorTokens[agentID] = token
	d.connectors[agentID] = agent
	controlSessionID, hasControl := d.agentControls[agentID]
	if !hasControl {
		if len(d.pendingEvents[agentID]) >= DefaultMaxConnectorPendingEvents {
			delete(d.connectorTokens, agentID)
			delete(d.connectors, agentID)
			d.mu.Unlock()
			return nil, ErrConnectorBufferOverflow
		}
		d.pendingEvents[agentID] = append(d.pendingEvents[agentID], map[string]any{
			"_daemon_event": "connector.hello",
			"agent":         agent,
		})
	} else {
		currentStatus, statusErr := d.connectorStatusLocked(controlSessionID, nil)
		if statusErr == nil {
			_, statusErr = d.journal.Commit(controlSessionID, "connector.hello", agent, currentStatus)
		}
		if statusErr != nil {
			delete(d.connectorTokens, agentID)
			delete(d.connectors, agentID)
			d.mu.Unlock()
			return nil, statusErr
		}
	}
	d.mu.Unlock()

	var once sync.Once
	rollback := func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.connectorTokens[agentID] == token {
				delete(d.connectorTokens, agentID)
				delete(d.connectors, agentID)
				// Note: control mapping and pending events are preserved for reconnect!
			}
		})
	}
	return rollback, nil
}

// IngressConnectorEvent processes an event from a connector stream with identity verification,
// bounded pre-bind buffering, and emission to either control or session plane.
func (d *Daemon) IngressConnectorEvent(agentID string, event map[string]any, maxPending int) (committedSession string, committedSeq int64, overflow bool, err error) {
	if err := d.beginControl(); err != nil {
		return "", 0, false, err
	}
	defer d.endControl()

	eventAgentID, _ := event["agent_id"].(string)
	if eventAgentID != agentID {
		return "", 0, false, ErrConnectorIdentityMismatch
	}
	eventType, _ := event["type"].(string)
	if eventType == "" {
		return "", 0, false, ErrConnectorEventInvalid
	}

	sessionID, hasSession := event["session_id"].(string)

	d.mu.Lock()
	if _, registered := d.connectors[agentID]; !registered {
		d.mu.Unlock()
		return "", 0, false, ErrConnectorNotRegistered
	}

	if eventType == "agent.upsert" {
		if data, ok := event["data"].(map[string]any); ok {
			if curr, exists := d.connectors[agentID]; exists {
				for k, v := range data {
					curr[k] = v
				}
			}
		}
	}

	if hasSession && sessionID != "" {
		if len(sessionID) > 256 {
			d.mu.Unlock()
			return sessionID, 0, false, ErrConnectorEventInvalid
		}
		journalSessionID := sessionID
		if _, ownerErr := d.registry.ResolveSessionOwner(sessionID, core.SessionReadIdentity{}); ownerErr == nil {
			if _, identityErr := d.registry.ResolveSessionOwner(sessionID, core.SessionReadIdentity{AgentID: &agentID}); identityErr != nil {
				d.mu.Unlock()
				return sessionID, 0, false, ErrConnectorIdentityMismatch
			}
		} else if controlID := d.agentControls[agentID]; controlID == ConnectorEventStreamID(agentID) {
			journalSessionID = controlID
		}
		currentStatus, statusErr := d.connectorStatusLocked(journalSessionID, event)
		if statusErr != nil {
			d.mu.Unlock()
			return sessionID, 0, false, statusErr
		}
		appendRes, emitErr := d.journal.Commit(journalSessionID, "connector.event", event, currentStatus)
		d.mu.Unlock()
		if emitErr != nil {
			return sessionID, 0, false, emitErr
		}
		return journalSessionID, appendRes.SeqID, false, nil
	}

	controlSessionID, hasControl := d.agentControls[agentID]
	if !hasControl {
		if maxPending <= 0 {
			maxPending = DefaultMaxConnectorPendingEvents
		}
		pending := d.pendingEvents[agentID]
		if len(pending) >= maxPending {
			d.mu.Unlock()
			return "", 0, true, ErrConnectorBufferOverflow
		}
		d.pendingEvents[agentID] = append(d.pendingEvents[agentID], event)
		d.mu.Unlock()
		return "", 0, false, nil
	}

	// Ingress for control plane: serialize journal.Commit under d.mu
	controlStatus, statusErr := d.connectorStatusLocked(controlSessionID, event)
	if statusErr != nil {
		d.mu.Unlock()
		return controlSessionID, 0, false, statusErr
	}

	appendRes, emitErr := d.journal.Commit(controlSessionID, "connector.event", event, controlStatus)
	d.mu.Unlock()
	if emitErr != nil {
		return controlSessionID, 0, false, emitErr
	}
	return controlSessionID, appendRes.SeqID, false, nil
}

// BindConnectorControlSession binds an agent to a control session and flushes any buffered pre-bind events.
// It validates that the control session exists and flushes in exact arrival order.
// On emission failure, remaining events are kept at the head of the buffer and the error is returned.
func (d *Daemon) BindConnectorControlSession(agentID string, controlSessionID string) (uint32, error) {
	if agentID == "" || controlSessionID == "" {
		return 0, errors.New("agent_id and controlSessionID are required")
	}
	if err := d.beginControl(); err != nil {
		return 0, err
	}
	defer d.endControl()
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.connectors[agentID]; !ok {
		return 0, ErrConnectorNotRegistered
	}
	if existing := d.agentControls[agentID]; existing != "" && existing != controlSessionID {
		if controlSessionID != ConnectorEventStreamID(agentID) {
			return 0, ErrConnectorIdentityMismatch
		}
		// External ingress attaches to this Agent's already-authoritative native
		// control binding without replacing the native runtime's identity.
		controlSessionID = existing
	}
	if _, err := d.registry.ResolveSessionOwner(controlSessionID, core.SessionReadIdentity{}); err != nil {
		if !errors.Is(err, core.ErrSessionNotOwned) || controlSessionID != ConnectorEventStreamID(agentID) {
			return 0, err
		}
	}
	for otherAgent, streamID := range d.agentControls {
		if otherAgent != agentID && streamID == controlSessionID {
			return 0, ErrConnectorIdentityMismatch
		}
	}
	d.agentControls[agentID] = controlSessionID
	pending := d.pendingEvents[agentID]
	var flushed uint32
	for i, ev := range pending {
		kind, payload := "connector.event", ev
		if ev["_daemon_event"] == "connector.hello" {
			var ok bool
			payload, ok = ev["agent"].(map[string]any)
			if !ok {
				d.pendingEvents[agentID] = pending[i:]
				return flushed, ErrConnectorEventInvalid
			}
			kind = "connector.hello"
		}
		currentStatus, err := d.connectorStatusLocked(controlSessionID, ev)
		if err == nil {
			_, err = d.journal.Commit(controlSessionID, kind, payload, currentStatus)
		}
		if err != nil {
			d.pendingEvents[agentID] = pending[i:]
			return flushed, err
		}
		flushed++
	}
	delete(d.pendingEvents, agentID)
	return flushed, nil
}

// DisconnectConnectorExplicit forcefully marks the connector as disconnected and clears state.
// If the connector has active running runtime sessions, it fails with failed precondition.
func (d *Daemon) DisconnectConnectorExplicit(agentID string) (bool, error) {
	if agentID == "" {
		return false, errors.New("agent_id is required")
	}
	if err := d.beginControl(); err != nil {
		return false, err
	}
	defer d.endControl()
	d.mu.Lock()
	defer d.mu.Unlock()
	_, exists := d.connectors[agentID]
	if !exists {
		return false, nil
	}

	// Check if any active sessions belong to this agent
	sessions := d.registry.Sessions()
	controlID := d.agentControls[agentID]
	for sessionID, res := range sessions {
		_, identityErr := d.registry.ResolveSessionOwner(sessionID, core.SessionReadIdentity{AgentID: &agentID})
		if sessionID == controlID || identityErr == nil {
			if res.Status != "idle" {
				return false, fmt.Errorf("cannot disconnect connector with active session %s in status %s", sessionID, res.Status)
			}
		}
	}

	delete(d.connectors, agentID)
	delete(d.connectorTokens, agentID)
	delete(d.agentControls, agentID)
	delete(d.pendingEvents, agentID)
	return true, nil
}

// ConnectorCapacity returns the pending count for an agent before bind.
func (d *Daemon) ConnectorPendingCount(agentID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pendingEvents[agentID])
}
