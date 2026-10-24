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

func TestGatewayHTTPBlackboardPersistenceAndMCPRetired(t *testing.T) {
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
	status, result = call("/api/v1/agent/invoke", `{"capability":"blackboard.get","input":{"session_key":"group::g","key":"facts"}}`)
	if status != 200 || result["value"] == nil {
		t.Fatalf("invoke get=%d %+v", status, result)
	}
	status, result = call("/api/v1/agent/mcp", `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if status != http.StatusGone {
		t.Fatalf("retired mcp=%d %+v", status, result)
	}
	detail, _ := result["detail"].(string)
	if !strings.Contains(strings.ToLower(detail), "retired") {
		t.Fatalf("retired mcp missing detail: %+v", result)
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

// TestSwarmLockAndSOSCallerBinding covers the P1-1 server defense: HTTP/Invoke
// binds lock ownership and SOS worker identity to the verified caller, rejects
// conflicting explicit values, and the swarm service refuses ownerless calls.
func TestSwarmLockAndSOSCallerBinding(t *testing.T) {
	h, st, _ := setupDelegationTestEnv(t)
	defer st.Close()
	ctx := context.Background()

	if _, err := st.UpsertSession(ctx, map[string]any{
		"id":         "child-session-1",
		"agent_id":   "child-codex-1",
		"title":      "Child Session",
		"workspace":  "C:/child/work",
		"status":     "idle",
		"updated_at": "2026-10-20T00:00:00Z",
	}); err != nil {
		t.Fatalf("upsert child session: %v", err)
	}

	callerA := map[string]any{
		"agent_id":      "parent-hermes-default",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "parent-session-1",
	}
	callerB := map[string]any{
		"agent_id":      "child-codex-1",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "codex",
		"session_id":    "child-session-1",
	}
	keyA := "parent-hermes-default::parent-session-1"
	keyB := "child-codex-1::child-session-1"

	invokeLock := func(caller map[string]any, capability string, extra map[string]any) (map[string]any, error) {
		payload := map[string]any{"resource": "p1-resource", "caller": caller}
		for k, v := range extra {
			payload[k] = v
		}
		raw, err := h.Invoke(ctx, capability, payload)
		if err != nil {
			return nil, err
		}
		result, _ := raw.(map[string]any)
		return result, nil
	}

	// A acquires without owner_key; http.go must bind it to caller A.
	result, err := invokeLock(callerA, "swarm.lock.acquire", nil)
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	if result["acquired"] != true {
		t.Fatalf("A acquire must succeed: %v", result)
	}
	lock, _ := result["lock"].(map[string]any)
	if lock["owner_key"] != keyA {
		t.Fatalf("A owner=%v want %s", lock["owner_key"], keyA)
	}

	// B cannot acquire the same resource.
	result, err = invokeLock(callerB, "swarm.lock.acquire", nil)
	if err != nil {
		t.Fatalf("B acquire: %v", err)
	}
	if result["acquired"] != false {
		t.Fatalf("B acquire must be false: %v", result)
	}
	if result["current_owner"] != keyA {
		t.Fatalf("B acquire current_owner=%v want %s", result["current_owner"], keyA)
	}

	// B cannot release A's lock.
	result, err = invokeLock(callerB, "swarm.lock.release", nil)
	if err != nil {
		t.Fatalf("B release: %v", err)
	}
	if result["released"] != false {
		t.Fatalf("B release must be false: %v", result)
	}

	// A can release its own lock.
	result, err = invokeLock(callerA, "swarm.lock.release", nil)
	if err != nil {
		t.Fatalf("A release: %v", err)
	}
	if result["released"] != true {
		t.Fatalf("A release must succeed: %v", result)
	}

	// Explicit owner_key conflicting with the verified caller is rejected.
	if _, err := invokeLock(callerA, "swarm.lock.acquire", map[string]any{"owner_key": keyB}); err == nil {
		t.Fatal("expected conflicting owner_key rejection")
	} else if apiErr, ok := err.(*AgentApiError); !ok || apiErr.Code != "invalid_caller" {
		t.Fatalf("expected invalid_caller, got %T %v", err, err)
	}

	// SOS without key binds worker_key to caller A.
	sosRaw, err := h.Invoke(ctx, "swarm.sos.escalate", map[string]any{"reason": "fixture", "caller": callerA})
	if err != nil {
		t.Fatalf("SOS escalate: %v", err)
	}
	sosResult, _ := sosRaw.(map[string]any)
	sos, _ := sosResult["sos"].(map[string]any)
	if sos["worker_key"] != keyA {
		t.Fatalf("SOS worker_key=%v want %s", sos["worker_key"], keyA)
	}

	// Direct service calls without owner/key/caller are rejected.
	if _, err := h.swarm.LockAcquire(ctx, map[string]any{"resource": "p1-direct"}); err == nil {
		t.Fatal("ownerless direct acquire must fail")
	}
	if _, err := h.swarm.LockRelease(ctx, map[string]any{"resource": "p1-direct"}); err == nil {
		t.Fatal("ownerless direct release must fail")
	}
	if _, err := h.swarm.SOSEscalate(ctx, map[string]any{"reason": "fixture"}); err == nil {
		t.Fatal("anonymous direct SOS must fail")
	}

	// Explicit valid owners remain compatible.
	direct, err := h.swarm.LockAcquire(ctx, map[string]any{"resource": "p1-explicit", "owner_key": keyA})
	if err != nil || direct["acquired"] != true {
		t.Fatalf("explicit acquire: %v %v", direct, err)
	}
	direct, err = h.swarm.LockRelease(ctx, map[string]any{"resource": "p1-explicit", "owner_key": keyA})
	if err != nil || direct["released"] != true {
		t.Fatalf("explicit release: %v %v", direct, err)
	}
}

// TestPluginsCloseEventScope covers the P2 close-scope gap: the close event
// carries agent_id/session_id resolved from session_key, explicit ids, or the
// verified caller, matching plugins.open precedence.
func TestPluginsCloseEventScope(t *testing.T) {
	h, st, _, pub := setupDelegationTestEnvWithPublisher(t)
	defer st.Close()
	ctx := context.Background()

	cases := []struct {
		name        string
		payload     map[string]any
		wantAgent   any
		wantSession any
	}{
		{
			name:        "session_key",
			payload:     map[string]any{"plugin_id": "browser", "session_key": "agent-a::session-a"},
			wantAgent:   "agent-a",
			wantSession: "session-a",
		},
		{
			name:        "verified_caller",
			payload:     map[string]any{"plugin_id": "browser", "caller_agent_id": "agent-b", "caller_session_id": "session-b"},
			wantAgent:   "agent-b",
			wantSession: "session-b",
		},
		{
			name:        "explicit_ids",
			payload:     map[string]any{"plugin_id": "browser", "agent_id": "agent-c", "session_id": "session-c"},
			wantAgent:   "agent-c",
			wantSession: "session-c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub.events = nil
			if _, err := h.Invoke(ctx, "plugins.close", tc.payload); err != nil {
				t.Fatalf("plugins.close: %v", err)
			}
			if len(pub.events) != 1 {
				t.Fatalf("expected 1 plugin event, got %d", len(pub.events))
			}
			event := pub.events[0]
			if event.eventType != "sidecar.plugin.control" {
				t.Fatalf("event type=%s", event.eventType)
			}
			if event.data["action"] != "close" {
				t.Fatalf("action=%v", event.data["action"])
			}
			if event.data["agent_id"] != tc.wantAgent {
				t.Fatalf("agent_id=%v want %v", event.data["agent_id"], tc.wantAgent)
			}
			if event.data["session_id"] != tc.wantSession {
				t.Fatalf("session_id=%v want %v", event.data["session_id"], tc.wantSession)
			}
		})
	}
}
