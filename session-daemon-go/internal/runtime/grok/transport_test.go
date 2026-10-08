package grok

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestACPResponseReaderIsIndependentOfBlockedProjection(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	c := newClient(nil, nil, func(map[string]any) { close(entered); <-release })
	defer c.close()
	waiter := pendingRequest{response: make(chan map[string]any, 1), err: make(chan error, 1)}
	c.pending[17] = waiter
	go c.readLoop(reader)
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		encoder := json.NewEncoder(writer)
		encoder.Encode(map[string]any{"method": "session/update", "params": map[string]any{}})
		encoder.Encode(map[string]any{"id": 17, "result": map[string]any{"ok": true}})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("projection not reached")
	}
	select {
	case result := <-waiter.response:
		if result["ok"] != true {
			t.Fatal(result)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("blocked projection prevented unrelated RPC response")
	}
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("reader did not consume response")
	}
}

func TestACPFailureCannotBeUndoneByLateApproval(t *testing.T) {
	owned := &ownedSession{sessionID: "owned", failed: true, status: "error", pendingApprovals: map[string]any{}, approvalKinds: map[string]string{}}
	a := New(Config{})
	a.handleFrameNotification(owned, "owned", map[string]any{"method": "session/request_permission", "id": "late", "params": map[string]any{"sessionId": "owned"}})
	if owned.status != "error" || len(owned.pendingApprovals) > 0 {
		t.Fatal("late approval resurrected failed transport", owned.status)
	}
}

func TestACPNotificationUsesAppBridgeEventContract(t *testing.T) {
	events := []string{}
	a := New(Config{Emit: func(_ string, event string, _ map[string]any, _ string) error {
		events = append(events, event)
		return nil
	}})
	owned := &ownedSession{sessionID: "owned", status: "idle"}
	a.handleFrameNotification(owned, "owned", map[string]any{"method": "session/update", "params": map[string]any{"sessionId": "owned", "update": map[string]any{}}})
	if len(events) != 1 || events[0] != "grok.notification" {
		t.Fatal("App bridge does not subscribe to emitted event", events)
	}
}

func TestACPForeignApprovalCannotChangeOwnedState(t *testing.T) {
	owned := &ownedSession{sessionID: "owned", status: "idle", pendingApprovals: map[string]any{}, approvalKinds: map[string]string{}}
	a := New(Config{})
	a.handleFrameNotification(owned, "owned", map[string]any{"method": "session/request_permission", "id": "foreign-request", "params": map[string]any{"sessionId": "foreign"}})
	if owned.status != "idle" || len(owned.pendingApprovals) != 0 {
		t.Fatal("foreign approval gained ownership", owned.status)
	}
}

func TestACPInterruptDoesNotInventNativeIdle(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go io.Copy(io.Discard, reader)
	c := newClient(nil, writer, nil)
	defer c.close()
	a := New(Config{})
	owned := &ownedSession{client: c, status: "running", promptRunning: true, pendingApprovals: map[string]any{}}
	a.sessions["owned"] = owned
	result, err := a.Command(context.Background(), core.Request{SessionID: "owned", Action: "session.interrupt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" || owned.status != "running" || !owned.promptRunning {
		t.Fatal("cancel write was mistaken for native completion", result.Status)
	}
}
