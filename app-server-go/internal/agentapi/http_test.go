package agentapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestGatewayHTTPAndMCPSharePersistentBlackboard(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "fixture", ConnectorSecret: "connector-fixture"}, nil), Publisher: application.NewEventHub(st)})
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, body string) (int, map[string]any) {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer connector-fixture")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var result map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return w.Code, result
	}
	status, result := call("/api/v1/agent/invoke", `{"capability":"blackboard.set","input":{"session_key":"group::g","key":"facts","value":{"x":9007199254740993}}}`)
	if status != 200 || result["ok"] != true {
		t.Fatalf("invoke=%d %+v", status, result)
	}
	status, result = call("/api/v1/agent/mcp", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"blackboard_get","arguments":{"session_key":"group::g","key":"facts"}}}`)
	if status != 200 || result["error"] != nil {
		t.Fatalf("mcp=%d %+v", status, result)
	}
	value, err := st.GetPreference(context.Background(), "blackboard:group:g:facts")
	if err != nil || value.Value == nil {
		t.Fatalf("not persisted: %+v %v", value, err)
	}
	anonymous := httptest.NewRequest(http.MethodGet, "/api/v1/agent/catalog", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, anonymous)
	if response.Code != 401 {
		t.Fatal("Agent API not authenticated")
	}
}
