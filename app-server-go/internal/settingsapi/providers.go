package settingsapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/llmcompat"
)

const providersBasePath = "/api/v1/services/providers"

func matchesProviders(method, path string) bool {
	clean := strings.TrimRight(path, "/")
	switch clean {
	case providersBasePath:
		return method == http.MethodGet || method == http.MethodPost
	case providersBasePath + "/default":
		return method == http.MethodPut || method == http.MethodPost
	case providersBasePath + "/models":
		return method == http.MethodPost
	case providersBasePath + "/test":
		return method == http.MethodPost
	}
	if strings.HasPrefix(clean, providersBasePath+"/") {
		rest := strings.TrimPrefix(clean, providersBasePath+"/")
		if rest != "" && !strings.Contains(rest, "/") {
			return method == http.MethodDelete
		}
	}
	return false
}

func (h *handler) serveProviders(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimRight(r.URL.Path, "/")
	switch clean {
	case providersBasePath:
		if r.Method == http.MethodGet {
			h.handleGetProviders(w, r)
		} else {
			h.handleUpsertProvider(w, r)
		}
	case providersBasePath + "/default":
		h.handleSetDefaultProvider(w, r)
	case providersBasePath + "/models":
		h.handleFetchProviderModels(w, r)
	case providersBasePath + "/test":
		h.handleTestProvider(w, r)
	default:
		if strings.HasPrefix(clean, providersBasePath+"/") && r.Method == http.MethodDelete {
			id := strings.TrimPrefix(clean, providersBasePath+"/")
			h.handleDeleteProvider(w, r, id)
			return
		}
		fail(w, http.StatusNotFound, "Not Found")
	}
}

func (h *handler) handleGetProviders(w http.ResponseWriter, r *http.Request) {
	cfg, err := aiprovider.LoadConfig(r.Context(), h.Store)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取 AI 供应商配置失败")
		return
	}
	writeJSON(w, http.StatusOK, cfg.ToPublic())
}

func (h *handler) handleUpsertProvider(w http.ResponseWriter, r *http.Request) {
	var patch aiprovider.ProviderPatch
	if err := decode(r, &patch); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	cfg, saved, err := aiprovider.UpsertProvider(r.Context(), h.Store, patch)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	pub := cfg.ToPublic()
	var savedPub *aiprovider.PublicProvider
	for i := range pub.Providers {
		if pub.Providers[i].ID == saved.ID {
			savedPub = &pub.Providers[i]
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":   pub,
		"provider": savedPub,
	})
}

func (h *handler) handleDeleteProvider(w http.ResponseWriter, r *http.Request, id string) {
	cfg, err := aiprovider.DeleteProvider(r.Context(), h.Store, id)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg.ToPublic())
}

func (h *handler) handleSetDefaultProvider(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ProviderID string `json:"provider_id"`
		Model      string `json:"model"`
		Reasoning  string `json:"reasoning"`
	}
	if err := decode(r, &payload); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	cfg, err := aiprovider.SetDefault(r.Context(), h.Store, payload.ProviderID, payload.Model, payload.Reasoning)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg.ToPublic())
}

