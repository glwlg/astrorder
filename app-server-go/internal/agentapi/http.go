package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"astrorder.dev/app-server/internal/blackboard"
)

func (h *Handler) Invoke(ctx context.Context, capability string, payload map[string]any) (any, error) {
	if payload == nil {
		payload = map[string]any{}
	}

	// If caller is provided, parse and validate it, and inject verified caller metadata
	if rawCaller, hasCaller := payload["caller"]; hasCaller {
		if rawCaller == nil {
			return nil, NewAgentApiError("invalid_caller", "caller cannot be null")
		}
		caller, err := ParseCaller(rawCaller)
		if err != nil {
			return nil, err
		}
		if _, vErr := h.ValidateCaller(ctx, caller); vErr != nil {
			return nil, vErr
		}

		// Check all 5 caller_* fields if already present in payload: reject null, non-string, empty, or conflict
		checkCallerField := func(fieldName, expected string) error {
			if rawVal, exists := payload[fieldName]; exists {
				if rawVal == nil {
					return NewAgentApiError("invalid_caller", fmt.Sprintf("%s cannot be null", fieldName))
				}
				strVal, ok := rawVal.(string)
				if !ok || strVal == "" {
					return NewAgentApiError("invalid_caller", fmt.Sprintf("%s must be a non-empty string", fieldName))
				}
				if strVal != expected {
					return NewAgentApiError("invalid_caller", fmt.Sprintf("%s conflicts with caller (%s != %s)", fieldName, strVal, expected))
				}
			}
			return nil
		}

		if err := checkCallerField("caller_agent_id", caller.AgentID); err != nil {
			return nil, err
		}
		if err := checkCallerField("caller_session_id", caller.SessionID); err != nil {
			return nil, err
		}
		if err := checkCallerField("caller_connection_id", caller.ConnectionID); err != nil {
			return nil, err
		}
		if err := checkCallerField("caller_profile_name", caller.ProfileName); err != nil {
			return nil, err
		}
		if err := checkCallerField("caller_runtime", caller.Runtime); err != nil {
			return nil, err
		}

		// Check session_key if present: must not be null/empty/non-string
		callerSK := caller.AgentID + "::" + caller.SessionID
		if rawSK, exists := payload["session_key"]; exists {
			if rawSK == nil {
				return nil, NewAgentApiError("invalid_input", "session_key cannot be null")
			}
			skStr, ok := rawSK.(string)
			if !ok || skStr == "" {
				return nil, NewAgentApiError("invalid_input", "session_key must be a non-empty string")
			}
			if strings.Contains(skStr, "::") && !strings.HasPrefix(skStr, "group::") && !strings.HasPrefix(skStr, "swarm::") {
				if skStr != callerSK {
					return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("session_key '%s' conflicts with caller '%s'", skStr, callerSK))
				}
			}
		}

		payload["caller_agent_id"] = caller.AgentID
		payload["caller_session_id"] = caller.SessionID
		payload["caller_connection_id"] = caller.ConnectionID
		payload["caller_profile_name"] = caller.ProfileName
		payload["caller_runtime"] = caller.Runtime

		// Distinguish collaboration capabilities (blackboard/telemetry/swarm) from management commands
		isBlackboard := strings.HasPrefix(capability, "blackboard.")
		isSwarm := strings.HasPrefix(capability, "swarm.")

		if isBlackboard {
			// Only default session_key = callerSK when namespace, session_key, group_id, and parent_key are ALL absent
			hasNS := payload["namespace"] != nil && payload["namespace"] != ""
			hasSK := payload["session_key"] != nil && payload["session_key"] != ""
			hasGroup := payload["group_id"] != nil && payload["group_id"] != ""
			hasParent := payload["parent_key"] != nil && payload["parent_key"] != ""
			if !hasNS && !hasSK && !hasGroup && !hasParent {
				payload["session_key"] = callerSK
			}
			// Note: blackboard payload["key"] is dictionary data key (e.g. 'dataset::row'), NEVER check against callerSK!
		} else if isSwarm {
			// In swarm.telemetry.report, "key" is the worker reporting key
			if capability == "swarm.telemetry.report" {
				if rawKey, ok := payload["key"].(string); ok && rawKey != "" {
					if rawKey != callerSK {
						return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("swarm.telemetry.report key '%s' conflicts with caller '%s'", rawKey, callerSK))
					}
				} else {
					payload["key"] = callerSK
				}
			}
			// In swarm.telemetry.get, caller CAN inspect other workers' telemetry, do not restrict key != callerSK
			if capability == "swarm.telemetry.get" {
				if rawKey, ok := payload["key"].(string); !ok || rawKey == "" {
					payload["key"] = callerSK
				}
			}
			// Lock ownership and SOS worker identity are bound to the verified
			// caller unless an explicit value is supplied. Conflicting explicit
			// values are rejected so one agent cannot act as another.
			switch capability {
			case "swarm.lock.acquire", "swarm.lock.release":
				if rawOwner, ok := payload["owner_key"].(string); ok && strings.TrimSpace(rawOwner) != "" {
					if rawOwner != callerSK {
						return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("%s owner_key '%s' conflicts with caller '%s'", capability, rawOwner, callerSK))
					}
				} else {
					payload["owner_key"] = callerSK
				}
			case "swarm.sos.escalate":
				if rawKey, ok := payload["key"].(string); ok && strings.TrimSpace(rawKey) != "" {
					if rawKey != callerSK {
						return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("swarm.sos.escalate key '%s' conflicts with caller '%s'", rawKey, callerSK))
					}
				} else {
					payload["key"] = callerSK
				}
			}
		}

		if isBlackboard || isSwarm {
			if aid, ok := payload["agent_id"].(string); ok && aid != "" && aid != caller.AgentID {
				return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("agent_id '%s' conflicts with caller agent '%s'", aid, caller.AgentID))
			}
			if sid, ok := payload["session_id"].(string); ok && sid != "" && sid != caller.SessionID {
				return nil, NewAgentApiError("invalid_caller", fmt.Sprintf("session_id '%s' conflicts with caller session '%s'", sid, caller.SessionID))
			}
		}
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
	case "sessions.delegate":
		return h.invokeSessionsDelegate(ctx, payload)
	case "sessions.operation":
		return h.invokeSessionsOperation(ctx, payload)
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
	// Retired MCP endpoints answer 410 before authentication so old clients get
	// an unambiguous retirement signal instead of a misleading auth error.
	if path == "/api/v1/agent/mcp" || (strings.HasPrefix(path, "/api/v1/agents/") && strings.HasSuffix(path, "/mcp")) {
		agentJSON(w, http.StatusGone, map[string]any{"detail": mcpRetiredMessage})
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
		agentJSON(w, 200, map[string]any{"items": AllCapabilities})
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
	}
}
