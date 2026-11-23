package settingsapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func newHandler(t *testing.T, upstream http.Handler) http.Handler {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	options := Options{Store: st, Port: 30001, Origins: []string{"http://127.0.0.1:30001"}, PublicURL: "https://ao.example", BrowserSecret: "s"}
	if upstream != nil {
		server := httptest.NewServer(upstream)
		t.Cleanup(server.Close)
		options.HTTPClient = server.Client()
	}
	return NewHandler(options)
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestEveryOwnedRouteIsServed(t *testing.T) {
	h := newHandler(t, nil)
	for path, methods := range paths {
		for _, method := range methods {
			if !Matches(method, path) {
				t.Fatalf("%s %s is not matched", method, path)
			}
			if w := call(h, method, path, `{}`); w.Code == http.StatusNotFound {
				t.Fatalf("%s %s answered 404", method, path)
			}
		}
	}
}

func TestNetworkConfigFallsBackToProcessConfigAndSavesOverrides(t *testing.T) {
	h := newHandler(t, nil)
	first := call(h, http.MethodGet, "/api/v1/network/config", "")
	if first.Code != 200 || !strings.Contains(first.Body.String(), `"port":30001`) || !strings.Contains(first.Body.String(), "https://ao.example") {
		t.Fatalf("defaults wrong: %d %s", first.Code, first.Body.String())
	}
	saved := call(h, http.MethodPost, "/api/v1/network/config", `{"allowed_origins":["http://a:1/"," http://a:1","http://b:2"],"public_url":" https://pub "}`)
	body := saved.Body.String()
	if saved.Code != 200 || !strings.Contains(body, `"allowed_origins":["http://a:1","http://b:2","https://pub"]`) || !strings.Contains(body, `"public_url":"https://pub"`) {
		t.Fatalf("overrides not applied or not de-duplicated: %d %s", saved.Code, body)
	}
}

func TestLLMConfigMasksKeyKeepsItWhenOmittedAndValidates(t *testing.T) {
	h := newHandler(t, nil)
	put := call(h, http.MethodPost, "/api/v1/services/llm/config", `{"base_url":"https://llm.example/v1/","api_key":"sk-abcdefghijklmnop","model":"m","reasoning":"high"}`)
	if put.Code != 200 || !strings.Contains(put.Body.String(), `"masked_key":"sk-abc...mnop"`) || strings.Contains(put.Body.String(), "abcdefghijklmnop") {
		t.Fatalf("save/mask wrong: %d %s", put.Code, put.Body.String())
	}
	again := call(h, http.MethodPost, "/api/v1/services/llm/config", `{"base_url":"https://llm.example/v1","model":"m2","reasoning":"low"}`)
	if !strings.Contains(again.Body.String(), `"configured":true`) || !strings.Contains(again.Body.String(), `"model":"m2"`) {
		t.Fatalf("an omitted key must be kept: %s", again.Body.String())
	}
	for _, bad := range []string{
		`{"base_url":"ftp://x","model":"m","reasoning":"low"}`,
		`{"base_url":"https://x","model":"","reasoning":"low"}`,
		`{"base_url":"https://x","model":"m","reasoning":"extreme"}`,
	} {
		if w := call(h, http.MethodPost, "/api/v1/services/llm/config", bad); w.Code != 422 {
			t.Fatalf("%s should be rejected, got %d", bad, w.Code)
		}
	}
}

func TestLLMTestCallsTheGatewayWithTheSavedKey(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		if _, hasOpenRouterReasoning := gotBody["reasoning"]; hasOpenRouterReasoning {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`[{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"reasoning\": Cannot find field.","status":"INVALID_ARGUMENT"}}]`))
			return
		}
		w.Write([]byte(`{"model":"real","choices":[{"message":{"content":"OK"}}]}`))
	})
	st, _ := store.Open(filepath.Join(t.TempDir(), "app.db"))
	t.Cleanup(func() { st.Close() })
	server := httptest.NewServer(upstream)
	defer server.Close()
	h := NewHandler(Options{Store: st, HTTPClient: server.Client()})
	call(h, http.MethodPost, "/api/v1/services/llm/config", `{"base_url":"`+server.URL+`/v1","api_key":"saved-key-0123456789","model":"m","reasoning":"medium"}`)
	w := call(h, http.MethodPost, "/api/v1/services/llm/test", `{"base_url":"`+server.URL+`/v1","model":"m","reasoning":"medium"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reply":"OK"`) || gotAuth != "Bearer saved-key-0123456789" || gotPath != "/v1/chat/completions" {
		t.Fatalf("test call wrong: %d %s auth=%q path=%q", w.Code, w.Body.String(), gotAuth, gotPath)
	}
	if gotBody["reasoning_effort"] != "medium" || gotBody["reasoning"] != nil {
		t.Fatalf("expected standard reasoning_effort without OpenRouter reasoning object: %#v", gotBody)
	}
	if maxTokens, _ := gotBody["max_tokens"].(float64); maxTokens < 1024 {
		t.Fatalf("max_tokens too small for thinking models: %v", gotBody["max_tokens"])
	}
}

