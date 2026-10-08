package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
)

func runtimeControl(r core.Request) bool {
	params, _ := r.Fields["params"].(map[string]any)
	return params["runtime_control"] == true
}

func (a *Adapter) connectControl(ctx context.Context, r core.Request) (core.Result, error) {
	if r.SessionID == "" {
		return core.Result{}, fmt.Errorf("control identity is required")
	}
	if err := a.startProcess(ctx); err != nil {
		return core.Result{}, err
	}
	profile := a.config.ProfileName
	if profile == "" {
		profile = "default"
	}
	source := a.config.SourceID
	if source == "" {
		source = "hermes-local-" + profile
	}
	metadata := map[string]any{"agent_id": a.config.AgentID, "runtime_id": a.config.AgentID, "source_id": source, "profile_name": profile, "runtime_control": true}
	name := a.config.AgentName
	if name == "" {
		name = "Hermes"
	}
	metadata["name"] = name
	if a.config.ConnectionID != "" {
		metadata["connection_id"] = a.config.ConnectionID
	}
	a.mu.Lock()
	if a.closed || a.failed {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("Hermes transport unavailable")
	}
	a.sessions[r.SessionID] = &SessionState{SessionID: r.SessionID, Status: "idle", Metadata: metadata}
	a.mu.Unlock()
	if err := a.publishLocalOwner(r.SessionID, "idle"); err != nil {
		return core.Result{}, err
	}
	if a.config.Emit != nil {
		agent := map[string]any{"id": a.config.AgentID, "kind": "hermes", "name": "Hermes", "status": "ready", "capabilities": []string{"chat", "events"}, "control_state": "owned"}
		for k, v := range metadata {
			if k != "agent_id" && k != "runtime_control" {
				agent[k] = v
			}
		}
		if err := a.config.Emit(r.SessionID, "connector.hello", agent, ""); err != nil {
			return core.Result{}, err
		}
	}
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: metadata}, nil
}

// Query exposes read-only native discovery without acquiring a conversation handle.
func (a *Adapter) Query(ctx context.Context, r core.Request) (map[string]any, error) {
	method, _ := r.Fields["method"].(string)
	switch method {
	case "session.list", "session.active_list", "projects.tree", "projects.project_sessions", "session.history", "models.list", "commands.list":
	default:
		return nil, fmt.Errorf("Hermes runtime query method is not allowed")
	}
	if err := a.startProcess(ctx); err != nil {
		return nil, err
	}
	params, _ := r.Fields["request_params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	result, err := a.rpc(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return map[string]any{"result": result}, nil
}

func (p *Isolated) Query(ctx context.Context, r core.Request) (map[string]any, error) {
	p.mu.Lock()
	var control *Adapter
	for _, w := range p.workers {
		for _, s := range w.Snapshot() {
			if s.Payload["runtime_control"] == true {
				control = w
				break
			}
		}
		if control != nil {
			break
		}
	}
	closed := p.closed
	p.mu.Unlock()
	if closed || control == nil {
		return nil, fmt.Errorf("Hermes control connection is not ready")
	}
	return control.Query(ctx, r)
}
