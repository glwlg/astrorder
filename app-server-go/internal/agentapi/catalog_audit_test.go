package agentapi

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryNonSessionCapabilityThroughHTTPInvoke exercises the real HTTP
// invoke path for every non-session capability. The MCP tool surface is
// retired; HTTP catalog/invoke remain the supported automation surface.
func TestEveryNonSessionCapabilityThroughHTTPInvoke(t *testing.T) {
	if len(AllCapabilities) != 42 {
		t.Fatalf("expected 42 HTTP capabilities, got %d", len(AllCapabilities))
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready", "capabilities": []string{"chat", "stop"}})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "audit", "workspace": t.TempDir(), "status": "idle", "updated_at": "2026-10-13T00:00:00Z"})
	receipt := func(sid string) *pb.CommandReceipt {
		return &pb.CommandReceipt{Accepted: true, Session: &pb.SessionRef{SessionId: sid, AgentId: strptr("a")}, Status: pb.SessionStatus_SESSION_STATUS_IDLE, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}
	}
	native := &mockSessionControlServer{createResp: receipt("created"), deleteResp: receipt("s"), sendResp: receipt("s"), interruptResp: receipt("s")}
	client, closeClient := setupMockGRPC(t, native)
	defer closeClient()
	handler, err := NewHandler(Options{Store: st, Client: client, Publisher: &testPublisher{}, DefaultWorkspace: t.TempDir(), BrowserInvoke: func(_ context.Context, cap string, payload map[string]any) (any, error) {
		return map[string]any{"ok": true, "status": "done", "session_key": payload["session_key"], "capability": cap}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	h := handler.(*Handler)
	h.options.Authorizer = &mockTestAuthorizer{}

	call := func(t *testing.T, id string, input map[string]any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"capability": id, "input": input})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/invoke", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s failed: status=%d body=%s", id, w.Code, w.Body.String())
		}
		result := map[string]any{}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("%s returned invalid JSON: %v", id, err)
		}
		if result["ok"] == false {
			t.Fatalf("%s returned failure: %v", id, result)
		}
		return result
	}
	inputs := map[string]map[string]any{
		"catalog.list": nil, "agents.list": nil, "machines.list": nil, "projects.list": nil,
		"plugins.list":      nil,
		"plugins.configure": {"plugin_id": "browser", "enabled": true},
		"plugins.open":      {"plugin_id": "browser", "session_key": "a::s"},
		"plugins.close":     {"plugin_id": "browser"},
		"monitor.sessions.add":    {"key": "a::s"},
		"monitor.sessions.remove": {"key": "a::s"},
		"monitor.layout.set":      {"columns": 2},
		"browser.run":             {"session_key": "a::s", "url": "http://example.invalid", "goal": "fixture", "max_steps": 1},
		"browser.screenshot":      {"session_key": "a::s"},
		"browser.cdp":             {"session_key": "a::s", "method": "Runtime.evaluate", "params": map[string]any{"expression": "1"}},
		"blackboard.set":          {"namespace": "audit", "key": "facts", "value": map[string]any{"fixture": true}},
		"blackboard.get":          {"namespace": "audit", "key": "facts"},
		"blackboard.delete":       {"namespace": "audit", "key": "facts"},
		"blackboard.auto_render":  {"namespace": "audit", "key": "rendered", "content": "[x] done"},
		"blackboard.components.list": nil,
		"blackboard.component.schema": {"component_id": "Checklist"},
		"swarm.milestone.declare":     {"milestone_id": "audit", "title": "audit"},
		"swarm.milestone.resolve":     {"milestone_id": "audit"},
		"swarm.milestone.list":        nil,
		"swarm.telemetry.report":      {"key": "a::s", "progress": 50, "phase": "audit", "status": "ok", "summary": "fixture"},
		"swarm.telemetry.get":         {"key": "a::s"},
		"swarm.sos.escalate":          {"key": "a::s", "reason": "fixture"},
		"swarm.sos.list":              nil,
		"swarm.lock.acquire":          {"owner_key": "a::s", "resource": "audit", "ttl_seconds": 60},
		"swarm.lock.release":          {"owner_key": "a::s", "resource": "audit"},
		"swarm.lock.list":             nil,
	}
	seen := map[string]bool{}
	for _, cap := range Capabilities {
		if strings.HasPrefix(cap.ID, "sessions.") || cap.ID == "machines.dispatch" {
			continue
		}
		capID := cap.ID
		if capID == "swarm.sos.resolve" {
			continue
		}
		t.Run(capID, func(t *testing.T) {
			result := call(t, capID, inputs[capID])
			seen[capID] = true
			if capID == "swarm.sos.escalate" {
				raw, _ := json.Marshal(result)
				t.Log(string(raw))
				if record, ok := result["sos"].(map[string]any); ok {
					inputs["swarm.sos.resolve"] = map[string]any{"sos_id": record["id"]}
				}
			}
		})
	}
	if inputs["swarm.sos.resolve"] == nil {
		t.Fatal("SOS id was not returned")
	}
	call(t, "swarm.sos.resolve", inputs["swarm.sos.resolve"])
	seen["swarm.sos.resolve"] = true
	if len(seen) != 31 {
		t.Fatalf("tested %d of 31 non-session capabilities", len(seen))
	}
	t.Logf("HTTP invoke coverage: %d/%d", len(seen), 31)
}
func strptr(s string) *string { return &s }
