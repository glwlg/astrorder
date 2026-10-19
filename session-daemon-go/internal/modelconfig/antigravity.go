package modelconfig

import (
	"encoding/json"
	"net/url"
	"strings"
)

func validateAntigravityConfig(data []byte) error {
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil || doc == nil {
		return newProtocolErrorf("invalid Antigravity config JSON")
	}
	if raw, exists := doc["gateway"]; exists {
		gateway, ok := raw.(map[string]any)
		if !ok {
			return newProtocolErrorf("invalid Antigravity gateway")
		}
		if gateway["enabled"] == true {
			endpoint, _ := gateway["url"].(string)
			u, err := url.Parse(endpoint)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || gateway["wireProtocol"] != "openai" {
				return newProtocolErrorf("invalid Antigravity gateway binding")
			}
		}
	}
	if raw, exists := doc["models"]; exists {
		models, ok := raw.(map[string]any)
		if !ok {
			return newProtocolErrorf("invalid Antigravity models")
		}
		if raw, exists := models["list"]; exists {
			list, ok := raw.([]any)
			if !ok {
				return newProtocolErrorf("invalid Antigravity model list")
			}
			seen := map[string]bool{}
			for _, item := range list {
				model, ok := item.(map[string]any)
				id, _ := model["modelId"].(string)
				if !ok || strings.TrimSpace(id) == "" || seen[id] {
					return newProtocolErrorf("invalid Antigravity model identity")
				}
				seen[id] = true
			}
		}
	}
	return nil
}
