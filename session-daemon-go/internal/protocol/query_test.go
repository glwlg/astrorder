package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"testing"
)

type nativeQueryFixture struct{ queries int }

func (a *nativeQueryFixture) Create(context.Context, core.Request) (core.Result, error) {
	panic("read-only runtime query must never create a session")
}
func (a *nativeQueryFixture) Query(_ context.Context, r core.Request) (map[string]any, error) {
	a.queries++
	return map[string]any{"native_method": r.Fields["method"], "items": []any{}}, nil
}
func TestRuntimeQueryUsesReadOnlyNativeRoute(t *testing.T) {
	d := New("fixture")
	defer d.Close()
	a := &nativeQueryFixture{}
	d.RegisterRuntime("grok", a)
	result := d.control(context.Background(), map[string]any{"action": "runtime.request", "agent_type": "grok", "method": "initialize", "request_id": "native-query", "params": map[string]any{}})
	if result["action"] != "runtime.request.result" || a.queries != 1 {
		t.Fatal("native runtime query route unavailable", result, a.queries)
	}
	payload, ok := result["result"].(map[string]any)
	if !ok || payload["native_method"] != "initialize" {
		t.Fatal("query result changed", result)
	}
	if _, exists := payload["status"]; exists {
		t.Fatal("query invented session status")
	}
	if len(d.registry.Sessions()) != 0 || len(d.journal.Statuses()) != 0 {
		t.Fatal("read-only query created ownership")
	}
}
