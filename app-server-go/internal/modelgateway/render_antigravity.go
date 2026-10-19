package modelgateway

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RenderAntigravityConfig updates the CLI gateway settings without replacing
// unrelated user settings. Gateway model IDs remain exact, including slashes.
func RenderAntigravityConfig(existing string, catalog *Catalog, inferenceURL, apiKey string) (string, error) {
	existing = strings.TrimPrefix(existing, "\ufeff")
	doc := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &doc); err != nil || doc == nil {
			return "", fmt.Errorf("invalid Antigravity settings JSON")
		}
	}
	object := func(key string) (map[string]any, error) {
		if value, ok := doc[key]; ok {
			m, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid Antigravity %s settings", key)
			}
			return m, nil
		}
		m := map[string]any{}
		doc[key] = m
		return m, nil
	}
	gateway, err := object("gateway")
	if err != nil {
		return "", err
	}
	gateway["enabled"] = true
	gateway["url"] = strings.TrimRight(inferenceURL, "/")
	gateway["apiKey"] = apiKey
	gateway["wireProtocol"] = "openai"
	models, err := object("models")
	if err != nil {
		return "", err
	}
	list := make([]map[string]any, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		supportsImages := false
		for _, modality := range model.InputModalities {
			if modality == "image" {
				supportsImages = true
			}
		}
		list = append(list, map[string]any{"modelId": model.Slug, "displayName": model.Slug, "supportsImages": supportsImages})
	}
	models["list"] = list
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw) + "\n", nil
}
