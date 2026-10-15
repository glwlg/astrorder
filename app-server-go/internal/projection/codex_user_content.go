package projection

import "strings"

// Codex userMessage snapshots carry UserInput blocks rather than a top-level text.
func codexUserContentText(content any) string {
	parts, _ := content.([]any)
	var text strings.Builder
	for _, part := range parts {
		block, ok := part.(map[string]any)
		if !ok || block["type"] != "text" {
			continue
		}
		value, _ := block["text"].(string)
		text.WriteString(value)
	}
	return text.String()
}
