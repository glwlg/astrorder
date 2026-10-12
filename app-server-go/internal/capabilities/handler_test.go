package capabilities

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
)

type denyAuth struct{}

func (denyAuth) AuthorizeBrowser(*http.Request) *auth.Error {
	return &auth.Error{Status: http.StatusUnauthorized, Detail: "no"}
}

func TestMatchesOwnsTheDocumentedRoutes(t *testing.T) {
	owned := [][2]string{
		{"GET", "/api/v1/capabilities/targets"}, {"GET", "/api/v1/capabilities/summary"},
		{"POST", "/api/v1/capabilities/mcp/update"}, {"POST", "/api/v1/capabilities/mcp/ping"},
		{"POST", "/api/v1/capabilities/plugins/update"}, {"POST", "/api/v1/capabilities/marketplaces/update"},
		{"POST", "/api/v1/capabilities/dispatch"}, {"GET", "/api/v1/skills/targets"}, {"GET", "/api/v1/skills/search"},
		{"GET", "/api/v1/skills/target/local"}, {"POST", "/api/v1/skills/install"}, {"POST", "/api/v1/skills/remove"},
		{"GET", "/api/v1/rtk/local"}, {"POST", "/api/v1/rtk/ssh-1/install"}, {"PUT", "/api/v1/rtk/local/agents/codex"},
	}
	for _, route := range owned {
		if !Matches(route[0], route[1]) {
			t.Errorf("%v not matched", route)
		}
	}
	for _, route := range [][2]string{{"POST", "/api/v1/capabilities/targets"}, {"GET", "/api/v1/rtk/"}, {"GET", "/api/v1/skills/install"}, {"GET", "/api/v1/sessions"}} {
		if Matches(route[0], route[1]) {
			t.Errorf("%v wrongly matched", route)
		}
	}
}

func call(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = *bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, &reader)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	var decoded map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

func TestHandlerEndToEnd(t *testing.T) {
	env := newEnv("local")
	env.Info.Kind = "local"
	env.Files[env.P.Codex+"/config.toml"] = []byte(codexSample)
	h := NewHandler(serviceFor(env), nil)

	status, body := call(t, h, "GET", "/api/v1/capabilities/summary?target_id=local&agent=codex", nil)
	if status != 200 || body["target_id"] != "local" || body["counts"].(map[string]any)["mcp"].(float64) != 2 {
		t.Fatalf("%d %v", status, body)
	}
	status, body = call(t, h, "POST", "/api/v1/capabilities/mcp/update", map[string]any{
		"target_id": "local", "agent": "codex", "action": "save", "server": map[string]any{"name": "n1", "url": "http://127.0.0.1:1/mcp"}})
	if status != 200 || body["ok"] != true || body["server"] != "n1" {
		t.Fatalf("%d %v", status, body)
	}
	status, body = call(t, h, "POST", "/api/v1/capabilities/mcp/update", map[string]any{
		"target_id": "local", "agent": "codex", "action": "save", "server": map[string]any{"name": "bad name", "url": "http://x"}})
	if status != 422 || !strings.Contains(body["detail"].(string), "名称") {
		t.Fatalf("%d %v", status, body)
	}
	status, _ = call(t, h, "POST", "/api/v1/capabilities/mcp/update", map[string]any{"target_id": "local"})
	if status != 422 {
		t.Fatalf("missing server must be 422, got %d", status)
	}
	status, body = call(t, h, "POST", "/api/v1/capabilities/mcp/update", map[string]any{"target_id": "nope", "agent": "codex", "action": "delete", "server": map[string]any{"name": "x"}})
	if status != 404 {
		t.Fatalf("%d %v", status, body)
	}
	status, body = call(t, h, "GET", "/api/v1/capabilities/targets", nil)
	_ = body
	if status != 200 {
		t.Fatalf("targets: %d", status)
	}
	status, body = call(t, h, "GET", "/api/v1/skills/targets", nil)
	rows, _ := body["targets"].([]any)
	if status != 200 || len(rows) != 1 {
		t.Fatalf("%d %v", status, body)
	}
	status, body = call(t, h, "POST", "/api/v1/skills/install", map[string]any{"target_ids": []string{"local"}, "package_source": "--flag"})
	results, _ := body["results"].([]any)
	if status != 200 || len(results) != 1 || results[0].(map[string]any)["success"] != false {
		t.Fatalf("%d %v", status, body)
	}
	status, _ = call(t, h, "POST", "/api/v1/skills/install", map[string]any{"target_ids": []string{}, "package_source": "o/r"})
	if status != 422 {
		t.Fatalf("empty target list must be 422, got %d", status)
	}
	status, body = call(t, h, "PUT", "/api/v1/rtk/local/agents/codex", map[string]any{})
	if status != 422 {
		t.Fatalf("missing enabled must be 422, got %d %v", status, body)
	}
	status, body = call(t, h, "PUT", "/api/v1/rtk/local/agents/grok", map[string]any{"enabled": true})
	if status != 422 {
		t.Fatalf("grok: %d %v", status, body)
	}
	status, body = call(t, h, "GET", "/api/v1/rtk/local?days=7", nil)
	if status != 200 || body["installed"] != true {
		t.Fatalf("%d %v", status, body)
	}
}

func TestHandlerRequiresBrowserAuth(t *testing.T) {
	h := NewHandler(serviceFor(newEnv("local")), denyAuth{})
	status, _ := call(t, h, "GET", "/api/v1/capabilities/targets", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d", status)
	}
	_ = context.Background()
}
