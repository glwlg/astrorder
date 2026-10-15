package agentapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"astrorder.dev/app-server/internal/blackboard"
)

var (
	reBool   = regexp.MustCompile(`\b(boolean|bool)\b`)
	reInt    = regexp.MustCompile(`\b(integer|int|percentage)\b`)
	reNum    = regexp.MustCompile(`\b(number|float)\b`)
	reArray  = regexp.MustCompile(`\b(list|array)\b`)
	reObject = regexp.MustCompile(`\b(object|dict|key-value)\b`)
)

// ToolName converts capability ID to MCP tool name (replacing '.' with '_').
func ToolName(capabilityID string) string {
	return strings.ReplaceAll(capabilityID, ".", "_")
}

// CapabilityID maps MCP tool name back to capability ID.
func CapabilityID(name string) string {
	for _, item := range Capabilities {
		if ToolName(item.ID) == name {
			return item.ID
		}
	}
	return strings.ReplaceAll(name, "_", ".")
}

func inferSchemaProperty(desc string) map[string]any {
	text := strings.ToLower(desc)
	prop := map[string]any{"description": desc}
	if strings.Contains(text, "string, object, array, or number") || strings.Contains(text, "data payload") {
		return prop
	}
	if reBool.MatchString(text) {
		prop["type"] = "boolean"
	} else if reInt.MatchString(text) {
		prop["type"] = "integer"
	} else if reNum.MatchString(text) {
		prop["type"] = "number"
	} else if reArray.MatchString(text) {
		prop["type"] = "array"
		prop["items"] = map[string]any{"type": "string"}
	} else if reObject.MatchString(text) {
		prop["type"] = "object"
	} else {
		prop["type"] = "string"
	}
	return prop
}

// BuildInputSchema builds an MCP inputSchema matching Python agents/mcp.py.
func BuildInputSchema(rawInput map[string]any) map[string]any {
	if len(rawInput) == 0 {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	properties := make(map[string]any, len(rawInput))
	var required []string

	for key, descVal := range rawInput {
		descStr := fmt.Sprintf("%v", descVal)
		properties[key] = inferSchemaProperty(descStr)

		descLow := strings.ToLower(descStr)
		isOptional := false
		for _, opt := range []string{"optional", "or omit", "defaults to", "default:", "default automatically"} {
			if strings.Contains(descLow, opt) {
				isOptional = true
				break
			}
		}
		if !isOptional {
			required = append(required, key)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// Tool describes an MCP tool.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Tools returns the list of 40 generated MCP tools.
func Tools() []Tool {
	result := make([]Tool, len(Capabilities))
	for i, item := range Capabilities {
		result[i] = Tool{
			Name:        ToolName(item.ID),
			Description: item.Summary,
			InputSchema: capabilitySchema(item),
		}
	}
	return result
}

func capabilitySchema(item Capability) map[string]any {
	schema := BuildInputSchema(item.Input)
	props := schema["properties"].(map[string]any)
	for _, key := range []string{"limit", "offset", "max_steps", "ttl_seconds", "columns", "progress"} {
		if prop, ok := props[key].(map[string]any); ok {
			prop["type"] = "integer"
		}
	}
	return schema
}

// InvokerFunc is called to execute a capability during MCP tools/call.
type InvokerFunc func(capability string, payload map[string]any) (any, error)

// HandleRPC processes a single JSON-RPC 2.0 message.
func HandleRPC(message map[string]any, invoker InvokerFunc) map[string]any {
	method, _ := message["method"].(string)
	ident := message["id"]

	switch method {
	case "initialize":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      ident,
			"result": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "astrorder", "version": "0.1.0"},
			},
		}

	case "notifications/initialized", "initialized", "notifications/cancelled":
		return nil

	case "tools/list":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      ident,
			"result":  map[string]any{"tools": Tools()},
		}

	case "ping":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      ident,
			"result":  map[string]any{},
		}

	case "tools/call":
		params, _ := message["params"].(map[string]any)
		if params == nil {
			params = map[string]any{}
		}
		name, _ := params["name"].(string)
		arguments, _ := params["arguments"].(map[string]any)
		if arguments == nil {
			arguments = map[string]any{}
		}

		capID := CapabilityID(name)
		data, err := invoker(capID, arguments)
		if err != nil {
			msg := err.Error()
			if apiErr, ok := err.(*AgentApiError); ok {
				msg = apiErr.Message
			}
			return map[string]any{
				"jsonrpc": "2.0",
				"id":      ident,
				"error": map[string]any{
					"code":    -32000,
					"message": msg,
				},
			}
		}

		// Convert result to map for screenshot handling
		var payload map[string]any
		if m, ok := data.(map[string]any); ok {
			payload = make(map[string]any, len(m))
			for k, v := range m {
				payload[k] = v
			}
		} else {
			rawJSON, _ := json.Marshal(data)
			_ = json.Unmarshal(rawJSON, &payload)
			if payload == nil {
				payload = map[string]any{"result": data}
			}
		}

		screenshot, _ := payload["screenshot"].(string)
		delete(payload, "screenshot")
		mimeType, _ := payload["mime_type"].(string)
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		delete(payload, "mime_type")

		jsonBytes, _ := json.Marshal(payload)
		content := []map[string]any{
			{"type": "text", "text": string(jsonBytes)},
		}
		if screenshot != "" {
			content = append(content, map[string]any{
				"type":     "image",
				"data":     screenshot,
				"mimeType": mimeType,
			})
		}

		return map[string]any{
			"jsonrpc": "2.0",
			"id":      ident,
			"result":  map[string]any{"content": content},
		}

	default:
		if ident == nil {
			return nil
		}
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      ident,
			"error": map[string]any{
				"code":    -32601,
				"message": fmt.Sprintf("Unknown method %s", method),
			},
		}
	}
}

// BlackboardCatalog returns the static component catalog items.
func BlackboardCatalog() []blackboard.ComponentInfo {
	return blackboard.Catalog
}
