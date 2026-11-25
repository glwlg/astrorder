package llmcompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	// ProtocolOpenAI covers OpenAI official, DeepSeek, Qwen, OpenRouter, OneAPI,
	// LiteLLM, Magpie, OpenCodeX, and any OpenAI-compatible gateway.
	ProtocolOpenAI = "openai"
	// ProtocolGemini covers Google Gemini native REST API and Gemini-compatible relays.
	ProtocolGemini = "gemini"
)

// NormalizeProtocol coerces any protocol value into the supported set ("openai" | "gemini").
func NormalizeProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case ProtocolGemini:
		return ProtocolGemini
	default:
		return ProtocolOpenAI
	}
}

// NormalizeGeminiBaseURL trims whitespace/trailing slashes, strips an accidental
// OpenAI compatibility suffix (/openai), and defaults bare hosts to /v1beta.
func NormalizeGeminiBaseURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	p := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(p), "/openai") {
		p = strings.TrimRight(p[:len(p)-len("/openai")], "/")
	}
	if p == "" {
		p = "/v1beta"
	}
	parsed.Path = p
	return strings.TrimRight(parsed.String(), "/")
}

// OpenAIModelsURL resolves the GET /models endpoint from an OpenAI-compatible base URL.
func OpenAIModelsURL(baseURL string) string {
	normalized := NormalizeBaseURL(baseURL)
	lower := strings.ToLower(normalized)
	if strings.HasSuffix(lower, "/chat/completions") {
		normalized = strings.TrimRight(normalized[:len(normalized)-len("/chat/completions")], "/")
	}
	if strings.HasSuffix(strings.ToLower(normalized), "/models") {
		return normalized
	}
	return normalized + "/models"
}

// GeminiModelsURL resolves the GET /models endpoint from a Gemini-compatible base URL.
func GeminiModelsURL(baseURL string) string {
	normalized := NormalizeGeminiBaseURL(baseURL)
	if strings.HasSuffix(strings.ToLower(normalized), "/models") {
		return normalized
	}
	return normalized + "/models"
}

// ListModels fetches available model IDs from either an OpenAI-compatible or
// Gemini-compatible endpoint (official API or gateway/relay).
func ListModels(ctx context.Context, client *http.Client, protocol, baseURL, apiKey string) ([]string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	proto := NormalizeProtocol(protocol)
	cleanKey := strings.TrimSpace(apiKey)

	if proto == ProtocolGemini {
		return listGeminiModels(ctx, client, baseURL, cleanKey)
	}
	return listOpenAIModels(ctx, client, baseURL, cleanKey)
}

func listOpenAIModels(ctx context.Context, client *http.Client, baseURL, apiKey string) ([]string, error) {
	primary := OpenAIModelsURL(baseURL)
	candidates := []string{primary}

	// If user pasted a bare host (e.g. https://api.openai.com) without /v1, add /v1/models fallback.
	if parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/")); err == nil && parsed.Host != "" {
		p := strings.TrimRight(parsed.Path, "/")
		if p == "" {
			fallback := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/v1/models"
			if fallback != primary {
				candidates = append(candidates, fallback)
			}
		}
	}

	var lastStatus int
	var lastRaw []byte
	var lastErr error
	for idx, endpoint := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		lastStatus = resp.StatusCode
		lastRaw = raw
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			models, parseErr := parseModelListJSON(raw, false)
			if parseErr == nil {
				return models, nil
			}
			lastErr = parseErr
			if idx+1 < len(candidates) {
				continue
			}
		}
		if resp.StatusCode == http.StatusNotFound && idx+1 < len(candidates) {
			continue
		}
		break
	}
	if lastStatus >= 400 {
		detail := ExtractErrorMessage(lastRaw)
		if detail != "" {
			return nil, fmt.Errorf("HTTP %d: %s", lastStatus, detail)
		}
		return nil, fmt.Errorf("HTTP %d", lastStatus)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("无法从模型目录接口读取模型列表")
}

