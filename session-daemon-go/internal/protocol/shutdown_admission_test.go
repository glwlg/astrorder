package protocol

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAcceptedShutdownPreventsNewRuntimeDispatch(t *testing.T) {
	daemon, err := Open("fixture", filepath.Join(t.TempDir(), "drain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	adapter := &admissionFixture{}
	daemon.RegisterRuntime("fixture", adapter)
	if _, err := daemon.AuthorizeShutdown(nil, false); err != nil {
		t.Fatal(err)
	}
	response := daemon.control(context.Background(), map[string]any{"action": "session.create", "agent_type": "fixture", "operation_id": "after-shutdown"})
	if response["action"] != "error" || adapter.creates != 0 {
		t.Fatalf("accepted shutdown allowed new runtime dispatch: %#v calls=%d", response, adapter.creates)
	}
	if status, err := daemon.AuthorityStatus(); err != nil || status.PendingOperations != 0 {
		t.Fatalf("shutdown rejection reserved operation: %#v %v", status, err)
	}
	if _, err := daemon.AppendEvent(EventInput{SessionID: "after", Event: "new", Status: "running"}); err == nil {
		t.Fatal("draining daemon accepted an external session event")
	}
	if err := daemon.recordConnectorEvent("connector", map[string]any{"agent_id": "connector", "type": "session.upsert", "session_id": "external", "data": map[string]any{"status": "running"}}); err == nil {
		t.Fatal("draining daemon accepted new active Connector state")
	}
	daemon.CancelShutdown()
	response = daemon.control(context.Background(), map[string]any{"action": "session.create", "agent_type": "fixture", "operation_id": "after-cancel"})
	if response["action"] != "session.create.result" || adapter.creates != 1 {
		t.Fatalf("cancelled shutdown did not release admission: %#v", response)
	}
}

func TestShutdownRefusesAlreadyAdmittedControl(t *testing.T) {
	daemon := New("fixture")
	defer daemon.Close()
	if err := daemon.beginControl(); err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.AuthorizeShutdown(nil, false); err == nil {
		t.Fatal("in-flight control was not protected")
	}
	daemon.endControl()
	if _, err := daemon.AuthorizeShutdown(nil, false); err != nil {
		t.Fatal(err)
	}
	daemon.CancelShutdown()
	if err := daemon.beginControl(); err != nil {
		t.Fatal("refused shutdown left admission closed")
	}
	daemon.endControl()
}
