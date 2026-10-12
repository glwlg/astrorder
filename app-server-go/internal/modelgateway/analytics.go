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

	validRanges := map[string]bool{"all": true, "30d": true, "7d": true}
	if !validRanges[rangeVal] {
		writeError(w, http.StatusUnprocessableEntity, "invalid_range", "统计范围无效")
		return
	}
	validSurfaces := map[string]bool{"all": true, "codex": true, "claude": true, "grok": true}
	if !validSurfaces[surface] {
		writeError(w, http.StatusUnprocessableEntity, "invalid_surface", "Agent 类型无效")
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

	if cfg.GatewayType == "magpie" {
		h.fetchMagpieUsage(w, r, cfg, rangeVal, surface, sinceStr, untilStr)
		return
	}

	// OpenCodeX usage
	mgmtURL := strings.TrimRight(cfg.ManagementURL, "/") + "/api/usage"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mgmtURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request_error", err.Error())
		return
	}
	req.URL.RawQuery = r.URL.RawQuery
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", fmt.Sprintf("LLM 网关用量请求失败：%v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "upstream_error", fmt.Sprintf("LLM 网关返回 HTTP %d", resp.StatusCode))
		return
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10_000_000))
	var data any
	_ = json.Unmarshal(body, &data)
	writeJSON(w, http.StatusOK, data)
}

func (h *Handler) fetchMagpieUsage(w http.ResponseWriter, r *http.Request, cfg GatewayConfig, rangeVal, surface, sinceStr, untilStr string) {
	if cfg.ManagementKey == "" {
		writeError(w, http.StatusUnprocessableEntity, "missing_key", "未配置 Magpie 管理端访问参数")
		return
	}

	mgmtURL := strings.TrimRight(cfg.ManagementURL, "/") + "/api/usage/requests"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mgmtURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request_error", err.Error())
		return
	}
	q := req.URL.Query()
	q.Set("period", rangeVal)
	q.Set("limit", "1")
	if surface != "all" {
		q.Set("agent", surface)
	}
	q.Set("k", cfg.ManagementKey)
	req.URL.RawQuery = q.Encode()

	resp, err := h.httpClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", fmt.Sprintf("Magpie 用量请求失败：%v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10_000_000))
	var data any
	_ = json.Unmarshal(body, &data)
	writeJSON(w, http.StatusOK, data)
}

func (h *Handler) handleGetModelQuota(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		writeError(w, http.StatusBadRequest, "invalid_model", "model required")
		return
	}

	cfg, err := GetGatewayConfig(r.Context(), h.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}

	var models []map[string]any
	var combos []map[string]any
	var reports []map[string]any
	accounts := []map[string]any{}

	if cfg.GatewayType == "magpie" {
		inferenceURL := strings.TrimRight(cfg.InferenceURL, "/")
		modelsURL := inferenceURL + "/models"
		if strings.HasSuffix(inferenceURL, "/models") {
			modelsURL = inferenceURL
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, modelsURL, nil)
		if err == nil {
			if cfg.APIKey != "" {
				req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}
			resp, err := h.httpClient.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 10_000_000))
				resp.Body.Close()
				var raw any
				if err := json.Unmarshal(body, &raw); err == nil {
					var rawList []any
					if m, ok := raw.(map[string]any); ok {
						if d, ok := m["data"].([]any); ok {
							rawList = d
						}
					} else if l, ok := raw.([]any); ok {
						rawList = l
					}
					for _, item := range rawList {
						if rm, ok := item.(map[string]any); ok {
							mid, _ := rm["id"].(string)
							provider, _ := rm["owned_by"].(string)
							if provider == "" {
								if strings.Contains(mid, "/") {
									provider = strings.Split(mid, "/")[0]
								} else {
									provider = "magpie"
								}
							}
							models = append(models, map[string]any{
								"id":         mid,
								"namespaced": mid,
								"provider":   provider,
								"disabled":   false,
							})
						}
					}
				}
			}
		}
	} else {
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

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
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
