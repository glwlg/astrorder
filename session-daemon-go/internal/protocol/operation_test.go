package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestOperationCapabilityAndReadOnlyLookup(t *testing.T) {
	d, err := Open("key", filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	authenticated := false
	handshake, _ := d.handle([]byte(`{"action":"daemon.handshake","secret":"key"}`), &authenticated)
	capabilities, _ := handshake["operations"].(map[string]any)
	if capabilities["version"] != 1 {
		t.Fatalf("missing operation capability: %v", handshake)
	}
	f := &admissionFixture{}
	d.RegisterRuntime("fixture", f)
	request := map[string]any{"action": "session.create", "agent_type": "fixture", "operation_id": "lookup"}
	query := map[string]any{"action": "operation.read", "operation_action": "session.create", "agent_type": "fixture", "operation_id": "lookup", "request_id": "read"}
	lookup := func() map[string]any { raw, _ := json.Marshal(query); r, _ := d.handle(raw, &authenticated); return r }
	if r := lookup(); r["operation_state"] != "missing" {
		t.Fatal(r)
	}
	created := d.control(context.Background(), request)
	r := lookup()
	if r["action"] != "operation.read.result" || r["operation_state"] != "completed" || r["request_id"] != "read" {
		t.Fatal(r)
	}
	result, _ := r["response"].(map[string]any)
	if result["session_id"] != created["session_id"] || f.creates != 1 {
		t.Fatal(r)
	}
	if r := d.status(nil); r["pending_operations"] != 0 {
		t.Fatal(r)
	}
}

func TestInvalidOperationDoesNotBlockShutdown(t *testing.T) {
	cases := []map[string]any{
		{"action": "session.create", "operation_id": "missing-agent"},
		{"action": "session.create", "agent_type": "fixture", "operation_id": "bad-params", "params": "invalid"},
		{"action": "session.send", "operation_id": "missing-session"},
	}
	for _, request := range cases {
		t.Run(request["operation_id"].(string), func(t *testing.T) {
			d, err := Open("key", filepath.Join(t.TempDir(), "journal.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			f := &admissionFixture{}
			d.RegisterRuntime("fixture", f)
			response := d.control(context.Background(), request)
			if response["action"] != "error" {
				t.Fatalf("invalid request accepted: %v", response)
			}
			if f.creates != 0 {
				t.Fatal("invalid request reached native runtime")
			}
			if status := d.status(nil); status["pending_operations"] != 0 {
				t.Fatalf("validation failure reserved an uncertain operation: %v", status["pending_operations"])
			}
			if response := d.shutdown(map[string]any{}); response["action"] == "error" {
				t.Fatal(response)
			}
		})
	}
}

type timeoutOperationFixture struct{ calls int }

func (f *timeoutOperationFixture) Create(context.Context, core.Request) (core.Result, error) {
	f.calls++
	return core.Result{}, fmt.Errorf("native outcome unavailable")
}

func TestFailedNativeOperationRetainsUncertaintyOnRetry(t *testing.T) {
	d, err := Open("key", filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f := &timeoutOperationFixture{}
	d.RegisterRuntime("fixture", f)
	r := map[string]any{"action": "session.create", "agent_type": "fixture", "operation_id": "uncertain"}
	for i := 0; i < 2; i++ {
		response := d.control(context.Background(), r)
		if response["operation_state"] != "uncertain" {
			t.Fatalf("native failure lost uncertainty: %v", response)
		}
	}
	if f.calls != 1 {
		t.Fatalf("native retried %d times", f.calls)
	}
	if d.shutdown(map[string]any{})["action"] != "error" {
		t.Fatal("shutdown ignored uncertain operation")
	}
}

func TestDurableOperationRetryDoesNotCreateTwice(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal.db")
	d, err := Open("key", p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f := &admissionFixture{}
	d.RegisterRuntime("fixture", f)
	request := map[string]any{"action": "session.create", "agent_type": "fixture", "operation_id": "create-1", "request_id": "first"}
	first := d.control(context.Background(), request)
	if first["action"] != "session.create.result" {
		t.Fatal(first)
	}
	request["request_id"] = "retry"
	second := d.control(context.Background(), request)
	if second["action"] != "session.create.result" || second["request_id"] != "retry" || f.creates != 1 {
		t.Fatalf("duplicate dispatch: %v count=%d", second, f.creates)
	}
	request["title"] = "different"
	conflict := d.control(context.Background(), request)
	if conflict["action"] != "error" || f.creates != 1 {
		t.Fatalf("conflicting retry accepted: %v", conflict)
	}
	delete(request, "title")
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open("key", p)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replacement := &admissionFixture{}
	reopened.RegisterRuntime("fixture", replacement)
	response := reopened.control(context.Background(), request)
	if response["action"] != "session.create.result" || replacement.creates != 0 {
		t.Fatalf("restart dispatched retry: %v count=%d", response, replacement.creates)
	}
}
