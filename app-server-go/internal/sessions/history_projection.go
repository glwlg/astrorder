package sessions

// ProjectNativeHistoryItem shares the browser history presentation contract
// with MCP consumers, including native envelopes, tools and hidden context.
func ProjectNativeHistoryItem(item map[string]any, aid, sid, nativeID, source, agentKind string) []map[string]any {
	return projectHistoryItem(item, aid, sid, nativeID, source, agentKind)
}
