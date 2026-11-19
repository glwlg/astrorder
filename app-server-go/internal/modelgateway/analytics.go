package modelgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/store"
)

func (h *Handler) handleGetAnalyticsConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := GetGatewayConfig(r.Context(), h.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg.ToPublic())
}

func (h *Handler) handleUpdateAnalyticsConfig(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_body", "无效的请求格式")
		return
	}
	updated, err := SaveGatewayConfig(r.Context(), h.store, payload)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated.ToPublic())
}

func (h *Handler) handleGetAnalyticsUsage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rangeVal := q.Get("range")
	if rangeVal == "" {
		rangeVal = "30d"
	}
	surface := q.Get("surface")
	if surface == "" {
		surface = "all"
	}

	validRanges := map[string]bool{"today": true, "all": true, "30d": true, "7d": true}
	if !validRanges[rangeVal] {
		writeError(w, http.StatusUnprocessableEntity, "invalid_range", "统计范围无效")
		return
	}
	validSurfaces := map[string]bool{"all": true, "codex": true, "hermes": true, "claude": true, "grok": true, "antigravity": true}
	if !validSurfaces[surface] {
		writeError(w, http.StatusUnprocessableEntity, "invalid_surface", "Agent 类型无效")
		return
	}
	source := strings.TrimSpace(q.Get("source"))
	if source != "" && source != "all" && source != "direct" && source != "gateway" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_source", "统计来源无效")
		return
	}

	sinceStr := q.Get("since")
	untilStr := q.Get("until")
	if (sinceStr == "") != (untilStr == "") {
		writeError(w, http.StatusUnprocessableEntity, "invalid_range", "自定义时间范围无效")
		return
	}
	if sinceStr != "" && untilStr != "" {
		s, err1 := strconv.ParseInt(sinceStr, 10, 64)
		u, err2 := strconv.ParseInt(untilStr, 10, 64)
		if err1 != nil || err2 != nil || s >= u {
			writeError(w, http.StatusUnprocessableEntity, "invalid_range", "自定义时间范围无效")
			return
		}
	}

	cfg, err := GetGatewayConfig(r.Context(), h.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}

	if source == "direct" {
		direct, dErr := h.collectDirectUsage(r.Context(), cfg, rangeVal, surface, sinceStr, untilStr)
		if dErr != nil {
			writeError(w, http.StatusInternalServerError, "store_error", dErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, direct.toResponse(rangeVal, surface))
		return
	}

	if cfg.GatewayType == "magpie" {
		h.fetchMagpieUsage(w, r, cfg, rangeVal, surface, sinceStr, untilStr)
		return
	}

	var direct *directUsageAggregate
	if source != "gateway" {
		direct, _ = h.collectDirectUsage(r.Context(), cfg, rangeVal, surface, sinceStr, untilStr)
	}

	// OpenCodeX usage
	mgmtURL := strings.TrimRight(cfg.ManagementURL, "/") + "/api/usage"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mgmtURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request_error", err.Error())
		return
	}
	req.URL.RawQuery = r.URL.RawQuery
	if rangeVal == "today" {
		// OpenCodeX exposes per-day model rows in its seven-day response.
		query := req.URL.Query()
		query.Set("range", "7d")
		req.URL.RawQuery = query.Encode()
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		if direct != nil && direct.Totals.totalTokens() > 0 {
			writeJSON(w, http.StatusOK, direct.toResponse(rangeVal, surface))
			return
		}
		writeError(w, http.StatusBadGateway, "upstream_error", fmt.Sprintf("LLM 网关用量请求失败：%v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if direct != nil && direct.Totals.totalTokens() > 0 {
			writeJSON(w, http.StatusOK, direct.toResponse(rangeVal, surface))
			return
		}
		writeError(w, http.StatusBadGateway, "upstream_error", fmt.Sprintf("LLM 网关返回 HTTP %d", resp.StatusCode))
		return
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10_000_000))
	var data map[string]any
	if json.Unmarshal(body, &data) == nil && data != nil && direct != nil && direct.Totals.totalTokens() > 0 {
		mergeDirectIntoGenericResponse(data, direct)
		writeJSON(w, http.StatusOK, data)
		return
	}
	var rawData any
	_ = json.Unmarshal(body, &rawData)
	writeJSON(w, http.StatusOK, rawData)
}

func (h *Handler) fetchMagpieUsage(w http.ResponseWriter, r *http.Request, cfg GatewayConfig, rangeVal, surface, sinceStr, untilStr string) {
	h.serveMagpieUsage(w, r, cfg, rangeVal, surface, sinceStr, untilStr)
}

