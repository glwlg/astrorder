package agentapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestMCPToolsListIsEmpty asserts the retired MCP endpoint never advertises
// tools, even though the legacy Tools() shim remains for old in-repo tests.
func TestMCPToolsListIsEmpty(t *testing.T) {
	resp := HandleRPC(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, nil)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list missing result: %v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list missing tools array: %v", result)
	}
	if len(tools) != 0 {
		t.Fatalf("tools/list must be empty, got %d tools", len(tools))
	}
}

func TestMCPInitializeDoesNotAdvertiseTools(t *testing.T) {
	resp := HandleRPC(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize missing result: %v", resp)
	}
	capabilities, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("initialize missing capabilities: %v", result)
	}
	if len(capabilities) != 0 {
		t.Fatalf("initialize must not advertise MCP tools, got %v", capabilities)
	}
}

// TestMCPToolsCallRejectsEveryName asserts dot, underscore, extended, session
// and unknown names are all rejected before the invoker can run.
func TestMCPToolsCallRejectsEveryName(t *testing.T) {
	names := []string{
		"catalog_list", "catalog.list",
		"sessions_list", "sessions.list",
		"sessions_create", "sessions.create",
		"sessions_delete", "sessions.delete",
		"sessions_delegate", "sessions.delegate",
		"sessions_operation", "sessions.operation",
		"machines_dispatch", "machines.dispatch",
		"blackboard_get", "blackboard.get",
		"browser_run", "browser.run",
		"swarm_sos_escalate", "swarm.sos.escalate",
		"unknown_tool", "unknown.tool",
		"", "   ",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var calls int32
			invoker := func(cap string, payload map[string]any) (any, error) {
				atomic.AddInt32(&calls, 1)
				return map[string]any{"ok": true}, nil
			}
			resp := HandleRPC(map[string]any{
				"jsonrpc": "2.0",
				"id":      7,
				"method":  "tools/call",
				"params":  map[string]any{"name": name, "arguments": map[string]any{"key": "x"}},
			}, invoker)
			if atomic.LoadInt32(&calls) != 0 {
				t.Fatalf("invoker called %d times for retired tool %q", calls, name)
			}
			errObj, ok := resp["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected error for %q, got %v", name, resp)
			}
			message, _ := errObj["message"].(string)
			if !strings.Contains(message, "retired") {
				t.Fatalf("expected retirement error for %q, got %v", name, errObj)
			}
		})
	}
}

// TestMCPHTTPEndpointsAreRetired asserts both the MCP JSON-RPC endpoint and the
// old per-agent MCP toggle answer 410 Gone for GET and POST.
func TestMCPHTTPEndpointsAreRetired(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	h.options.Authorizer = &mockTestAuthorizer{}

	paths := []string{"/api/v1/agent/mcp", "/api/v1/agents/foo/mcp"}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req := httptest.NewRequest(method, path, bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusGone {
				t.Fatalf("%s %s: expected 410, got %d: %s", method, path, w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s %s: invalid JSON: %v", method, path, err)
			}
			detail, _ := body["detail"].(string)
			if !strings.Contains(strings.ToLower(detail), "retired") {
				t.Fatalf("%s %s: expected retirement detail, got %v", method, path, body)
			}
		}
	}

	// Retirement must not depend on authentication or leave a fake permission
	// gate behind: an unauthenticated old client still gets 410.
	h.options.Authorizer = nil
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("unauthenticated retired MCP: expected 410, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHTTPCatalogAndInvokeStillExpose42Capabilities asserts the supported HTTP
// surface keeps the full catalog and invoke behavior.
func TestHTTPCatalogAndInvokeStillExpose42Capabilities(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	h.options.Authorizer = &mockTestAuthorizer{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/catalog", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", w.Code, w.Body.String())
	}
	var catalog map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("catalog invalid JSON: %v", err)
	}
	items, _ := catalog["items"].([]any)
	if len(items) != 42 {
		t.Fatalf("expected 42 catalog items, got %d", len(items))
	}

	body, _ := json.Marshal(map[string]any{"capability": "catalog.list", "input": map[string]any{}})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/invoke", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("invoke status=%d body=%s", w.Code, w.Body.String())
	}
	var invoked map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &invoked); err != nil {
		t.Fatalf("invoke invalid JSON: %v", err)
	}
	items, _ = invoked["items"].([]any)
	if len(items) != 42 {
		t.Fatalf("expected 42 invoke catalog items, got %d", len(items))
	}
}

// TestLegacyToolsShimIsEmpty documents that the old Tools() compatibility
// helper is empty; the real HTTP catalog is the only capability source.
func TestLegacyToolsShimIsEmpty(t *testing.T) {
	if tools := Tools(); len(tools) != 0 {
		t.Fatalf("expected empty legacy tools shim, got %d", len(tools))
	}
}