func TestLLMTestRetriesWhenGoogleGeminiRejectsReasoningNone(t *testing.T) {
	var attempts int
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["reasoning_effort"] == "none" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`[{"error":{"code":400,"message":"Thinking cannot be disabled for this model (reasoning_effort).","status":"INVALID_ARGUMENT"}}]`))
			return
		}
		_, _ = w.Write([]byte(`{"model":"models/gemini-4-hydrogen","choices":[{"message":{"content":"OK"}}]}`))
	})
	st, _ := store.Open(filepath.Join(t.TempDir(), "app.db"))
	t.Cleanup(func() { st.Close() })
	server := httptest.NewServer(upstream)
	defer server.Close()
	h := NewHandler(Options{Store: st, HTTPClient: server.Client()})
	call(h, http.MethodPost, "/api/v1/services/llm/config", `{"base_url":"`+server.URL+`/v1","api_key":"saved-key-0123456789","model":"group/flash","reasoning":"none"}`)
	w := call(h, http.MethodPost, "/api/v1/services/llm/test", `{"base_url":"`+server.URL+`/v1","model":"group/flash","reasoning":"none"}`)
	if w.Code != 200 || attempts != 2 || !strings.Contains(w.Body.String(), `"reply":"OK"`) {
		t.Fatalf("expected fallback to succeed on attempt 2: code=%d attempts=%d body=%s", w.Code, attempts, w.Body.String())
	}
}

func TestJevKeyRoundTripsAndClears(t *testing.T) {
	h := newHandler(t, nil)
	set := call(h, http.MethodPost, "/api/v1/services/jev/config", `{"api_key":"jev-0123456789-abcdefghijklmnop"}`)
	if !strings.Contains(set.Body.String(), `"configured":true`) || strings.Contains(set.Body.String(), "0123456789-abcdefgh") {
		t.Fatalf("key not stored or leaked: %s", set.Body.String())
	}
	cleared := call(h, http.MethodPost, "/api/v1/services/jev/config", `{"api_key":""}`)
	if !strings.Contains(cleared.Body.String(), `"configured":false`) {
		t.Fatalf("empty key must clear: %s", cleared.Body.String())
	}
}

func TestRuntimeAndDesktopStatusAnswerInsteadOfFailing(t *testing.T) {
	h := newHandler(t, nil)
	if w := call(h, http.MethodGet, "/api/v1/runtime", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"codex"`) {
		t.Fatalf("runtime: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, http.MethodGet, "/api/v1/codex-desktop/status", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatalf("desktop: %d %s", w.Code, w.Body.String())
	}
}

func TestPluginSettingsPersistToStoreAndRespectImportOnly(t *testing.T) {
	h := newHandler(t, nil)
	initial := call(h, http.MethodGet, "/api/v1/plugins/settings", "")
	if initial.Code != 200 || !strings.Contains(initial.Body.String(), `"enabled":{}`) || !strings.Contains(initial.Body.String(), `"configs":{}`) {
		t.Fatalf("initial plugin settings wrong: %d %s", initial.Code, initial.Body.String())
	}
	saved := call(h, http.MethodPut, "/api/v1/plugins/settings", `{"enabled":{"drawio":false,"sidebar-integrations":true},"configs":{"drawio":{"theme":"dark"}}}`)
	if saved.Code != 200 || !strings.Contains(saved.Body.String(), `"drawio":false`) || !strings.Contains(saved.Body.String(), `"theme":"dark"`) {
		t.Fatalf("save plugin settings failed: %d %s", saved.Code, saved.Body.String())
	}
	// import_only must not clobber already-saved DB keys, only fill missing keys
	imported := call(h, http.MethodPut, "/api/v1/plugins/settings", `{"import_only":true,"enabled":{"drawio":true,"mermaid":false},"configs":{"drawio":{"theme":"light","grid":true}}}`)
	if imported.Code != 200 {
		t.Fatalf("import_only failed: %d %s", imported.Code, imported.Body.String())
	}
	var got pluginSettingsView
	if err := json.Unmarshal(imported.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled["drawio"] != false || got.Enabled["mermaid"] != false || got.Enabled["sidebar-integrations"] != true {
		t.Fatalf("unexpected enabled map after import_only: %#v", got.Enabled)
	}
	if got.Configs["drawio"]["theme"] != "dark" || got.Configs["drawio"]["grid"] != true {
		t.Fatalf("unexpected configs map after import_only: %#v", got.Configs)
	}
}
