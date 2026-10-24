package agentapi

// Tool is a legacy compatibility type. MCP no longer exports tools.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Tools returns an empty slice. The MCP tool surface is retired; HTTP
// capabilities remain the supported catalog.
func Tools() []Tool {
	return []Tool{}
}