func (h *handler) handleFetchProviderModels(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ProviderID string `json:"provider_id"`
		Protocol   string `json:"protocol"`
		BaseURL    string `json:"base_url"`
		APIKey     string `json:"api_key"`
		AgentID    string `json:"agent_id"`
	}
	if err := decode(r, &payload); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}

	proto := strings.TrimSpace(payload.Protocol)
	baseURL := strings.TrimRight(strings.TrimSpace(payload.BaseURL), "/")
	apiKey := strings.TrimSpace(payload.APIKey)
	agentID := strings.TrimSpace(payload.AgentID)

	if pid := strings.TrimSpace(payload.ProviderID); pid != "" {
		if saved, ok := aiprovider.FindProvider(r.Context(), h.Store, pid); ok {
			if proto == "" {
				proto = saved.Protocol
			}
			if baseURL == "" {
				baseURL = saved.BaseURL
			}
			if apiKey == "" {
				apiKey = saved.APIKey
			}
			if agentID == "" {
				agentID = saved.AgentID
			}
		}
	}
	proto = llmcompat.NormalizeProtocol(proto)

	if proto == llmcompat.ProtocolAgent {
		if agentID == "" {
			fail(w, http.StatusUnprocessableEntity, "请先选择要对接的 Agent")
			return
		}
		if h.ListAgentModels == nil {
			fail(w, http.StatusServiceUnavailable, "Agent 模型列表服务暂不可用")
			return
		}
		models, err := h.ListAgentModels(r.Context(), agentID)
		if err != nil {
			fail(w, http.StatusBadRequest, "读取 Agent 可用模型失败："+err.Error())
			return
		}
		if pid := strings.TrimSpace(payload.ProviderID); pid != "" && len(models) > 0 {
			_, _ = aiprovider.UpdateProviderModels(r.Context(), h.Store, pid, models)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"models": models,
			"count":  len(models),
		})
		return
	}

	if parsed, err := url.Parse(baseURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		fail(w, http.StatusUnprocessableEntity, "接口地址 (Base URL) 必须是完整的 http 或 https 地址")
		return
	}

	models, err := llmcompat.ListModels(r.Context(), h.HTTPClient, proto, baseURL, apiKey)
	if err != nil {
		fail(w, http.StatusBadRequest, "拉取模型列表失败："+err.Error())
		return
	}

	// If fetching for an existing saved provider, cache the fetched model list automatically.
	if pid := strings.TrimSpace(payload.ProviderID); pid != "" && len(models) > 0 {
		_, _ = aiprovider.UpdateProviderModels(r.Context(), h.Store, pid, models)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"models": models,
		"count":  len(models),
	})
}

func (h *handler) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var payload struct {
		ProviderID string `json:"provider_id"`
		Protocol   string `json:"protocol"`
		BaseURL    string `json:"base_url"`
		APIKey     string `json:"api_key"`
		AgentID    string `json:"agent_id"`
		Model      string `json:"model"`
		Reasoning  string `json:"reasoning"`
	}
	if err := decode(r, &payload); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}

	proto := strings.TrimSpace(payload.Protocol)
	baseURL := strings.TrimRight(strings.TrimSpace(payload.BaseURL), "/")
	apiKey := strings.TrimSpace(payload.APIKey)
	agentID := strings.TrimSpace(payload.AgentID)
	model := strings.TrimSpace(payload.Model)

	if pid := strings.TrimSpace(payload.ProviderID); pid != "" {
		if saved, ok := aiprovider.FindProvider(r.Context(), h.Store, pid); ok {
			if proto == "" {
				proto = saved.Protocol
			}
			if baseURL == "" {
				baseURL = saved.BaseURL
			}
			if apiKey == "" {
				apiKey = saved.APIKey
			}
			if agentID == "" {
				agentID = saved.AgentID
			}
			if model == "" && len(saved.Models) > 0 {
				model = saved.Models[0]
			}
		}
	}
	proto = llmcompat.NormalizeProtocol(proto)

	if proto == llmcompat.ProtocolAgent {
		if agentID == "" {
			fail(w, http.StatusUnprocessableEntity, "请先选择要对接的 Agent")
			return
		}
	} else {
		if parsed, err := url.Parse(baseURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			fail(w, http.StatusUnprocessableEntity, "接口地址 (Base URL) 必须是完整的 http 或 https 地址")
			return
		}
		if apiKey == "" {
			fail(w, http.StatusBadRequest, "请先填写 API Key")
			return
		}
		if model == "" {
			fail(w, http.StatusUnprocessableEntity, "请先选择或填写用于测试的模型名称")
			return
		}
	}

	res, err := llmcompat.CompleteChat(r.Context(), h.HTTPClient, llmcompat.ChatRequest{
		Protocol:  proto,
		BaseURL:   baseURL,
		APIKey:    apiKey,
		AgentID:   agentID,
		Model:     model,
		Reasoning: payload.Reasoning,
		MaxTokens: 1024,
		Messages:  []llmcompat.ChatMessage{{Role: "user", Content: "Reply with OK only."}},
	})
	latency := time.Since(started).Milliseconds()
	if err != nil {
		fail(w, http.StatusBadRequest, "连接测试失败："+err.Error())
		return
	}
	if res.Status >= 400 || strings.TrimSpace(res.Content) == "" {
		msg := "连接测试失败：HTTP " + strconv.Itoa(res.Status)
		if detail := llmcompat.ExtractErrorMessage(res.Raw); detail != "" {
			msg += "：" + detail
		}
		fail(w, http.StatusBadRequest, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"model":      res.Model,
		"reply":      res.Content,
		"latency_ms": latency,
	})
}
