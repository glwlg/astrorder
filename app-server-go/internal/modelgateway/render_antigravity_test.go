package modelgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderAntigravityPreservesSettingsAndReplacesGatewayCatalog(t *testing.T) {
	catalog := &Catalog{Models: []NormalizedModel{{Slug: "group/luozhi", ContextWindow: 128000, InputModalities: []string{"text", "image"}}}}
	rendered, err := RenderAntigravityConfig(`{"keep":{"value":true},"gateway":{"headers":{"X-Test":"kept"}},"models":{"list":[{"modelId":"removed"}]}}`, catalog, "http://localhost:3425/v1", "fixture-secret")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["keep"].(map[string]any)["value"] != true {
		t.Fatal("unrelated settings lost")
	}
	gw := doc["gateway"].(map[string]any)
	if gw["url"] != "http://localhost:3425/v1" || gw["wireProtocol"] != "openai" || gw["apiKey"] != "fixture-secret" || gw["enabled"] != true {
		t.Fatal("gateway binding incorrect")
	}
	if gw["headers"].(map[string]any)["X-Test"] != "kept" {
		t.Fatal("gateway headers lost")
	}
	models := doc["models"].(map[string]any)["list"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["modelId"] != "group/luozhi" {
		t.Fatal("gateway model identities not preserved")
	}
	if models[0].(map[string]any)["supportsImages"] != true {
		t.Fatal("native image capability was lost")
	}
	if strings.Contains(unifiedDiffText("", rendered, "antigravity_config"), "fixture-secret") {
		t.Fatal("preview leaks gateway key")
	}
	if _, err = RenderAntigravityConfig("{invalid", catalog, "http://localhost/v1", ""); err == nil {
		t.Fatal("invalid existing settings must fail")
	}
}
