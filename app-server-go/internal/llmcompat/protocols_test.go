package llmcompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestListModelsOpenAIAndGemini(t *testing.T) {
	t.Run("openai standard and bare host /v1/models fallback", func(t *testing.T) {
		var gotAuth string
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			gotAuth = r.Header.Get("Authorization")
			if r.URL.Path == "/models" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1-mini"},{"id":"deepseek-chat"},{"id":"gpt-4.1-mini"}]}`))
		}))
		defer srv.Close()

		models, err := ListModels(context.Background(), srv.Client(), ProtocolOpenAI, srv.URL, "sk-test-123")
		if err != nil {
			t.Fatalf("ListModels openai failed: %v", err)
		}
		want := []string{"deepseek-chat", "gpt-4.1-mini"}
		if !reflect.DeepEqual(models, want) {
			t.Fatalf("got %v, want %v", models, want)
		}
		if gotAuth != "Bearer sk-test-123" {
			t.Fatalf("unexpected auth header: %q", gotAuth)
		}
		if len(paths) != 2 || paths[0] != "/models" || paths[1] != "/v1/models" {
			t.Fatalf("expected /models -> /v1/models fallback, got %v", paths)
		}
	})

	t.Run("gemini native models filters generateContent and strips models/ prefix", func(t *testing.T) {
		var gotKey, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotKey = r.Header.Get("x-goog-api-key")
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"models": [
					{"name":"models/gemini-2.5-pro","supportedGenerationMethods":["generateContent","countTokens"]},
					{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent"]},
					{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]}
				]
			}`))
		}))
		defer srv.Close()

		models, err := ListModels(context.Background(), srv.Client(), ProtocolGemini, srv.URL, "gem-key-999")
		if err != nil {
			t.Fatalf("ListModels gemini failed: %v", err)
		}
		want := []string{"gemini-2.5-flash", "gemini-2.5-pro"}
		if !reflect.DeepEqual(models, want) {
			t.Fatalf("got %v, want %v", models, want)
		}
		if gotKey != "gem-key-999" || gotPath != "/v1beta/models" {
			t.Fatalf("unexpected gemini request: key=%q path=%q", gotKey, gotPath)
		}
	})
}

func TestCompleteChatGeminiNative(t *testing.T) {
	var gotKey, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-goog-api-key")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"modelVersion": "gemini-2.5-flash-001",
			"candidates": [{
				"content": {
					"parts": [
						{"text": "internal thought", "thought": true},
						{"text": "OK from Gemini"}
					]
				}
			}]
		}`))
	}))
	defer srv.Close()

	res, err := CompleteChat(context.Background(), srv.Client(), ChatRequest{
		Protocol:  ProtocolGemini,
		BaseURL:   srv.URL + "/v1beta",
		APIKey:    "AIza-test-key",
		Model:     "gemini-2.5-flash",
		MaxTokens: 256,
		Messages: []ChatMessage{
			{Role: "system", Content: "Be brief."},
			{Role: "user", Content: "Say OK"},
		},
	})
	if err != nil {
		t.Fatalf("CompleteChat gemini failed: %v", err)
	}
	if res.Status != 200 || res.Model != "gemini-2.5-flash-001" || res.Content != "OK from Gemini" {
		t.Fatalf("unexpected gemini result: %+v", res)
	}
	if gotKey != "AIza-test-key" || !strings.HasSuffix(gotPath, "/v1beta/models/gemini-2.5-flash:generateContent") {
		t.Fatalf("unexpected gemini request: key=%q path=%q", gotKey, gotPath)
	}
	if gotBody["systemInstruction"] == nil {
		t.Fatalf("expected systemInstruction in Gemini request body: %#v", gotBody)
	}
}