func (h *Handler) handleGetModelQuota(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		writeError(w, http.StatusBadRequest, "invalid_model", "model required")
		return
	}
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))

	// Direct-account Antigravity models do not carry a nested gateway provider prefix
	// (unlike gateway-proxied `antigravity/<provider>/<slug>`). Prefer live native
	// subscription quota first so direct login users avoid slow or empty gateway lookups.
	trimmedAgy := strings.TrimPrefix(model, "antigravity/")
	if !strings.Contains(trimmedAgy, "/") && h.shouldTryAntigravityNative(r.Context(), model, agentID) {
		if native, ok := h.serveAntigravityNativeModelQuota(r.Context(), model, agentID); ok {
			writeJSON(w, http.StatusOK, native)
			return
		}
	}

	cfg, err := GetGatewayConfig(r.Context(), h.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}

	if cfg.GatewayType == "magpie" {
		h.serveMagpieModelQuota(w, r, cfg, model, agentID)
		return
	}

	var models []map[string]any
	var combos []map[string]any
	var reports []map[string]any
	accounts := []map[string]any{}

	{
		// OpenCodeX
		mgmtURL := strings.TrimRight(cfg.ManagementURL, "/")
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mgmtURL+"/api/models", nil)
		if err == nil {
			if cfg.APIKey != "" {
				req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}
			resp, err := h.httpClient.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 10_000_000))
				resp.Body.Close()
				_ = json.Unmarshal(body, &models)
			}
		}

		combosReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mgmtURL+"/api/combos", nil)
		if err == nil {
			if cfg.APIKey != "" {
				combosReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}
			resp, err := h.httpClient.Do(combosReq)
			if err == nil && resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 10_000_000))
				resp.Body.Close()
				var cPayload map[string]any
				if err := json.Unmarshal(body, &cPayload); err == nil {
					if cList, ok := cPayload["combos"].([]any); ok {
						for _, it := range cList {
							if cm, ok := it.(map[string]any); ok {
								combos = append(combos, cm)
							}
						}
					}
				}
			}
		}

		quotasReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mgmtURL+"/api/provider-quotas", nil)
		if err == nil {
			if cfg.APIKey != "" {
				quotasReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}
			resp, err := h.httpClient.Do(quotasReq)
			if err == nil && resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 10_000_000))
				resp.Body.Close()
				var qPayload map[string]any
				if err := json.Unmarshal(body, &qPayload); err == nil {
					if rList, ok := qPayload["reports"].([]any); ok {
						for _, it := range rList {
							if rm, ok := it.(map[string]any); ok {
								reports = append(reports, rm)
							}
						}
					}
				}
			}
		}
	}

	candidate := model
	if strings.Contains(model, "/") {
		candidate = strings.SplitN(model, "/", 2)[1]
	}

	providers := []string{}
	providerSet := make(map[string]bool)
	for _, row := range models {
		dis, _ := row["disabled"].(bool)
		ns, _ := row["namespaced"].(string)
		id, _ := row["id"].(string)
		if !dis && (ns == model || ns == candidate || id == candidate) {
			if p, ok := row["provider"].(string); ok && p != "" {
				providerSet[p] = true
			}
		}
	}
	for _, c := range combos {
		cModel, _ := c["model"].(string)
		if cModel == model || cModel == candidate {
			if targets, ok := c["targets"].([]any); ok {
				for _, t := range targets {
					if tm, ok := t.(map[string]any); ok {
						if p, ok := tm["provider"].(string); ok && p != "" {
							providerSet[p] = true
						}
					}
				}
			}
		}
	}

	for p := range providerSet {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	selectedReports := []map[string]any{}
	for _, r := range reports {
		rp, _ := r["provider"].(string)
		if providerSet[rp] {
			selectedReports = append(selectedReports, r)
		}
	}

	if len(selectedReports) == 0 && len(accounts) == 0 && h.shouldTryAntigravityNative(r.Context(), model, agentID) {
		if native, ok := h.serveAntigravityNativeModelQuota(r.Context(), model, agentID); ok {
			writeJSON(w, http.StatusOK, native)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"model":     candidate,
		"providers": providers,
		"reports":   selectedReports,
		"accounts":  accounts,
	})
}

