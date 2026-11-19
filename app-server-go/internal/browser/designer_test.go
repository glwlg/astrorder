package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDesignCommandValidation(t *testing.T) {
	base := `{"agent_id":"agent","session_id":"session","target_id":"target","document_id":"doc","element_id":"node",`
	for _, test := range []struct {
		name, fields string
		valid        bool
	}{
		{"state", `"action":"state"}`, true},
		{"selection", `"action":"select","ratio_x":0.5,"ratio_y":0.5}`, true},
		{"outside viewport", `"action":"select","ratio_x":1.1,"ratio_y":0.5}`, false},
		{"missing coordinate", `"action":"select","ratio_x":0.5}`, false},
		{"length", `"action":"apply","property":"width","value":"120px"}`, true},
		{"negative margin", `"action":"apply","property":"margin-left","value":"-12px"}`, true},
		{"negative padding", `"action":"apply","property":"padding-left","value":"-12px"}`, false},
		{"excess length", `"action":"apply","property":"width","value":"999999px"}`, false},
		{"url value", `"action":"apply","property":"background-color","value":"url(https://example.com)"}`, false},
		{"arbitrary CSS", `"action":"apply","property":"position","value":"fixed"}`, false},
		{"color", `"action":"apply","property":"color","value":"#fefefe"}`, true},
		{"unknown action", `"action":"eval"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var command designCommand
			if err := json.Unmarshal([]byte(base+test.fields), &command); err != nil {
				t.Fatal(err)
			}
			if err := command.validate(); (err == nil) != test.valid {
				t.Fatalf("validation = %v, expected valid=%v", err, test.valid)
			}
		})
	}
}

func TestDesignEndpointAuthIsolationAndRuntimeErrors(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	raw, err := NewHandler(Options{Authorizer: testAuthenticator(), CDPURL: fake.server.URL, NoAutoExec: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := raw.(*Handler)
	snapshot, err := handler.manager.Navigate(context.Background(), fixtureSessionKey, "https://example.com/test", "")
	if err != nil {
		t.Fatal(err)
	}
	var evaluations atomic.Int32
	var runtimeFailure atomic.Bool
	fake.cdpHandlers.Store("Runtime.evaluate", func(_ string, params map[string]any) (any, error) {
		expression, _ := params["expression"].(string)
		if !strings.Contains(expression, "function browserDesigner") {
			return nil, fmt.Errorf("use default")
		}
		evaluations.Add(1)
		if runtimeFailure.Load() {
			return map[string]any{"result": map[string]any{"value": map[string]any{"error": "页面已变化，请重新选取"}}}, nil
		}
		return map[string]any{"result": map[string]any{"value": map[string]any{
			"document_id": "doc", "url": "https://example.com/test", "changes": []any{},
		}}}, nil
	})
	body := fmt.Sprintf(`{"agent_id":"agent-1","session_id":"session-1","target_id":%q,"action":"state"}`, snapshot.ActiveTargetID)
	request := func(body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/browser/design", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	if result := request(body, ""); result.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d %s", result.Code, result.Body.String())
	}
	if result := request(body, "browser-secret"); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"snapshot"`) {
		t.Fatalf("design state: %d %s", result.Code, result.Body.String())
	}
	before := evaluations.Load()
	if result := request(strings.Replace(body, `"session-1"`, `"other-session"`, 1), "browser-secret"); result.Code == http.StatusOK {
		t.Fatal("another session was allowed to edit the target")
	}
	if evaluations.Load() != before {
		t.Fatal("cross-session request executed editor code")
	}
	runtimeFailure.Store(true)
	if result := request(body, "browser-secret"); result.Code != http.StatusConflict || !strings.Contains(result.Body.String(), "页面已变化") {
		t.Fatalf("runtime error: %d %s", result.Code, result.Body.String())
	}
	if result := request(strings.TrimSuffix(body, "}")+`,"expression":"alert(1)"}`, "browser-secret"); result.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unexpected request fields accepted: %d", result.Code)
	}
}
