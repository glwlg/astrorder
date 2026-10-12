package modelgateway

import (
	"strings"
	"testing"
)

func TestRenderHermesConfigUpdatesExistingProvider(t *testing.T) {
	existing := `model:
  default: group/luozhi
  provider: magpie
  base_url: "http://192.168.1.100:3425/v1"
providers:
  ocx:
    name: OCX
    base_url: "https://llm.651971564.xyz/v1"
  magpie:
    name: magpie
    base_url: "http://192.168.1.100:3425/v1"
    api_key: "old-key"
`
	bindings := map[string]map[string]string{
		"magpie": {
			"base_url": "http://192.168.1.200:3425/v1",
			"api_key":  "new-key",
		},
	}

	result := RenderHermesConfig(existing, bindings)

	if !strings.Contains(result, `http://192.168.1.200:3425/v1`) {
		t.Fatalf("expected updated base_url in result, got:\n%s", result)
	}
	if !strings.Contains(result, `new-key`) {
		t.Fatalf("expected updated api_key in result, got:\n%s", result)
	}
	// model.base_url should also be updated since model.provider is magpie
	if strings.Contains(result, `base_url: "http://192.168.1.100:3425/v1"`) {
		t.Fatalf("old base_url should not be present, got:\n%s", result)
	}
}

func TestRenderHermesConfigUpdatesModelBaseURL(t *testing.T) {
	existing := `model:
  default: group/luozhi
  provider: magpie
  base_url: "http://192.168.1.100:3425/v1"
providers:
  magpie:
    name: magpie
    base_url: "http://192.168.1.100:3425/v1"
`
	bindings := map[string]map[string]string{
		"magpie": {
			"base_url": "http://10.0.0.1:3425/v1",
			"api_key":  "k-123",
		},
	}

	result := RenderHermesConfig(existing, bindings)
	lines := strings.Split(result, "\n")
	modelBaseURLFound := false
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "base_url:") && strings.Contains(l, "http://10.0.0.1:3425/v1") {
			modelBaseURLFound = true
			break
		}
	}
	if !modelBaseURLFound {
		t.Fatalf("expected model.base_url to be updated, got:\n%s", result)
	}
}

func TestRenderHermesConfigInsertsUnderProvidersNotAtEnd(t *testing.T) {
	existing := `model:
  default: group/luozhi
  provider: magpie
providers:
  ocx:
    name: OCX
    base_url: "https://llm.651971564.xyz/v1"
_left_core_installed:
  - homeassistant
`
	bindings := map[string]map[string]string{
		"magpie": {
			"base_url": "http://192.168.1.100:3425/v1",
			"api_key":  "test-key",
		},
	}

	result := RenderHermesConfig(existing, bindings)

	// Verify magpie is before _left_core_installed
	idxMagpie := strings.Index(result, "magpie:")
	idxLeftCore := strings.Index(result, "_left_core_installed:")
	if idxMagpie == -1 {
		t.Fatalf("magpie not found in result:\n%s", result)
	}
	if idxLeftCore != -1 && idxMagpie > idxLeftCore {
		t.Fatalf("magpie was appended after _left_core_installed! Result:\n%s", result)
	}
}

func TestRenderHermesConfigOpencodexAlias(t *testing.T) {
	existing := `model:
  default: combo/deepseek-v4-flash
  provider: ocx
providers:
  ocx:
    name: OCX
    base_url: "https://old.example.com/v1"
    api_key: "old-key"
`
	// sync might pass "opencodex"
	bindings := map[string]map[string]string{
		"opencodex": {
			"base_url": "https://new.example.com/v1",
			"api_key":  "new-key",
		},
	}

	result := RenderHermesConfig(existing, bindings)
	if !strings.Contains(result, "https://new.example.com/v1") {
		t.Fatalf("expected new URL in ocx alias, got:\n%s", result)
	}
	if strings.Contains(result, "opencodex:") {
		t.Fatalf("should update existing ocx block, not create redundant opencodex block:\n%s", result)
	}
}
