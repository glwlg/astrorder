package agentapi

import "testing"

func TestMCPNumericAndArrayContracts(t *testing.T) {
	tools := map[string]Tool{}
	for _, tool := range Tools() {
		tools[tool.Name] = tool
	}
	checks := map[string]map[string]string{"browser_run": {"max_steps": "integer", "inputs": "object"}, "sessions_list": {"limit": "integer", "offset": "integer"}, "swarm_lock_acquire": {"ttl_seconds": "integer"}, "sessions_delete": {"keys": "array"}, "monitor_sessions_add": {"keys": "array"}}
	for name, fields := range checks {
		props := tools[name].InputSchema["properties"].(map[string]any)
		for field, kind := range fields {
			p := props[field].(map[string]any)
			if p["type"] != kind {
				t.Errorf("%s.%s type=%v want %s", name, field, p["type"], kind)
			}
			if kind == "array" && p["items"] == nil {
				t.Errorf("%s.%s missing items schema", name, field)
			}
		}
	}
}
