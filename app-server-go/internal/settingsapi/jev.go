package settingsapi

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/browser"
)

func (h *handler) jev(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		jevPatch
		Command string `json:"command"`
		Output  string `json:"output"`
	}
	if err := decode(r, &payload); err != nil {
		fail(w, 422, "Invalid request")
		return
	}
	if r.URL.Path != "/api/v1/services/jev/test" && utf8.RuneCountInString(payload.Output) < 200 {
		writeJSON(w, 200, map[string]any{"ok": true, "filtered": payload.Output, "noise_pruned": false})
		return
	}
	config, err := browser.GetJevConfig(r.Context(), h.Store)
	if err != nil {
		fail(w, 500, "读取 Jev 配置失败")
		return
	}
	var key *string
	if r.URL.Path == "/api/v1/services/jev/test" {
		config, key, err = payload.jevPatch.apply(config)
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
	}
	endpoint, err := browser.ResolveJevConfig(r.Context(), h.Store, config, key)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	planner := &browser.JevPlanner{Client: h.HTTPClient, JevKey: endpoint.APIKey, JevURL: endpoint.URL, JevModel: endpoint.Model}
	if r.URL.Path == "/api/v1/services/jev/test" {
		result, err := planner.InvokeJevHTTP(r.Context(), "Ping health check probe: system metrics and status.", map[string]any{"recommended_component": map[string]any{"type": "choice", "instructions": "Choose the appropriate dashboard component.", "criteria": map[string]string{"StepTimeline": "Sequential stages or milestones", "MetricGrid": "Numerical metrics and counters", "DataTable": "Tabular records", "Checklist": "Verification items", "TestReport": "Test results", "CveSecurityReport": "Security findings", "ApiEndpointsCard": "API routes", "ResourceUsageBar": "Resource capacity", "StatusCard": "High-level health or status"}}})
		if err != nil {
			fail(w, 400, "Jev 连接测试失败："+err.Error())
			return
		}
		answers, _ := result["answers"].(map[string]any)
		answer, _ := answers["recommended_component"].(map[string]any)
		if answer == nil || answer["choice"] == nil {
			fail(w, 502, "Jev 返回了无效的组件判定")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "details": map[string]any{"ok": true, "component": answer["choice"], "confidence": answer["confidence"], "probabilities": answer["probabilities"], "usage": result["usage"]}})
		return
	}
	output := payload.Output
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(output, "\r\n", "\n"), "\n"), "\n")
	sample := []rune(output)
	if len(sample) > 1500 {
		sample = sample[:1500]
	}
	decision, err := planner.InvokeJevHTTP(r.Context(), fmt.Sprintf("Command: %s\nLines: %d\nSample: %s", payload.Command, len(lines), string(sample)), map[string]any{"log_nature": map[string]any{"type": "choice", "instructions": "Classify the overall outcome of this terminal command.", "criteria": map[string]string{"BuildTestFailure": "Compilation error, failed assertions, stack traces, exit non-zero.", "Success": "All tasks succeeded, exit zero, normal operational report.", "VerboseScan": "Long search, list or telemetry dump without critical failure."}}})
	nature := "Unknown"
	if err != nil {
		decision = map[string]any{"fallback": true}
	} else {
		answers, _ := decision["answers"].(map[string]any)
		answer, _ := answers["log_nature"].(map[string]any)
		if value, ok := answer["choice"].(string); ok {
			nature = value
		}
	}
	kept := []string{}
	for i, line := range lines {
		keep := i < 5 || i >= len(lines)-10
		lower := strings.ToLower(line)
		for _, pattern := range []string{"error", "failed", "failure", "exception", "traceback", "fatal", "warn", "assert", "panic", "===", "---", "passed in"} {
			keep = keep || strings.Contains(lower, pattern)
		}
		if keep {
			kept = append(kept, line)
		}
	}
	pruned := strings.Join(kept, "\n")
	reduced := utf8.RuneCountInString(pruned)*4 < utf8.RuneCountInString(output)*3
	if reduced {
		output = fmt.Sprintf("[Jev 降噪过滤: 判定为 %s, 已剔除冗余日志 %d 行]\n%s", nature, len(lines)-len(kept), pruned)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "nature": nature, "filtered": output, "original_lines": len(lines), "filtered_lines": len(kept), "noise_pruned": reduced, "jev_decision": decision})
}
