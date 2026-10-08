package codex

import "testing"

func TestPermissionHooksKeepNativeWaitingState(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.setSession("thread", "idle")
	r.handleNotification("turn/started", map[string]any{"params": map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn"}}})
	hook := func(method, id, event string) {
		r.handleNotification(method, map[string]any{"params": map[string]any{"threadId": "thread", "turnId": "turn", "run": map[string]any{"id": id, "eventName": event}}})
	}
	hook("hook/started", "observer", "permissionRequest")
	hook("hook/started", "other", "permissionRequest")
	if r.Status() != "waiting_approval" {
		t.Fatalf("permission hook hidden as %s", r.Status())
	}
	hook("hook/completed", "other", "permissionRequest")
	hook("hook/completed", "tool", "preToolUse")
	if r.Status() != "waiting_approval" {
		t.Fatal("unresolved permission hook lost")
	}
	hook("hook/completed", "observer", "permissionRequest")
	if r.Status() != "running" {
		t.Fatalf("completed hook stuck as %s", r.Status())
	}
	hook("hook/started", "observer", "permissionRequest")
	r.handleNotification("item/commandExecution/requestApproval", map[string]any{"id": float64(7), "params": map[string]any{"threadId": "thread"}})
	hook("hook/completed", "observer", "permissionRequest")
	if r.Status() != "waiting_approval" {
		t.Fatal("native approval lost after hook completion")
	}
	r.handleNotification("serverRequest/resolved", map[string]any{"params": map[string]any{"threadId": "thread", "requestId": float64(7)}})
	if r.Status() != "running" {
		t.Fatal(r.Status())
	}
}
