package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"path/filepath"
	"testing"
)

type rejectedOperationFixture struct{ calls int }

func (f *rejectedOperationFixture) Create(context.Context, core.Request) (core.Result, error) {
	f.calls++
	return core.Result{}, core.RejectBeforeDispatch("local guard rejected input")
}
func TestLocalRejectionCompletesLedgerWithoutNativeRedispatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	d, err := Open("key", path)
	if err != nil {
		t.Fatal(err)
	}
	f := &rejectedOperationFixture{}
	d.RegisterRuntime("fixture", f)
	req := map[string]any{"action": "session.create", "agent_type": "fixture", "operation_id": "rejected"}
	for i := 0; i < 2; i++ {
		res := d.control(context.Background(), req)
		if res["action"] != "error" || res["rejected_before_dispatch"] != true || res["operation_state"] == "uncertain" {
			t.Fatalf("rejection became uncertain: %+v", res)
		}
	}
	if f.calls != 1 {
		t.Fatal("rejection was dispatched again")
	}
	if d.status(nil)["pending_operations"] != 0 {
		t.Fatal("local rejection blocks shutdown")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open("key", path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	replacement := &rejectedOperationFixture{}
	d.RegisterRuntime("fixture", replacement)
	res := d.control(context.Background(), req)
	if res["rejected_before_dispatch"] != true || replacement.calls != 0 {
		t.Fatal("lost rejected receipt across restart")
	}
}