func (h *Handler) handleGetSessionUsage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "invalid_path", "session_id required")
		return
	}
	sessionID := parts[3]
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))

	metrics, err := h.store.ListTokenMetrics(r.Context(), agentID, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", "Session usage unavailable")
		return
	}

	needNative := len(metrics) == 0
	if !needNative && metrics[0]["provider"] == "antigravity" {
		cw, _ := strconv.ParseFloat(fmt.Sprint(metrics[0]["context_window"]), 64)
		li, _ := strconv.ParseFloat(fmt.Sprint(metrics[0]["last_input_tokens"]), 64)
		tot, _ := strconv.ParseFloat(fmt.Sprint(metrics[0]["total_tokens"]), 64)
		if (cw <= 0 && li <= 0) || (li > 0 && tot <= 0) || (cw > 0 && li <= 0 && tot <= 0) {
			needNative = true
		}
	}
	if needNative && agentID != "" && h.shouldTryAntigravityNative(r.Context(), "", agentID) {
		if h.fetchAntigravityNativeSessionUsage(r.Context(), agentID, sessionID) {
			if refreshed, rErr := h.store.ListTokenMetrics(r.Context(), agentID, sessionID); rErr == nil {
				metrics = refreshed
			}
		}
	}

	if len(metrics) > 0 {
		result := metrics[0]
		if len(metrics) > 1 && result["provider"] == "antigravity" {
			curTot, _ := strconv.ParseFloat(fmt.Sprint(result["total_tokens"]), 64)
			if curTot <= 0 {
				var best map[string]any
				var bestTot float64
				for _, m := range metrics[1:] {
					if m["provider"] != "antigravity" {
						continue
					}
					if tVal, _ := strconv.ParseFloat(fmt.Sprint(m["total_tokens"]), 64); tVal > bestTot {
						bestTot = tVal
						best = m
					}
				}
				if best != nil {
					for _, k := range []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens", "total_tokens"} {
						result[k] = best[k]
					}
				}
			}
			curLi, _ := strconv.ParseFloat(fmt.Sprint(result["last_input_tokens"]), 64)
			curCw, _ := strconv.ParseFloat(fmt.Sprint(result["context_window"]), 64)
			if curLi <= 0 && curCw > 0 {
				for _, m := range metrics[1:] {
					if liVal, _ := strconv.ParseFloat(fmt.Sprint(m["last_input_tokens"]), 64); liVal > 0 {
						result["last_input_tokens"] = m["last_input_tokens"]
						break
					}
				}
			}
		}
		result["ok"] = true
		number := func(key string) float64 { value, _ := strconv.ParseFloat(fmt.Sprint(result[key]), 64); return value }
		if _, exists := result["context_available"]; !exists {
			if result["provider"] == "antigravity" {
				result["context_available"] = number("context_window") > 0 || number("last_input_tokens") > 0
			} else {
				result["context_available"] = result["last_input_tokens"] != nil
			}
		}
		if result["provider"] == "antigravity" && result["context_available"] == true && result["source"] == nil {
			result["source"] = "native_last_request"
		}
		result["used_percentage"] = nil
		if window := number("context_window"); window > 0 {
			result["used_percentage"] = number("last_input_tokens") * 100 / window
		}
		rate := 0.0
		if input := number("input_tokens"); input > 0 {
			rate = number("cached_tokens") * 100 / input
			if rate < 0 {
				rate = 0
			} else if rate > 100 {
				rate = 100
			}
		}
		result["cache_hit_rate"] = rate
		result["speed"] = nil
		writeJSON(w, http.StatusOK, result)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"context_available": false,
		"session_id":        sessionID,
		"model":             "default",
		"context_window":    nil,
		"last_input_tokens": 0,
		"used_percentage":   nil,
		"total_tokens":      0,
		"input_tokens":      0,
		"output_tokens":     0,
		"cached_tokens":     0,
		"reasoning_tokens":  0,
		"cache_hit_rate":    0,
		"speed":             nil,
	})
}

// ResolverConfig returns gateway configuration resolved for background workers (e.g., audio/browser dictation).
func ResolverConfig(ctx context.Context, st *store.Store) (map[string]any, error) {
	cfg, err := GetGatewayConfig(ctx, st)
	if err != nil {
		return nil, err
	}
	profile := cfg.GatewayProfiles[cfg.GatewayType]
	key := profile.APIKey
	if key == "" {
		key = cfg.APIKey
	}
	if key == "" && cfg.GatewayType == "opencodex" {
		key = os.Getenv("OPENCODEX_API_AUTH_TOKEN")
	}

	return map[string]any{
		"gateway_type":   cfg.GatewayType,
		"inference_url":  profile.InferenceURL,
		"management_url": profile.ManagementURL,
		"api_key":        key,
	}, nil
}
