package grok

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestACPFailureFixture(t *testing.T) {
	if os.Getenv("ASTRORDER_ACP_FAILURE_FIXTURE") != "1" {
		return
	}
	dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			os.Exit(0)
		}
		method, _ := req["method"].(string)
		result := map[string]any{}
		switch method {
		case "initialize":
			result["protocolVersion"] = 1
		case "session/load":
			result["sessionId"] = req["params"].(map[string]any)["sessionId"]
		case "exit-fixture":
			os.Exit(0)
		case "session/prompt":
			enc.Encode(map[string]any{"method": "session/update", "params": map[string]any{"sessionId": req["params"].(map[string]any)["sessionId"], "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": "fixture"}}}})
			result["stopReason"] = "end_turn"
		}
		if req["id"] != nil {
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": result})
		}
	}
}

func failureAdapter(t *testing.T) *Adapter {
	t.Helper()
	t.Setenv("ASTRORDER_ACP_FAILURE_FIXTURE", "1")
	// Child race instrumentation normally sleeps for 1s in os.Exit; this is not
	// native exit latency. Keep the fixture deadline independent of that delay.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=^TestACPFailureFixture$"}, Workspace: root, Allowed: []string{root}})
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	if _, err := a.Spawn(ctx, core.Request{SessionID: "owned"}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestACPExplicitCloseReleasesOnlyAfterConfirmedCleanup(t *testing.T) {
	a := failureAdapter(t)
	result, err := a.Command(context.Background(), core.Request{SessionID: "owned", Action: "session.close"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Payload["closed"] != true || len(a.Snapshot()) != 0 {
		t.Fatal("close did not provide durable binding-release contract", result)
	}
}

func TestACPDeleteCannotPretendToDeleteNativeHistory(t *testing.T) {
	a := failureAdapter(t)
	if _, err := a.Command(context.Background(), core.Request{SessionID: "owned", Action: "session.delete"}); err == nil {
		t.Fatal("closing transport was falsely reported as native history deletion")
	}
	if len(a.Snapshot()) != 1 {
		t.Fatal("unsupported deletion released owned native session")
	}
}

func TestACPAdapterCloseReportsUnfinishedProjection(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	c := newClient(nil, nil, func(map[string]any) { close(entered); <-release })
	c.queueEvent(func() { c.onNotification(nil) })
	<-entered
	a := New(Config{})
	a.sessions["owned"] = &ownedSession{client: c, status: "idle"}
	if err := a.Close(); err == nil {
		t.Fatal("adapter swallowed incomplete dispatcher cleanup")
	}
	if err := a.Close(); err == nil {
		t.Fatal("second close erased cleanup failure")
	}
}

func TestACPAdapterCloseIncludesStartupClients(t *testing.T) {
	t.Setenv("ASTRORDER_ACP_FAILURE_FIXTURE", "1")
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=^TestACPFailureFixture$"}})
	c, err := a.startClient(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.processDone:
	case <-time.After(time.Second):
		t.Fatal("adapter Close leaked client before session registration")
	}
}

func TestACPClientRequiresOwnedProcessTree(t *testing.T) {
	a := failureAdapter(t)
	c := a.sessions["owned"].client
	if c.command.SysProcAttr == nil {
		t.Fatal("native client started without owned process group/job")
	}
}

func TestACPStartupHonorsCancelledContext(t *testing.T) {
	t.Setenv("ASTRORDER_ACP_FAILURE_FIXTURE", "1")
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=^TestACPFailureFixture$"}})
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, err := a.startClient(ctx, t.TempDir(), nil)
	if c != nil {
		c.close()
	}
	if err != context.Canceled {
		t.Fatal("cancelled startup still launched native client", err)
	}
}

func TestACPProjectionFailureFailsOwnedTransport(t *testing.T) {
	a := failureAdapter(t)
	a.config.Emit = func(_ string, event string, _ map[string]any, _ string) error {
		if event == "grok.notification" {
			return fmt.Errorf("fixture journal unavailable")
		}
		return nil
	}
	if _, err := a.Command(context.Background(), core.Request{SessionID: "owned", Action: "session.send", Fields: map[string]any{"command_id": "emitter", "prompt": "fixture"}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if a.Snapshot()["owned"].Status == "error" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("projection failed but adapter permitted a false successful completion")
}

func TestACPCompletionCannotOvertakeNativeProjection(t *testing.T) {
	a := failureAdapter(t)
	entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	a.config.Emit = func(_ string, event string, _ map[string]any, _ string) error {
		if event == "grok.notification" {
			close(entered)
			<-release
		}
		if event == "grok.completed" {
			close(completed)
		}
		return nil
	}
	if _, err := a.Command(context.Background(), core.Request{SessionID: "owned", Action: "session.send", Fields: map[string]any{"command_id": "ordering", "prompt": "fixture"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native event missing")
	}
	select {
	case <-completed:
		t.Fatal("completion overtook blocked native projection")
	case <-time.After(100 * time.Millisecond):
	}
	if a.Snapshot()["owned"].Status != "running" {
		t.Fatal("native output is still uncommitted but snapshot became idle")
	}
}

func TestACPIdleTransportExitCannotKeepIdleSnapshot(t *testing.T) {
	a := failureAdapter(t)
	c := a.sessions["owned"].client
	if err := c.send(map[string]any{"method": "exit-fixture"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if a.Snapshot()["owned"].Status == "error" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("native transport died but owned snapshot still idle")
}
