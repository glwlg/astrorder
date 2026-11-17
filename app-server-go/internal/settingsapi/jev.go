package settingsapi

import (
	"net/http"

	"astrorder.dev/app-server/internal/browser"
)

func (h *handler) testJev(w http.ResponseWriter, r *http.Request) {
	var payload jevPatch
	if err := decode(r, &payload); err != nil {
		fail(w, 422, "Invalid request")
		return
	}
	config, err := browser.GetJevConfig(r.Context(), h.Store)
	if err != nil {
		fail(w, 500, "读取 System One 配置失败")
		return
	}
	config, key, err := payload.apply(config)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	endpoint, err := browser.ResolveJevConfig(r.Context(), h.Store, config, key)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	planner := &browser.JevPlanner{Client: h.HTTPClient, JevKey: endpoint.APIKey, JevURL: endpoint.URL, JevModel: endpoint.Model}
	result, err := planner.InvokeJevHTTP(r.Context(), "Ping health check probe: system metrics and status.", map[string]any{"recommended_component": map[string]any{"type": "choice", "instructions": "Choose the appropriate dashboard component.", "criteria": map[string]string{"StepTimeline": "Sequential stages or milestones", "MetricGrid": "Numerical metrics and counters", "DataTable": "Tabular records", "Checklist": "Verification items", "TestReport": "Test results", "CveSecurityReport": "Security findings", "ApiEndpointsCard": "API routes", "ResourceUsageBar": "Resource capacity", "StatusCard": "High-level health or status"}}})
	if err != nil {
		fail(w, 400, "System One 连接测试失败："+err.Error())
		return
	}
	answers, _ := result["answers"].(map[string]any)
	answer, _ := answers["recommended_component"].(map[string]any)
	if answer == nil || answer["choice"] == nil {
		fail(w, 502, "System One 返回了无效的组件判定")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "details": map[string]any{"ok": true, "component": answer["choice"], "confidence": answer["confidence"], "probabilities": answer["probabilities"], "usage": result["usage"]}})
}
