package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"strings"
	"time"
)

// validateSessionControl runs before reserving an operation: malformed
// envelopes cannot have reached native IO and must not create uncertainty.
func validateSessionControl(request map[string]any) string {
	action, _ := request["action"].(string)
	kind, _ := request["agent_type"].(string)
	id, _ := request["session_id"].(string)
	if raw, exists := request["params"]; exists {
		if _, ok := raw.(map[string]any); !ok {
			return "params must be an object"
		}
	}
	if (action == "session.create" || action == "session.spawn") && kind == "" {
		return "agent_type is required"
	}
	if action != "session.create" && (id == "" || len(id) > 256) {
		return "session_id is invalid"
	}
	return ""
}

func (d *Daemon) RegisterRuntime(kind string, adapter core.Adapter) {
	d.registry.Register(kind, adapter)
}
func (d *Daemon) controlOnce(parent context.Context, request map[string]any) map[string]any {
	action, _ := request["action"].(string)
	switch action {
	case "session.create", "session.spawn", "session.send", "session.steer", "session.review", "session.compact", "model_config.apply", "model_config.reload":
		if err := d.journal.CheckAdmission(); err != nil {
			return errorResponse(request["request_id"], err.Error())
		}
	}
	if strings.HasPrefix(action, "model_config.") {
		return d.modelConfigControl(parent, request)
	}
	kind, _ := request["agent_type"].(string)
	id, _ := request["session_id"].(string)
	if action == "runtime.request" {
		if kind == "" {
			return errorResponse(request["request_id"], "agent_type is required")
		}
		if raw, exists := request["params"]; exists {
			if _, ok := raw.(map[string]any); !ok {
				return errorResponse(request["request_id"], "params must be an object")
			}
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		defer d.wakeModelReloads()
		result, err := d.registry.Query(ctx, core.Request{AgentType: kind, Action: action, Fields: request})
		if err != nil {
			return errorResponse(request["request_id"], err.Error())
		}
		return map[string]any{"action": action + ".result", "request_id": request["request_id"], "daemon_id": d.daemonID, "result": result}
	}
	if action == "runtime.disconnect" {
		if kind == "" {
			return errorResponse(request["request_id"], "agent_type is required")
		}
		connID, _ := request["connection_id"].(string)
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		defer d.wakeModelReloads()

		released, err := d.registry.DisconnectRuntime(ctx, kind, connID)
		if err != nil {
			return errorResponse(request["request_id"], err.Error())
		}
		for _, sid := range released {
			_ = d.journal.TrackSession(sid, "idle")
		}
		if released == nil {
			released = []string{}
		}
		return map[string]any{
			"action":     "runtime.disconnect.result",
			"request_id": request["request_id"],
			"daemon_id":  d.daemonID,
			"result": map[string]any{
				"disconnected":      true,
				"released_sessions": released,
			},
		}
	}
	if action != "session.create" && action != "session.spawn" && !strings.HasPrefix(action, "session.") {
		return errorResponse(request["request_id"], "unsupported action")
	}
	if message := validateSessionControl(request); message != "" {
		return errorResponse(request["request_id"], message)
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer d.wakeModelReloads()
	defer cancel()
	r := core.Request{AgentType: kind, SessionID: id, Action: action, Fields: request}
	var result core.Result
	var err error
	switch action {
	case "session.create":
		result, err = d.registry.Create(ctx, r)
	case "session.spawn":
		_, previouslyOwned := d.registry.Sessions()[id]
		result, err = d.registry.Spawn(ctx, r)
		if err == nil {
			if params, ok := request["params"].(map[string]any); ok {
				if rc, ok := params["runtime_control"].(bool); ok && rc {
					d.BindAgentControlSession(result.SessionID, result.Payload)
					// A restarted App has already consumed the original hello.
					// Reannounce a confirmed live Hermes control transport.
					if previouslyOwned && result.Payload["runtime_control"] == true && result.Status == "idle" && (kind == "hermes" || kind == "ssh") {
						agent := map[string]any{"id": result.Payload["agent_id"], "kind": "hermes", "name": "Hermes", "status": "ready", "capabilities": []string{"chat", "events"}, "control_state": "owned"}
						for _, key := range []string{"name", "runtime_id", "source_id", "profile_name", "connection_id"} {
							if value, ok := result.Payload[key]; ok {
								agent[key] = value
							}
						}
						err = d.Emit(result.SessionID, "connector.hello", agent, "")
					}
				}
			}
		}
	default:
		result, err = d.registry.Command(ctx, r)
	}
	if err != nil {
		return errorResponse(request["request_id"], err.Error())
	}
	if result.Payload["deleted"] == result.SessionID || result.Payload["closed"] == true {
		err = d.journal.ForgetSession(result.SessionID)
	} else {
		if current, exists := d.registry.Sessions()[result.SessionID]; exists {
			result.Status = current.Status
		}
		err = d.journal.TrackSession(result.SessionID, result.Status)
	}
	if err != nil {
		response := errorResponse(request["request_id"], "runtime operation completed but durable session state failed: "+err.Error())
		response["session_id"] = result.SessionID
		response["runtime_result"] = result.Payload
		return response
	}
	payload := result.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payload["status"] = result.Status
	return map[string]any{"action": action + ".result", "request_id": request["request_id"], "daemon_id": d.daemonID, "session_id": result.SessionID, "result": payload}
}

// Emit commits native adapter output to the same replay/live stream.
func (d *Daemon) Emit(id, event string, payload map[string]any, status string) error {
	_, err := d.journal.Commit(id, event, payload, status)
	if err == nil && status == "idle" && event != "runtime.owner" {
		d.wakeModelReloads()
	}
	return err
}
