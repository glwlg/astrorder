package settingsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/browser"
	"astrorder.dev/app-server/internal/modelgateway"
)

func saveJevGateway(t *testing.T, h *handler, kind, base, key string) {
	t.Helper()
	_, err := modelgateway.SaveGatewayConfig(context.Background(), h.Store, map[string]any{
		"gateway_type": kind, "inference_url": base, "management_url": "https://management.test", "api_key": key, "management_key": "management-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestJevModeSwitchPreservesOfficialKeyAndUsesCurrentGateway(t *testing.T) {
	h := newHandler(t, nil).(*handler)
	saveJevGateway(t, h, "magpie", "https://gateway.test/v1", "gateway-secret")
	if w := call(h, "POST", "/api/v1/services/jev/config", `{"api_key":"official-secret"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	urls, models, keys := []string{}, []string{}, []string{}
	h.HTTPClient = &http.Client{Transport: jevTransport(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Model     string         `json:"model"`
			State     any            `json:"state"`
			Questions map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.State == nil || len(body.Questions) == 0 {
			t.Fatal("lost System One request")
		}
		urls = append(urls, r.URL.String())
		models = append(models, body.Model)
		keys = append(keys, r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"answers":{"recommended_component":{"choice":"MetricGrid","confidence":0.9},"log_nature":{"choice":"Success"}}}`))}, nil
	})}
	// Testing an unsaved mode must not change the saved connection.
	w := call(h, "POST", "/api/v1/services/jev/test", `{"mode":"gateway","model":"typesafe/jev-preview"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	config, _ := browser.GetJevConfig(context.Background(), h.Store)
	if config.Mode != "official" {
		t.Fatal("test persisted its draft")
	}
	w = call(h, "POST", "/api/v1/services/jev/config", `{"mode":"gateway","model":"typesafe/jev-preview"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":true`) || strings.Contains(w.Body.String(), "gateway-secret") || strings.Contains(w.Body.String(), "official-secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	output, _ := json.Marshal(map[string]string{"output": strings.Repeat("operation succeeded\n", 25), "command": "check"})
	if w = call(h, "POST", "/api/v1/tools/jev-filter", string(output)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	saveJevGateway(t, h, "magpie", "https://new-gateway.test/v1", "rotated-secret")
	if w = call(h, "POST", "/api/v1/services/jev/test", `{}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(h, "POST", "/api/v1/services/jev/config", `{"mode":"official"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(h, "POST", "/api/v1/services/jev/test", `{}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	wantURLs := []string{"https://gateway.test/v1/systemone", "https://gateway.test/v1/systemone", "https://new-gateway.test/v1/systemone", "https://api.typesafe.ai/v1/systemone"}
	wantKeys := []string{"Bearer gateway-secret", "Bearer gateway-secret", "Bearer rotated-secret", "Bearer official-secret"}
	for i := range wantURLs {
		if urls[i] != wantURLs[i] || keys[i] != wantKeys[i] {
			t.Fatalf("request %d used wrong mode", i)
		}
	}
	if models[0] != "typesafe/jev-preview" || models[1] != models[0] || models[2] != models[0] || models[3] != "jev-latest" {
		t.Fatal(models)
	}
	if w = call(h, "POST", "/api/v1/services/jev/config", `{"mode":"official","api_key":null}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if browser.GetJevKey(context.Background(), h.Store) != "" {
		t.Fatal("clear did not remove official key")
	}
}

func TestJevDecisionModelCatalogUsesInferenceKeyAndProtocolHeader(t *testing.T) {
	h := newHandler(t, nil).(*handler)
	saveJevGateway(t, h, "magpie", "https://gateway.test/v1", "gateway-secret")
	h.HTTPClient = &http.Client{Transport: jevTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://gateway.test/v1/models" || r.Header.Get("Authorization") != "Bearer gateway-secret" || r.Header.Get("X-Magpie-Deciders") != "1" {
			t.Fatal("wrong catalog request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"chat/model"},{"id":"custom/arbitrary-name","kind":"decision","magpie_label":"Decision model"},{"id":"typesafe/jev-preview","kind":"decision"}]}`))}, nil
	})}
	w := call(h, "GET", "/api/v1/services/jev/models", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "chat/model") || !strings.Contains(w.Body.String(), "custom/arbitrary-name") || !strings.Contains(w.Body.String(), "typesafe/jev-preview") || strings.Contains(w.Body.String(), "gateway-secret") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestJevRejectsInvalidGatewayModeWithoutOfficialFallback(t *testing.T) {
	h := newHandler(t, nil).(*handler)
	call(h, "POST", "/api/v1/services/jev/config", `{"api_key":"official-secret"}`)
	for _, body := range []string{`{"mode":"unknown"}`, `{"mode":"gateway"}`, `{"mode":"gateway","model":"jev","api_key":"wrong-key"}`, `{"mode":"gateway","model":"jev"}`} {
		w := call(h, "POST", "/api/v1/services/jev/config", body)
		if w.Code != 422 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	if browser.GetJevKey(context.Background(), h.Store) != "official-secret" {
		t.Fatal("rejected update changed official key")
	}
	saveJevGateway(t, h, "magpie", "https://gateway.test/v1", "gateway-secret")
	calls := 0
	h.HTTPClient = &http.Client{Transport: jevTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "gateway.test" {
			t.Fatal("gateway failure fell back to official")
		}
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("invalid gateway credential"))}, nil
	})}
	w := call(h, "POST", "/api/v1/services/jev/test", `{"mode":"gateway","model":"typesafe/jev-preview"}`)
	if w.Code != 400 || calls != 1 {
		t.Fatal(w.Code, calls)
	}
	w = call(h, "GET", "/api/v1/services/jev/models", "")
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
}
