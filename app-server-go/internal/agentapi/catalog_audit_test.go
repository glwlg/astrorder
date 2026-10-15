package agentapi

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

// Native execution and browser planning use explicit fixtures; gateway,
// persistence, dispatch, schema and receipts are the production implementation.
func TestEveryCatalogToolThroughMCP(t *testing.T) {
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
	call := func(t *testing.T, id string, input map[string]any) map[string]any {
		t.Helper()
		response := HandleRPC(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": ToolName(id), "arguments": input}}, func(cap string, p map[string]any) (any, error) { return h.Invoke(ctx, cap, p) })
		if response["error"] != nil {
			t.Fatalf("%s failed: %v", id, response["error"])
		}
		raw := response["result"].(map[string]any)["content"].([]map[string]any)[0]["text"].(string)
		result := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		if result["ok"] == false {
			t.Fatalf("%s returned failure: %v", id, result)
		}
		return result
	}
	inputs := map[string]map[string]any{
		"sessions.list": {"limit": 10}, "sessions.search": {"q": "audit"}, "sessions.read": {"key": "a::s"}, "sessions.tree": {"key": "a::s"},
		"sessions.create": {"agent_id": "a", "workspace": t.TempDir(), "ephemeral": true}, "sessions.delete": {"key": "a::s"}, "sessions.send": {"key": "a::s", "text": "isolated fixture"}, "sessions.stop": {"key": "a::s"},
		"machines.dispatch": {"machine_id": "local", "agent_kind": "codex", "workspace": t.TempDir()},
		"plugins.configure": {"plugin_id": "browser", "enabled": true}, "plugins.open": {"plugin_id": "browser", "session_key": "a::s"}, "plugins.close": {"plugin_id": "browser"},
		"monitor.sessions.add": {"key": "a::s"}, "monitor.sessions.remove": {"key": "a::s"}, "monitor.layout.set": {"columns": 2},
		"browser.run": {"session_key": "a::s", "url": "http://example.invalid", "goal": "fixture", "max_steps": 1}, "browser.screenshot": {"session_key": "a::s"}, "browser.cdp": {"session_key": "a::s", "method": "Runtime.evaluate", "params": map[string]any{"expression": "1"}},
		"blackboard.set": {"namespace": "audit", "key": "facts", "value": map[string]any{"fixture": true}}, "blackboard.get": {"namespace": "audit", "key": "facts"}, "blackboard.delete": {"namespace": "audit", "key": "facts"}, "blackboard.auto_render": {"namespace": "audit", "key": "rendered", "content": "[x] done"}, "blackboard.component.schema": {"component_id": "Checklist"},
		"swarm.milestone.declare": {"milestone_id": "audit", "title": "audit"}, "swarm.milestone.resolve": {"milestone_id": "audit"},
		"swarm.telemetry.report": {"key": "a::s", "progress": 50, "phase": "audit", "status": "ok", "summary": "fixture"}, "swarm.telemetry.get": {"key": "a::s"},
		"swarm.sos.escalate": {"key": "a::s", "reason": "fixture"}, "swarm.lock.acquire": {"owner_key": "a::s", "resource": "audit", "ttl_seconds": 60}, "swarm.lock.release": {"owner_key": "a::s", "resource": "audit"},
	}
	seen := map[string]bool{}
	// Keep the target alive until every session/browser/monitor check is done.
	for _, cap := range Capabilities {
		if cap.ID == "sessions.delete" || cap.ID == "swarm.sos.resolve" {
			continue
		}
		t.Run(cap.ID, func(t *testing.T) {
			result := call(t, cap.ID, inputs[cap.ID])
			seen[cap.ID] = true
			if cap.ID == "swarm.sos.escalate" {
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
	call(t, "sessions.delete", inputs["sessions.delete"])
	seen["sessions.delete"] = true
	if len(seen) != len(Capabilities) {
		t.Fatal(fmt.Sprintf("tested %d of %d", len(seen), len(Capabilities)))
	}
	t.Logf("MCP catalog coverage: %d/%d", len(seen), len(Capabilities))
}
func strptr(s string) *string { return &s }
