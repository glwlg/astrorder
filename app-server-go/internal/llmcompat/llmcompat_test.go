package llmcompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNormalizeBaseURLAndChatCompletionsURL(t *testing.T) {
	cases := []struct {
		in       string
		wantBase string
		wantChat string
	}{
		{
			in:       "https://generativelanguage.googleapis.com",
			wantBase: "https://generativelanguage.googleapis.com/v1beta/openai",
			wantChat: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
		},
		{
			in:       "https://generativelanguage.googleapis.com/v1beta/",
			wantBase: "https://generativelanguage.googleapis.com/v1beta/openai",
			wantChat: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
		},
		{
			in:       "https://generativelanguage.googleapis.com/v1beta/openai/",
			wantBase: "https://generativelanguage.googleapis.com/v1beta/openai",
			wantChat: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
		},
		{
			in:       "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
			wantBase: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
			wantChat: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
		},
		{
			in:       "http://192.168.1.100:3425/v1/",
			wantBase: "http://192.168.1.100:3425/v1",
			wantChat: "http://192.168.1.100:3425/v1/chat/completions",
		},
	}
	for _, tc := range cases {
		if got := NormalizeBaseURL(tc.in); got != tc.wantBase {
			t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.wantBase)
		}
		if got := ChatCompletionsURL(tc.in); got != tc.wantChat {
			t.Fatalf("ChatCompletionsURL(%q) = %q, want %q", tc.in, got, tc.wantChat)
		}
	}
}

func TestApplyReasoningByProvider(t *testing.T) {
	t.Run("magpie_or_gemini_uses_reasoning_effort", func(t *testing.T) {
		body := map[string]any{"model": "group/flash", "reasoning": map[string]any{"effort": "old"}}
		ApplyReasoning("http://192.168.1.100:3425/v1", "medium", body)
		if body["reasoning_effort"] != "medium" {
			t.Fatalf("expected reasoning_effort=medium, got %#v", body)
		}
		if _, exists := body["reasoning"]; exists {
			t.Fatalf("must not send OpenRouter reasoning object to standard/Gemini gateway: %#v", body)
		}
	})

	t.Run("deepseek_uses_thinking", func(t *testing.T) {
		body := map[string]any{"model": "deepseek-chat"}
		ApplyReasoning("https://api.deepseek.com/v1", "none", body)
		thinking, _ := body["thinking"].(map[string]any)
		if thinking["type"] != "disabled" || body["reasoning_effort"] != nil || body["reasoning"] != nil {
			t.Fatalf("unexpected deepseek payload: %#v", body)
		}
	})

	t.Run("openrouter_keeps_reasoning_object", func(t *testing.T) {
		body := map[string]any{"model": "google/gemini-2.5-flash"}
		ApplyReasoning("https://openrouter.ai/api/v1", "low", body)
		reasoning, _ := body["reasoning"].(map[string]any)
		if reasoning["effort"] != "low" || body["reasoning_effort"] != nil {
			t.Fatalf("unexpected openrouter payload: %#v", body)
		}
	})
}

func TestPostChatCompletionRetriesOnGeminiAndOpenAI400s(t *testing.T) {
	t.Run("gemini_rejects_none_then_succeeds_without_reasoning_effort", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if _, hasReasoning := body["reasoning"]; hasReasoning {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`[{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"reasoning\": Cannot find field.","status":"INVALID_ARGUMENT"}}]`))
				return
			}
			if body["reasoning_effort"] == "none" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`[{"error":{"code":400,"message":"Thinking cannot be disabled for this model (reasoning_effort: none).","status":"INVALID_ARGUMENT"}}]`))
				return
			}
			if n != 2 {
				t.Errorf("expected retry on attempt 2, got %d", n)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"models/gemini-4-hydrogen","choices":[{"message":{"content":"OK"}}]}`))
		}))
		defer srv.Close()

		body := map[string]any{"model": "group/flash", "max_tokens": 2048}
		ApplyReasoning(srv.URL+"/v1", "none", body)
		status, raw, err := PostChatCompletion(context.Background(), srv.Client(), srv.URL+"/v1", "k", body)
		if err != nil || status != http.StatusOK {
			t.Fatalf("expected 200 after fallback, got status=%d err=%v body=%s", status, err, string(raw))
		}
		model, text, ok := ExtractMessageText(raw)
		if !ok || model != "models/gemini-4-hydrogen" || text != "OK" {
			t.Fatalf("unexpected reply: ok=%v model=%q text=%q", ok, model, text)
		}
	})

	t.Run("switches_max_tokens_to_max_completion_tokens", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["max_tokens"] != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."}}`))
				return
			}
			if body["max_completion_tokens"] == nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"model":"o3","choices":[{"message":{"content":[{"type":"text","text":"OK"}]}}]}`))
		}))
		defer srv.Close()

		body := map[string]any{"model": "o3", "max_tokens": 1024}
		ApplyReasoning(srv.URL+"/v1", "low", body)
		status, raw, err := PostChatCompletion(context.Background(), srv.Client(), srv.URL+"/v1", "k", body)
		if err != nil || status != http.StatusOK || attempts.Load() != 2 {
			t.Fatalf("expected retry with max_completion_tokens, got status=%d attempts=%d err=%v", status, attempts.Load(), err)
		}
		_, text, ok := ExtractMessageText(raw)
		if !ok || text != "OK" {
			t.Fatalf("expected multipart content extraction, got ok=%v text=%q", ok, text)
		}
	})
}

func TestExtractErrorMessageSupportsGoogleArrayAndOpenAIObject(t *testing.T) {
	googleErr := `[{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"reasoning\": Cannot find field.","status":"INVALID_ARGUMENT"}}]`
	if got := ExtractErrorMessage([]byte(googleErr)); !strings.Contains(got, `Unknown name "reasoning"`) {
		t.Fatalf("failed to parse Google array error: %q", got)
	}
	openaiErr := `{"error":{"message":"Invalid API key"}}`
	if got := ExtractErrorMessage([]byte(openaiErr)); got != "Invalid API key" {
		t.Fatalf("failed to parse OpenAI error: %q", got)
	}
}
