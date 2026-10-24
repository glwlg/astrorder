package projection

// CodexGeneratedImages projects native image generation output without storing
// image bytes or treating arbitrary extension paths as attachments.
func CodexGeneratedImages(item map[string]any, sessionID, itemID string) []any {
	kind, _ := item["kind"].(string)
	if kind != "image_gen.generation" && item["type"] != "imageGeneration" {
		return nil
	}
	file, _ := item["savedPath"].(string)
	if file == "" {
		return nil
	}
	return CodexUserAttachments([]any{map[string]any{"type": "localImage", "path": file}}, sessionID, itemID)
}
