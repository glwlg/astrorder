package grok

import (
	"context"
	"errors"
	"fmt"
	"sort"

	core "astrorder.dev/session-daemon/internal/runtime"
)

// RuntimeDisconnector lets runtime.disconnect release this adapter's confirmed
// idle transports without permanently closing it, so a later control attach
// can initialize cleanly.
var _ core.RuntimeDisconnector = (*Adapter)(nil)

// runtimeControlParams reports whether a spawn request is a control-only
// attach: initialize the native ACP transport and bind the exact control
// identity without creating, loading, or prompting any native session.
func runtimeControlParams(r core.Request) bool {
	params, _ := r.Fields["params"].(map[string]any)
	if params == nil {
		return false
	}
	control, _ := params["runtime_control"].(bool)
	return control
}

// controlSpawn binds one control-only session. It initializes the ACP
// transport through the same start path used by ordinary sessions but with
// load=false, so it never issues session/new, session/load, session/prompt,
// or any other native conversation method. A repeated spawn over a live
// transport reuses it; a transport that already failed is retired and
// replaced by a fresh initialization. The session is registered only after
// initialization is confirmed.
func (a *Adapter) controlSpawn(ctx context.Context, r core.Request) (core.Result, error) {
	sessionID := r.SessionID
	if sessionID == "" && r.Fields != nil {
		if s, ok := r.Fields["session_id"].(string); ok {
			sessionID = s
		}
	}
	if sessionID == "" || len(sessionID) > 256 {
		return core.Result{}, fmt.Errorf("grok control session ID is invalid")
	}
	rawCwd := ""
	if r.Fields != nil {
		if c, ok := r.Fields["cwd"].(string); ok && c != "" {
			rawCwd = c
		} else if w, ok := r.Fields["workspace"].(string); ok && w != "" {
			rawCwd = w
		}
	}
	workspace, err := a.workspace(rawCwd)
	if err != nil {
		return core.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("grok adapter is closed")
	}
	if a.opening[sessionID] {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("grok control attach is already in progress")
	}
	var dead *ownedSession
	if existing := a.sessions[sessionID]; existing != nil {
		if !existing.control {
			a.mu.Unlock()
			return core.Result{}, fmt.Errorf("grok session already attached")
		}
		existing.mu.Lock()
		status := existing.status
		failed := existing.failed
		existing.mu.Unlock()
		if !failed && status != "error" {
			// Live control transport: reuse it without a new native process.
			agentID := a.config.AgentID
			a.mu.Unlock()
			return a.controlResult(sessionID, agentID, status), nil
		}
		// The previous control transport failed; retire it before replacing.
		// The binding stays registered until its native cleanup is confirmed.
		dead = existing
	}
	a.opening[sessionID] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.opening, sessionID)
		a.mu.Unlock()
	}()
	if dead != nil {
		dead.mu.Lock()
		c := dead.client
		dead.closed = true
		dead.mu.Unlock()
		var closeErr error
		if c == nil {
			closeErr = fmt.Errorf("grok control session %s has no owned transport to retire", sessionID)
		} else {
			closeErr = c.close()
		}
		if closeErr != nil {
			// Unconfirmed native cleanup: keep the original error binding and
			// refuse the replacement so a possibly live process is never
			// silently orphaned or double-started.
			return core.Result{}, fmt.Errorf("grok control session %s cleanup is unconfirmed: %w", sessionID, closeErr)
		}
		a.mu.Lock()
		if a.sessions[sessionID] == dead {
			delete(a.sessions, sessionID)
		}
		a.mu.Unlock()
	}

	owned, err := a.openSessionMode(ctx, sessionID, workspace, false, true)
	if err != nil {
		return core.Result{}, err
	}
	owned.mu.Lock()
	status := owned.status
	failed := owned.failed
	owned.mu.Unlock()
	if failed || status != "idle" {
		// The transport failed during or right after initialization; drop the
		// binding instead of leaving a non-idle control session registered.
		a.mu.Lock()
		if a.sessions[sessionID] == owned {
			delete(a.sessions, sessionID)
		}
		a.mu.Unlock()
		if owned.client != nil {
			_ = owned.client.close()
		}
		return core.Result{}, fmt.Errorf("grok control transport is not idle after initialization")
	}
	return a.controlResult(sessionID, a.config.AgentID, status), nil
}

