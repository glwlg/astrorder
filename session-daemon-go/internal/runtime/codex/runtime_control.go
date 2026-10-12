package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
	"sort"
)

// RuntimeDisconnector lets runtime.disconnect release this adapter's confirmed
// idle transports without permanently closing it, so a later control attach
// can initialize cleanly.
var _ core.RuntimeDisconnector = (*Adapter)(nil)

// runtimeControlParams reports whether a spawn request is a control-only
// attach: initialize the native transport and bind the exact control identity
// without creating or resuming any native thread.
func runtimeControlParams(r core.Request) bool {
	params, _ := r.Fields["params"].(map[string]any)
	if params == nil {
		return false
	}
	control, _ := params["runtime_control"].(bool)
	return control
}

// controlSpawn binds one control-only session. It initializes the app-server
// transport through the same start path used by catalog reads and never
// issues thread/start, thread/resume, session/new, session/load, or
// turn/start. A repeated spawn over a live transport reuses it; a transport
// that already failed is retired and replaced by a fresh initialization.
func (a *Adapter) controlSpawn(ctx context.Context, r core.Request) (core.Result, error) {
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	if r.SessionID == "" || len(r.SessionID) > 256 {
		return core.Result{}, fmt.Errorf("codex control session id is invalid")
	}
	cwd := a.config.Workspace
	if value, exists := r.Fields["cwd"]; exists {
		var ok bool
		cwd, ok = value.(string)
		if !ok || cwd == "" {
			return core.Result{}, fmt.Errorf("codex workspace is invalid")
		}
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("codex adapter is closed")
	}
	if a.opening[r.SessionID] {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("codex control attach is already in progress")
	}
	var dead *adapterSession
	if existing := a.sessions[r.SessionID]; existing != nil {
		if !existing.control {
			a.mu.Unlock()
			return core.Result{}, fmt.Errorf("codex session already attached")
		}
		status := existing.runtime.Status()
		if status != "error" {
			// Live control transport: reuse it without a new native process.
			agentID := a.config.AgentID
			a.mu.Unlock()
			return controlResult(r.SessionID, agentID, status), nil
		}
		// The previous control transport failed; retire and replace it.
		dead = existing
		delete(a.sessions, r.SessionID)
	}
	a.opening[r.SessionID] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.opening, r.SessionID); a.mu.Unlock() }()
	if dead != nil {
		dead.runtime.Close()
	}
	config := a.config
	config.OnNotification = nil
	var rt *Runtime
	if a.starter != nil {
		rt = NewWithSSHStarter(config, a.starter)
	} else {
		rt = New(config)
	}
	workspace, err := rt.workspace(cwd)
	if err == nil {
		err = rt.start(ctx, workspace, nil)
	}
	if err != nil {
		rt.Close()
		return core.Result{}, err
	}
	status := rt.Status()
	if status != "idle" {
		rt.Close()
		return core.Result{}, fmt.Errorf("codex control transport is not idle after initialization")
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		rt.Close()
		return core.Result{}, fmt.Errorf("codex adapter is closed")
	}
	if a.sessions[r.SessionID] != nil {
		a.mu.Unlock()
		rt.Close()
		return core.Result{}, fmt.Errorf("codex ownership changed during startup")
	}
	a.sessions[r.SessionID] = &adapterSession{runtime: rt, control: true}
	a.mu.Unlock()
	return controlResult(r.SessionID, a.config.AgentID, status), nil
}

// controlPayload is the exact metadata a bound control session reports. The
// registry binds agent control metadata from this payload, and repeated
// spawns over the live transport return the same identity.
func controlPayload(sessionID, agentID, status string) map[string]any {
	return map[string]any{
		"session_id":      sessionID,
		"status":          status,
		"agent_id":        agentID,
		"runtime_control": true,
		"control_state":   "owned",
	}
}

func controlResult(sessionID, agentID, status string) core.Result {
	return core.Result{SessionID: sessionID, Status: status, Payload: controlPayload(sessionID, agentID, status)}
}

// controlCommand serves a control-only binding. The official read-only model
// catalog is the only supported action: it runs over the live control
// transport with the real model/list method and keeps the exact control
// identity and live status. Every native-session action (send, settings,
// rename, history, delete, approval, ...) is rejected explicitly with zero
// native requests and zero new threads; a pre-cancelled context never
// reports success.
func (a *Adapter) controlCommand(ctx context.Context, s *adapterSession, r core.Request) (core.Result, error) {
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	if r.Action != "session.models" {
		return core.Result{}, fmt.Errorf("codex control session does not support action %s", r.Action)
	}
	rt := s.runtime
	response, err := rt.request(ctx, "model/list", map[string]any{"limit": 100})
	if err != nil {
		return core.Result{}, err
	}
	status := rt.Status()
	payload := controlPayload(r.SessionID, a.config.AgentID, status)
	payload["items"] = response["data"]
	return core.Result{SessionID: r.SessionID, Status: status, Payload: payload}, nil
}

// Shutdown gracefully releases this runtime's confirmed idle sessions and
// catalog transport without permanently closing the adapter. Any non-idle or
// initializing session refuses the release so the daemon's active-session
// guard is never weakened; a later control attach re-initializes cleanly.
func (a *Adapter) Shutdown(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Serialize with the catalog channel so a Query cannot start a fresh
	// transport while Shutdown is releasing the old one.
	a.catalogMu.Lock()
	defer a.catalogMu.Unlock()
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, fmt.Errorf("codex adapter is closed")
	}
	if len(a.opening) > 0 {
		a.mu.Unlock()
		return nil, fmt.Errorf("codex runtime has an attach in progress; disconnect refused")
	}
	sessions := make(map[string]*adapterSession, len(a.sessions))
	for id, s := range a.sessions {
		sessions[id] = s
	}
	for id, s := range sessions {
		if s.runtime.Status() != "idle" {
			a.mu.Unlock()
			return nil, fmt.Errorf("codex session %s is not idle; stop it before disconnecting", id)
		}
	}
	catalog := a.catalog
	a.catalog = nil
	a.sessions = map[string]*adapterSession{}
	a.mu.Unlock()
	if catalog != nil {
		catalog.Close()
	}
	released := make([]string, 0, len(sessions))
	for id, s := range sessions {
		released = append(released, id)
		s.runtime.Close()
	}
	sort.Strings(released)
	return released, nil
}
