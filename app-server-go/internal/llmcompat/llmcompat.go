// Package llmcompat normalizes OpenAI-compatible Chat Completions requests
// and responses across providers (Google Gemini, OpenAI, DeepSeek, OpenRouter,
// and custom gateways such as Magpie).
package llmcompat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxResponseBytes = 1 << 20

// NormalizeBaseURL trims whitespace and trailing slashes, and fills in the
// OpenAI-compatible path suffix when a user pastes a bare Google Gemini API
// base URL such as https://generativelanguage.googleapis.com/v1beta.
func NormalizeBaseURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	if strings.EqualFold(parsed.Hostname(), "generativelanguage.googleapis.com") {
		switch strings.TrimRight(parsed.Path, "/") {
		case "":
			parsed.Path = "/v1beta/openai"
			return strings.TrimRight(parsed.String(), "/")
		case "/v1", "/v1beta":
			parsed.Path = strings.TrimRight(parsed.Path, "/") + "/openai"
			return strings.TrimRight(parsed.String(), "/")
		}
	}
	return trimmed
}

// ChatCompletionsURL resolves the /chat/completions endpoint from a base URL.
func ChatCompletionsURL(baseURL string) string {
	normalized := NormalizeBaseURL(baseURL)
	if strings.HasSuffix(strings.ToLower(normalized), "/chat/completions") {
		return normalized
	}
	return normalized + "/chat/completions"
}

func baseHost(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// ApplyReasoning writes the provider-compatible reasoning control field onto a
// Chat Completions request payload. Standard OpenAI-compatible endpoints
// (including Google Gemini and gateways forwarding to Gemini) receive the
// standard top-level "reasoning_effort" field rather than OpenRouter's
// non-standard nested "reasoning" object.
func ApplyReasoning(baseURL, reasoning string, body map[string]any) {
	if body == nil {
		return
	}
	delete(body, "reasoning")
	delete(body, "reasoning_effort")
	delete(body, "thinking")

	effort := strings.TrimSpace(reasoning)
	if effort == "" {
		return
	}
	host := baseHost(baseURL)
	switch {
	case host == "api.deepseek.com":
		kind := "enabled"
		if effort == "none" {
			kind = "disabled"
		}
		body["thinking"] = map[string]any{"type": kind}
	case host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai"):
		if effort == "none" {
			body["reasoning"] = map[string]any{"enabled": false}
		} else {
			body["reasoning"] = map[string]any{"effort": effort}
		}
	default:
		body["reasoning_effort"] = effort
	}
}

// PostChatCompletion sends a Chat Completions JSON request and performs narrow
// compatibility retries on HTTP 400/422 when an upstream model rejects
// reasoning parameters (e.g. Google Gemini 3/4 rejecting reasoning_effort="none"
// or non-reasoning models rejecting reasoning_effort) or requires
// max_completion_tokens instead of max_tokens.
func PostChatCompletion(ctx context.Context, client *http.Client, baseURL, apiKey string, body map[string]any) (int, []byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := ChatCompletionsURL(baseURL)
	payload := make(map[string]any, len(body))
	for k, v := range body {
		payload[k] = v
	}

	var status int
	var raw []byte
	for attempt := 0; attempt < 3; attempt++ {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		raw, _ = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		status = resp.StatusCode

		if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
			return status, raw, nil
		}

		changed := false
		if shouldRetryWithoutReasoning(payload, raw) {
			delete(payload, "reasoning")
			delete(payload, "reasoning_effort")
			delete(payload, "thinking")
			changed = true
		}
		if shouldRetryWithMaxCompletionTokens(payload, raw) {
			payload["max_completion_tokens"] = payload["max_tokens"]
			delete(payload, "max_tokens")
			changed = true
		}
		if !changed {
			return status, raw, nil
		}
	}
	return status, raw, nil
}

func hasReasoningField(payload map[string]any) bool {
	_, hasR := payload["reasoning"]
	_, hasRE := payload["reasoning_effort"]
	_, hasT := payload["thinking"]
	return hasR || hasRE || hasT
}

func shouldRetryWithoutReasoning(payload map[string]any, raw []byte) bool {
	if !hasReasoningField(payload) {
		return false
	}
	lower := strings.ToLower(string(raw))
	for _, needle := range []string{
		"reasoning_effort",
		"\"reasoning\"",
		"'reasoning'",
		"thinking",
		"unknown name",
		"cannot find field",
		"unrecognized request argument",
		"unsupported parameter",
		"extra_forbidden",
		"extra inputs are not permitted",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func shouldRetryWithMaxCompletionTokens(payload map[string]any, raw []byte) bool {
	if _, hasMaxTokens := payload["max_tokens"]; !hasMaxTokens {
		return false
	}
	lower := strings.ToLower(string(raw))
	return strings.Contains(lower, "max_tokens") && strings.Contains(lower, "max_completion_tokens")
}

// ExtractMessageText extracts the model name and first assistant choice text
// from a Chat Completions JSON response, supporting both string content and
// array-of-parts content.
func ExtractMessageText(raw []byte) (model string, content string, ok bool) {
	var reply struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil || len(reply.Choices) == 0 {
		return "", "", false
	}
	return strings.TrimSpace(reply.Model), parseContentRaw(reply.Choices[0].Message.Content), true
}

func parseContentRaw(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(trimmed, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// ExtractErrorMessage pulls a concise human-readable error message from either
// OpenAI's {"error":{"message":...}} object or Google Gemini's
// [{"error":{"message":...}}] array format.
func ExtractErrorMessage(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	type errorEnvelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	var msg string
	var single errorEnvelope
	if json.Unmarshal(trimmed, &single) == nil {
		msg = strings.TrimSpace(single.Error.Message)
		if msg == "" {
			msg = strings.TrimSpace(single.Message)
		}
	}
	if msg == "" {
		var list []errorEnvelope
		if json.Unmarshal(trimmed, &list) == nil && len(list) > 0 {
			msg = strings.TrimSpace(list[0].Error.Message)
			if msg == "" {
				msg = strings.TrimSpace(list[0].Message)
			}
		}
	}
	if msg == "" {
		return ""
	}
	msg = strings.Join(strings.Fields(msg), " ")
	runes := []rune(msg)
	if len(runes) > 240 {
		return string(runes[:240]) + "..."
	}
	return msg
}
