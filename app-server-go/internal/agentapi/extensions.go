package agentapi

import (
	"context"
	"strings"
	"unicode"
)

// ExtensionCapability describes an agent capability exposed by an enabled extension/plugin.
type ExtensionCapability struct {
	PluginID       string         `json:"plugin_id"`
	PluginName     string         `json:"plugin_name"`
	CapabilityID   string         `json:"capability_id"`
	FullID         string         `json:"id"`
	CLIAlias       []string       `json:"cli_alias,omitempty"`
	Summary        string         `json:"summary"`
	InputSchema    map[string]any `json:"input_schema,omitempty"`
	RequiresCaller bool           `json:"requires_caller,omitempty"`
}

// ExtensionDispatcher decouples agentapi from pluginhost without circular imports.
type ExtensionDispatcher interface {
	ListAgentCapabilities(ctx context.Context) []ExtensionCapability
	InvokeAgentCapability(ctx context.Context, pluginID, capID string, payload map[string]any) (any, error)
	ResolveCLIAlias(ctx context.Context, alias []string) (pluginID, capID string, ok bool)
}

// SetExtensions wires an ExtensionDispatcher onto an initialized Handler.
func (h *Handler) SetExtensions(d ExtensionDispatcher) {
	if h == nil {
		return
	}
	h.options.Extensions = d
}

// buildCatalog returns the static built-in capabilities plus dynamic capabilities
// exposed by currently enabled extensions.
func (h *Handler) buildCatalog(ctx context.Context) []Capability {
	items := make([]Capability, len(AllCapabilities))
	copy(items, AllCapabilities)

	if h.options.Extensions == nil {
		return items
	}

	items = append(items, Capability{
		ID:      "ext.list",
		Summary: "List agent capabilities exposed by currently enabled Astrorder extensions and plugins.",
		Input:   map[string]any{},
	})

	extCaps := h.options.Extensions.ListAgentCapabilities(ctx)
	for _, c := range extCaps {
		fullID := strings.TrimSpace(c.FullID)
		if fullID == "" {
			pid := strings.TrimSpace(c.PluginID)
			cid := strings.TrimSpace(c.CapabilityID)
			if pid == "" || cid == "" {
				continue
			}
			fullID = "ext." + pid + "." + cid
		}
		input := c.InputSchema
		if input == nil {
			input = map[string]any{}
		}
		items = append(items, Capability{
			ID:      fullID,
			Summary: c.Summary,
			Input:   input,
		})
	}
	return items
}

// parseExtensionCapabilityID splits "ext.<plugin_id>.<cap_id>" on the LAST dot
// so dotted plugin IDs such as "com.acme.foo" resolve cleanly.
func parseExtensionCapabilityID(capability string) (pluginID, capID string, ok bool) {
	if !strings.HasPrefix(capability, "ext.") {
		return "", "", false
	}
	rest := strings.TrimPrefix(capability, "ext.")
	idx := strings.LastIndexByte(rest, '.')
	if idx <= 0 || idx >= len(rest)-1 {
		return "", "", false
	}
	pluginID = strings.TrimSpace(rest[:idx])
	capID = strings.TrimSpace(rest[idx+1:])
	if pluginID == "" || capID == "" {
		return "", "", false
	}
	if strings.IndexFunc(pluginID, unicode.IsSpace) >= 0 || strings.IndexFunc(capID, unicode.IsSpace) >= 0 {
		return "", "", false
	}
	return pluginID, capID, true
}

// enrichExtensionPayload clones payload and populates workspace, connection_id, and session_id
// from the verified caller session when those fields were omitted.
func (h *Handler) enrichExtensionPayload(ctx context.Context, payload map[string]any) map[string]any {
	enriched := make(map[string]any, len(payload)+4)
	for k, v := range payload {
		enriched[k] = v
	}

	callerAID, _ := payload["caller_agent_id"].(string)
	callerSID, _ := payload["caller_session_id"].(string)
	callerConn, _ := payload["caller_connection_id"].(string)

	wsStr, _ := enriched["workspace"].(string)
	connStr, _ := enriched["connection_id"].(string)
	sessStr, _ := enriched["session_id"].(string)
	if strings.TrimSpace(sessStr) == "" && strings.TrimSpace(callerSID) != "" {
		enriched["session_id"] = strings.TrimSpace(callerSID)
	}

	if callerAID != "" && callerSID != "" && h.options.Store != nil {
		if sess, err := h.options.Store.GetSession(ctx, callerAID, callerSID); err == nil && sess != nil {
			if strings.TrimSpace(wsStr) == "" {
				if sws, ok := sess["workspace"].(string); ok && strings.TrimSpace(sws) != "" {
					enriched["workspace"] = strings.TrimSpace(sws)
				}
			}
			if strings.TrimSpace(connStr) == "" {
				if cid, ok := sess["connection_id"].(string); ok && strings.TrimSpace(cid) != "" {
					enriched["connection_id"] = strings.TrimSpace(cid)
				}
			}
		}
	}

	if cid, _ := enriched["connection_id"].(string); strings.TrimSpace(cid) == "" && strings.TrimSpace(callerConn) != "" && !strings.EqualFold(strings.TrimSpace(callerConn), "local") {
		enriched["connection_id"] = strings.TrimSpace(callerConn)
	}
	if ws, _ := enriched["workspace"].(string); strings.TrimSpace(ws) == "" && strings.TrimSpace(h.options.DefaultWorkspace) != "" {
		// Only fallback to DefaultWorkspace when no explicit project was specified.
		proj, _ := enriched["project"].(string)
		projPath, _ := enriched["project_path"].(string)
		if strings.TrimSpace(proj) == "" && strings.TrimSpace(projPath) == "" {
			enriched["workspace"] = strings.TrimSpace(h.options.DefaultWorkspace)
		}
	}
	return enriched
}

func (h *Handler) invokeExtList(ctx context.Context) (any, error) {
	if h.options.Extensions == nil {
		return map[string]any{"items": []ExtensionCapability{}}, nil
	}
	items := h.options.Extensions.ListAgentCapabilities(ctx)
	if items == nil {
		items = []ExtensionCapability{}
	}
	return map[string]any{"items": items}, nil
}

func (h *Handler) invokeExtensionCapability(ctx context.Context, capability string, payload map[string]any) (any, error) {
	pluginID, capID, ok := parseExtensionCapabilityID(capability)
	if !ok {
		return nil, NewAgentApiError("invalid_input", "Invalid extension capability id; expected ext.<plugin_id>.<capability_id>")
	}
	if h.options.Extensions == nil {
		return nil, NewAgentApiError("unavailable", "Extension host is not configured")
	}
	return h.options.Extensions.InvokeAgentCapability(ctx, pluginID, capID, h.enrichExtensionPayload(ctx, payload))
}