func listGeminiModels(ctx context.Context, client *http.Client, baseURL, apiKey string) ([]string, error) {
	base := GeminiModelsURL(baseURL)
	candidates := []string{base + "?pageSize=200", base}

	var lastStatus int
	var lastRaw []byte
	var lastErr error
	for idx, endpoint := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}
		if apiKey != "" {
			req.Header.Set("x-goog-api-key", apiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		lastStatus = resp.StatusCode
		lastRaw = raw
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			models, parseErr := parseModelListJSON(raw, true)
			if parseErr == nil {
				return models, nil
			}
			lastErr = parseErr
		}
		if idx+1 < len(candidates) && resp.StatusCode >= 400 {
			continue
		}
		break
	}
	if lastStatus >= 400 {
		detail := ExtractErrorMessage(lastRaw)
		if detail != "" {
			return nil, fmt.Errorf("HTTP %d: %s", lastStatus, detail)
		}
		return nil, fmt.Errorf("HTTP %d", lastStatus)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("无法从 Gemini 模型接口读取模型列表")
}

func parseModelListJSON(raw []byte, preferGemini bool) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("模型目录响应为空")
	}

	seen := make(map[string]bool)
	addModel := func(id string, list *[]string) {
		clean := strings.TrimSpace(id)
		if preferGemini {
			clean = strings.TrimPrefix(clean, "models/")
		}
		if clean == "" || seen[clean] {
			return
		}
		seen[clean] = true
		*list = append(*list, clean)
	}

	// 1. Native Gemini format: {"models": [{"name": "models/gemini-2.5-flash", "supportedGenerationMethods": ["generateContent"]}]}
	var geminiPayload struct {
		Models []struct {
			Name                       string   `json:"name"`
			ID                         string   `json:"id"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if json.Unmarshal(trimmed, &geminiPayload) == nil && len(geminiPayload.Models) > 0 {
		var filtered []string
		var all []string
		for _, m := range geminiPayload.Models {
			id := strings.TrimSpace(m.Name)
			if id == "" {
				id = strings.TrimSpace(m.ID)
			}
			clean := strings.TrimPrefix(id, "models/")
			if clean == "" {
				continue
			}
			all = append(all, clean)
			if len(m.SupportedGenerationMethods) == 0 {
				filtered = append(filtered, clean)
				continue
			}
			for _, method := range m.SupportedGenerationMethods {
				if strings.EqualFold(strings.TrimSpace(method), "generateContent") {
					filtered = append(filtered, clean)
					break
				}
			}
		}
		chosen := filtered
		if len(chosen) == 0 {
			chosen = all
		}
		var out []string
		for _, id := range chosen {
			addModel(id, &out)
		}
		sort.Strings(out)
		return out, nil
	}

	// 2. OpenAI format: {"data": [{"id": "gpt-4o"}]}
	var openaiPayload struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if json.Unmarshal(trimmed, &openaiPayload) == nil && openaiPayload.Data != nil {
		var out []string
		for _, item := range openaiPayload.Data {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				id = strings.TrimSpace(item.Name)
			}
			addModel(id, &out)
		}
		sort.Strings(out)
		return out, nil
	}

	// 3. Raw array fallback: [{"id": "..."}] or ["..."]
	var rawArray []any
	if json.Unmarshal(trimmed, &rawArray) == nil {
		var out []string
		for _, item := range rawArray {
			switch v := item.(type) {
			case string:
				addModel(v, &out)
			case map[string]any:
				if id, ok := v["id"].(string); ok && strings.TrimSpace(id) != "" {
					addModel(id, &out)
				} else if name, ok := v["name"].(string); ok && strings.TrimSpace(name) != "" {
					addModel(name, &out)
				}
			}
		}
		sort.Strings(out)
		return out, nil
	}

	return nil, fmt.Errorf("模型目录响应格式无效")
}

// ChatMessage is a protocol-neutral chat turn.
type ChatMessage struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// ChatRequest describes a single non-streaming chat completion call across protocols.
type ChatRequest struct {
	Protocol  string
	BaseURL   string
	APIKey    string
	Model     string
	Reasoning string
	MaxTokens int
	Messages  []ChatMessage
}

// ChatResult is the normalized completion output.
type ChatResult struct {
	Status  int
	Model   string
	Content string
	Raw     []byte
}

// CompleteChat dispatches a chat completion over either OpenAI-compatible
// (/chat/completions) or native Gemini (:generateContent) protocol.
func CompleteChat(ctx context.Context, client *http.Client, req ChatRequest) (ChatResult, error) {
	if client == nil {
		client = http.DefaultClient
	}
	proto := NormalizeProtocol(req.Protocol)
	if proto == ProtocolGemini {
		return completeGeminiChat(ctx, client, req)
	}
	return completeOpenAIChat(ctx, client, req)
}

func completeOpenAIChat(ctx context.Context, client *http.Client, req ChatRequest) (ChatResult, error) {
	msgs := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}
	body := map[string]any{
		"model":    strings.TrimSpace(req.Model),
		"messages": msgs,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	ApplyReasoning(req.BaseURL, req.Reasoning, body)
	status, raw, err := PostChatCompletion(ctx, client, req.BaseURL, req.APIKey, body)
	if err != nil {
		return ChatResult{}, err
	}
	modelName, content, ok := ExtractMessageText(raw)
	if modelName == "" {
		modelName = strings.TrimSpace(req.Model)
	}
	if !ok {
		content = ""
	}
	return ChatResult{
		Status:  status,
		Model:   modelName,
		Content: content,
		Raw:     raw,
	}, nil
}

func completeGeminiChat(ctx context.Context, client *http.Client, req ChatRequest) (ChatResult, error) {
	cleanModel := strings.TrimPrefix(strings.TrimSpace(req.Model), "models/")
	endpoint := NormalizeGeminiBaseURL(req.BaseURL) + "/models/" + url.PathEscape(cleanModel) + ":generateContent"

	var systemParts []map[string]string
	contents := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role == "system" {
			if strings.TrimSpace(m.Content) != "" {
				systemParts = append(systemParts, map[string]string{"text": m.Content})
			}
			continue
		}
		gemRole := "user"
		if role == "assistant" || role == "model" {
			gemRole = "model"
		}
		contents = append(contents, map[string]any{
			"role":  gemRole,
			"parts": []map[string]string{{"text": m.Content}},
		})
	}
	if len(contents) == 0 {
		contents = append(contents, map[string]any{
			"role":  "user",
			"parts": []map[string]string{{"text": ""}},
		})
	}

	payload := map[string]any{
		"contents": contents,
	}
	if len(systemParts) > 0 {
		payload["systemInstruction"] = map[string]any{"parts": systemParts}
	}
	if req.MaxTokens > 0 {
		payload["generationConfig"] = map[string]any{"maxOutputTokens": req.MaxTokens}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return ChatResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return ChatResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(req.APIKey) != "" {
		httpReq.Header.Set("x-goog-api-key", strings.TrimSpace(req.APIKey))
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return ChatResult{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	resp.Body.Close()
	if err != nil {
		return ChatResult{}, err
	}

	modelName, content, ok := extractGeminiText(raw)
	if modelName == "" {
		modelName = cleanModel
	}
	if !ok {
		content = ""
	}
	return ChatResult{
		Status:  resp.StatusCode,
		Model:   modelName,
		Content: content,
		Raw:     raw,
	}, nil
}

func extractGeminiText(raw []byte) (string, string, bool) {
	var parsed struct {
		ModelVersion string `json:"modelVersion"`
		Candidates   []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Candidates) == 0 {
		return "", "", false
	}
	var primary strings.Builder
	var fallback strings.Builder
	for _, p := range parsed.Candidates[0].Content.Parts {
		if p.Text == "" {
			continue
		}
		fallback.WriteString(p.Text)
		if !p.Thought {
			primary.WriteString(p.Text)
		}
	}
	text := primary.String()
	if strings.TrimSpace(text) == "" {
		text = fallback.String()
	}
	return strings.TrimSpace(parsed.ModelVersion), text, true
}
