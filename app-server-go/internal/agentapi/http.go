package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"astrorder.dev/app-server/internal/blackboard"
)

func (h *Handler) Invoke(ctx context.Context, capability string, payload map[string]any) (any, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	switch capability {
	case "catalog.list":
		return h.invokeCatalogList(ctx, payload)
	case "agents.list":
		return h.invokeAgentsList(ctx, payload)
	case "machines.list":
		return h.invokeMachinesList(ctx, payload)
	case "projects.list":
		return h.invokeProjectsList(ctx, payload)
	case "sessions.list":
		return h.invokeSessionsList(ctx, payload)
	case "sessions.search":
		return h.invokeSessionsSearch(ctx, payload)
	case "sessions.read":
		return h.invokeSessionsRead(ctx, payload)
	case "sessions.create":
		return h.invokeSessionsCreate(ctx, payload)
	case "sessions.delete":
		return h.invokeSessionsDelete(ctx, payload)
	case "sessions.tree":
		return h.invokeSessionsTree(ctx, payload)
	case "sessions.send":
		return h.invokeSessionsSend(ctx, payload)
	case "sessions.stop":
		return h.invokeSessionsStop(ctx, payload)
	case "plugins.list":
		return h.invokePluginsList(ctx, payload)
	case "plugins.configure":
		return h.invokePluginsConfigure(ctx, payload)
	case "plugins.open":
		return h.invokePluginsOpen(ctx, payload)
	case "plugins.close":
		return h.invokePluginsClose(ctx, payload)
	case "machines.dispatch":
		return h.invokeMachinesDispatch(ctx, payload)
	case "monitor.sessions.add":
		return h.invokeMonitorSessionsAdd(ctx, payload)
	case "monitor.sessions.remove":
		return h.invokeMonitorSessionsRemove(ctx, payload)
	case "monitor.layout.set":
		return h.invokeMonitorLayoutSet(ctx, payload)
	case "browser.run":
		return h.invokeBrowserRun(ctx, payload)
	case "browser.screenshot":
		return h.invokeBrowserScreenshot(ctx, payload)
	case "browser.cdp":
		return h.invokeBrowserCDP(ctx, payload)
	case "blackboard.get":
		return h.blackboard.Get(ctx, payload)
	case "blackboard.set":
		return h.blackboard.Set(ctx, payload)
	case "blackboard.delete":
		return h.blackboard.Delete(ctx, payload)
	case "blackboard.auto_render":
		return h.blackboard.AutoRender(ctx, payload)
	case "blackboard.components.list":
		return map[string]any{"items": blackboard.Catalog}, nil
	case "blackboard.component.schema":
		return h.invokeBlackboardComponentSchema(ctx, payload)
	case "swarm.milestone.declare":
		return h.swarm.MilestoneDeclare(ctx, payload)
	case "swarm.milestone.resolve":
		return h.swarm.MilestoneResolve(ctx, payload)
	case "swarm.milestone.list":
		return h.swarm.MilestoneList(ctx)
	case "swarm.telemetry.report":
		return h.swarm.TelemetryReport(ctx, payload)
	case "swarm.telemetry.get":
		return h.swarm.TelemetryGet(ctx, payload)
	case "swarm.sos.escalate":
		return h.swarm.SOSEscalate(ctx, payload)
	case "swarm.sos.list":
		return h.swarm.SOSList(ctx)
	case "swarm.sos.resolve":
		return h.swarm.SOSResolve(ctx, payload)
	case "swarm.lock.acquire":
		return h.swarm.LockAcquire(ctx, payload)
	case "swarm.lock.release":
		return h.swarm.LockRelease(ctx, payload)
	case "swarm.lock.list":
		return h.swarm.LockList(ctx)
	default:
		return nil, NewAgentApiError("not_found", "Unknown capability")
	}
}
func agentJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func agentError(w http.ResponseWriter, err error) {
	code := 400
	var apiError *AgentApiError
	if errors.As(err, &apiError) && apiError.Code == "not_found" {
		code = 404
	}
	agentJSON(w, code, map[string]any{"detail": err.Error()})
}
func decodeAgent(w http.ResponseWriter, r *http.Request, value any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16000000))
	d.UseNumber()
	if err := d.Decode(value); err != nil {
		return errors.New("Invalid JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("Invalid JSON")
	}
	return nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	if !Matches(r.Method, path) {
		agentJSON(w, 404, map[string]any{"detail": "Not Found"})
		return
	}
	if h.options.Authorizer == nil {
		agentJSON(w, 503, map[string]any{"detail": "Authentication is not configured"})
		return
	}
	if path == "/api/v1/blackboard" {
		if failure := h.options.Authorizer.AuthorizeBrowser(r); failure != nil {
			agentJSON(w, failure.Status, map[string]any{"detail": failure.Detail})
			return
		}
	} else {
		if _, failure := h.options.Authorizer.AuthorizeAgent(r); failure != nil {
			agentJSON(w, failure.Status, map[string]any{"detail": failure.Detail})
			return
		}
	}
	caller := r.Header.Get("x-astrorder-agent-id")
	invoke := func(capability string, payload map[string]any) (any, error) {
		if payload == nil {
			payload = map[string]any{}
		}
		if caller != "" && payload["caller_agent_id"] == nil {
			payload["caller_agent_id"] = caller
		}
		return h.Invoke(r.Context(), capability, payload)
	}
	switch {
	case path == "/api/v1/agent/catalog":
		agentJSON(w, 200, map[string]any{"items": Capabilities})
	case path == "/api/v1/blackboard":
		namespace := r.URL.Query().Get("namespace")
		if namespace == "" {
			namespace = "global"
		}
		result, err := h.blackboard.Get(r.Context(), map[string]any{"namespace": namespace})
		if err != nil {
			agentError(w, err)
			return
		}
		agentJSON(w, 200, result)
	case strings.HasPrefix(path, "/api/v1/agents/"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/agents/"), "/mcp")
		if id == "" || strings.Contains(id, "/") {
			agentJSON(w, 404, map[string]any{"detail": "Not Found"})
			return
		}
		if failure := h.options.Authorizer.AuthorizeBrowser(r); failure != nil {
			agentJSON(w, failure.Status, map[string]any{"detail": failure.Detail})
			return
		}
		key := "agent_mcp_enabled:" + id
		if r.Method == http.MethodPost {
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if err := decodeAgent(w, r, &body); err != nil {
				agentError(w, err)
				return
			}
			if err := h.options.Store.SetPreference(r.Context(), key, body.Enabled, true); err != nil {
				agentJSON(w, 500, map[string]any{"detail": "MCP preference unavailable"})
				return
			}
		}
		preference, err := h.options.Store.GetPreference(r.Context(), key)
		if err != nil {
			agentJSON(w, 500, map[string]any{"detail": "MCP preference unavailable"})
			return
		}
		agentJSON(w, 200, map[string]any{"agent_id": id, "enabled": !preference.Exists || preference.Value != false})
	case path == "/api/v1/agent/invoke":
		var body struct {
			Capability string         `json:"capability"`
			Input      map[string]any `json:"input"`
		}
		if err := decodeAgent(w, r, &body); err != nil {
			agentError(w, err)
			return
		}
		if body.Capability == "" || len(body.Capability) > 128 {
			agentError(w, errors.New("Invalid capability"))
			return
		}
		result, err := invoke(body.Capability, body.Input)
		if err != nil {
			agentError(w, err)
			return
		}
		agentJSON(w, 200, result)
	case path == "/api/v1/agent/mcp":
		if caller != "" {
			preference, err := h.options.Store.GetPreference(r.Context(), "agent_mcp_enabled:"+caller)
			if err != nil {
				agentJSON(w, 500, map[string]any{"detail": "MCP preference unavailable"})
				return
			}
			if preference.Exists && preference.Value == false {
				agentJSON(w, 403, map[string]any{"detail": "Astrorder MCP has been disabled for this Agent"})
				return
			}
		}
		var body any
		if err := decodeAgent(w, r, &body); err != nil {
			agentError(w, err)
			return
		}
		switch message := body.(type) {
		case map[string]any:
			reply := HandleRPC(message, invoke)
			if reply == nil {
				w.WriteHeader(202)
			} else {
				agentJSON(w, 200, reply)
			}
		case []any:
			replies := []map[string]any{}
			for _, item := range message {
				if object, ok := item.(map[string]any); ok {
					if reply := HandleRPC(object, invoke); reply != nil {
						replies = append(replies, reply)
					}
				}
			}
			agentJSON(w, 200, replies)
		default:
			agentError(w, errors.New("Invalid JSON-RPC"))
		}
	}
}