// controlCommand serves a control-only binding. The official read-only model
// catalog is the only supported action; it is answered from the initialize
// metadata with zero native requests. Every native-session action (send,
// settings, rename, history, delete, model writes, approval, interrupt, ...)
// is rejected explicitly; a pre-cancelled context never reports success.
func (a *Adapter) controlCommand(ctx context.Context, owned *ownedSession, sessionID, action string) (core.Result, error) {
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	if action != "session.models" {
		return core.Result{}, fmt.Errorf("grok control session does not support action %s", action)
	}
	owned.mu.Lock()
	items := make([]map[string]any, len(owned.models))
	copy(items, owned.models)
	status := owned.status
	owned.mu.Unlock()
	payload := a.controlPayload(sessionID, a.config.AgentID, status)
	payload["items"] = items
	return core.Result{SessionID: sessionID, Status: status, Payload: payload}, nil
}

// controlPayload is the exact metadata a bound control session reports. The
// registry binds agent control metadata from this payload, and repeated
// spawns over the live transport return the same identity.
func (a *Adapter) controlPayload(sessionID, agentID, status string) map[string]any {
	name := a.config.AgentName
	if name == "" {
		name = "Grok Build"
	}
	return map[string]any{
		"name":            name,
		"connection_id":   a.config.ConnectionID,
		"runtime_id":      agentID,
		"source_id":       agentID,
		"session_id":      sessionID,
		"status":          status,
		"agent_id":        agentID,
		"runtime_control": true,
		"control_state":   "owned",
	}
}

func (a *Adapter) controlResult(sessionID, agentID, status string) core.Result {
	return core.Result{SessionID: sessionID, Status: status, Payload: a.controlPayload(sessionID, agentID, status)}
}

// Shutdown gracefully releases this adapter's confirmed idle transports
// without permanently closing the adapter. Any non-idle session, an attach in
// progress, or a closed adapter refuses the release so the daemon's
// active-session guard is never weakened; a later control attach
// re-initializes cleanly. A session whose transport close cannot confirm
// native cleanup keeps its binding marked error and refuses the release
// instead of faking a successful disconnect.
func (a *Adapter) Shutdown(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, fmt.Errorf("grok adapter is closed")
	}
	if len(a.opening) > 0 {
		a.mu.Unlock()
		return nil, fmt.Errorf("grok runtime has an attach in progress; disconnect refused")
	}
	sessions := make(map[string]*ownedSession, len(a.sessions))
	for id, s := range a.sessions {
		sessions[id] = s
	}
	for id, s := range sessions {
		s.mu.Lock()
		status := s.status
		s.mu.Unlock()
		if status != "idle" {
			a.mu.Unlock()
			return nil, fmt.Errorf("grok session %s is not idle; stop it before disconnecting", id)
		}
	}
	a.mu.Unlock()

	// Release outside the adapter lock: a native close waits on the owned
	// process. Only a confirmed close is acknowledged; a session whose
	// transport cannot confirm cleanup keeps its binding and stays error.
	released := make([]string, 0, len(sessions))
	var failures error
	for id, s := range sessions {
		s.mu.Lock()
		s.closed = true
		c := s.client
		s.mu.Unlock()
		var closeErr error
		if c == nil {
			closeErr = fmt.Errorf("grok session %s has no owned transport to release", id)
		} else {
			closeErr = c.close()
		}
		if closeErr != nil {
			s.mu.Lock()
			s.status = "error"
			s.failed = true
			s.lastError = closeErr.Error()
			s.mu.Unlock()
			failures = errors.Join(failures, fmt.Errorf("grok session %s cleanup is unconfirmed: %w", id, closeErr))
			continue
		}
		a.mu.Lock()
		if a.sessions[id] == s {
			delete(a.sessions, id)
		}
		a.mu.Unlock()
		released = append(released, id)
	}
	sort.Strings(released)
	if failures != nil {
		return released, failures
	}
	return released, nil
}
