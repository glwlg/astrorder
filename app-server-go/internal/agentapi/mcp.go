package agentapi

import (
	"fmt"

	"astrorder.dev/app-server/internal/blackboard"
)

// InvokerFunc is the legacy MCP tools/call invoker signature. It is never
// called: every tools/call is rejected because MCP is retired.
type InvokerFunc func(capability string, payload map[string]any) (any, error)

const mcpRetiredMessage = "Astrorder MCP is retired; use the astrorder-agent CLI or the HTTP Agent API"

// HandleRPC processes a single JSON-RPC 2.0 message for the retired MCP
// endpoint. initialize, ping and notifications remain protocol-compatible;
// tools/list is always empty and every tools/call is rejected before the
// invoker can run.
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
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "astrorder", "version": "0.1.0"},
			},
		}

	case "notifications/initialized", "initialized", "notifications/cancelled":
		return nil

	case "tools/list":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      ident,
			"result":  map[string]any{"tools": []any{}},
		}

	case "ping":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      ident,
			"result":  map[string]any{},
		}

	case "tools/call":
		return map[string]any{
			"jsonrpc": "2.0",
			"id":      ident,
			"error": map[string]any{
				"code":    -32000,
				"message": mcpRetiredMessage,
			},
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
