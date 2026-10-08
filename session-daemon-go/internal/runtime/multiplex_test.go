package runtime

import (
	"context"
	"fmt"
	"testing"
)

type dummyChild struct {
	connID   string
	commands []string
	closed   bool
	closeErr error
}

func (d *dummyChild) Create(ctx context.Context, r Request) (Result, error) {
	return Result{SessionID: "sess-created", Status: "idle"}, nil
}

func (d *dummyChild) Spawn(ctx context.Context, r Request) (Result, error) {
	return Result{SessionID: r.SessionID, Status: "idle"}, nil
}

func (d *dummyChild) Command(ctx context.Context, r Request) (Result, error) {
	d.commands = append(d.commands, r.Action)
	return Result{SessionID: r.SessionID, Status: "running"}, nil
}

func (d *dummyChild) Close() error {
	d.closed = true
	return d.closeErr
}

func TestMultiplexRegistryRoutesAndDisconnects(t *testing.T) {
	children := map[string]*dummyChild{}
	factory := func(connID string, settings map[string]any) (Adapter, error) {
		c := &dummyChild{connID: connID}
		children[connID] = c
		return c, nil
	}

	m := NewMultiplexRegistry(factory)
	defer m.Close()

	ctx := context.Background()
	reqA := Request{
		AgentType: "codex-ssh",
		SessionID: "sess-1",
		Fields: map[string]any{
			"params": map[string]any{
				"connection_id": "conn-a",
				"ssh_settings":  map[string]any{"host": "host-a", "port": 22},
			},
		},
	}

	resA, err := m.Spawn(ctx, reqA)
	if err != nil {
		t.Fatal(err)
	}
	if resA.SessionID != "sess-1" {
		t.Fatalf("unexpected session ID: %s", resA.SessionID)
	}

	reqB := Request{
		AgentType: "codex-ssh",
		SessionID: "sess-2",
		Fields: map[string]any{
			"params": map[string]any{
				"connection_id": "conn-b",
				"ssh_settings":  map[string]any{"host": "host-b", "port": 22},
			},
		},
	}
	_, err = m.Spawn(ctx, reqB)
	if err != nil {
		t.Fatal(err)
	}

	sessionsA := m.SessionsForConnection("conn-a")
	if len(sessionsA) != 1 || sessionsA[0] != "sess-1" {
		t.Fatalf("expected [sess-1] for conn-a, got %v", sessionsA)
	}

	// Command routing
	cmdReq := Request{SessionID: "sess-1", Action: "session.send"}
	cmdRes, err := m.Command(ctx, cmdReq)
	if err != nil {
		t.Fatal(err)
	}
	if cmdRes.Status != "running" {
		t.Fatalf("expected running, got %s", cmdRes.Status)
	}
	if len(children["conn-a"].commands) != 1 {
		t.Fatalf("expected command forwarded to conn-a")
	}

	// Disconnect connection
	released, err := m.DisconnectConnection(ctx, "conn-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(released) != 1 || released[0] != "sess-1" {
		t.Fatalf("expected [sess-1] released, got %v", released)
	}
	if !children["conn-a"].closed {
		t.Fatalf("expected conn-a to be closed")
	}

	// Command after disconnect should fail
	_, err = m.Command(ctx, cmdReq)
	if err == nil {
		t.Fatalf("expected error sending command to disconnected session")
	}

	// Test error propagation on disconnect failure
	children["conn-b"].closeErr = fmt.Errorf("underlying connection close failed")
	_, err = m.DisconnectConnection(ctx, "conn-b")
	if err == nil {
		t.Fatalf("expected error from DisconnectConnection when closer fails")
	}
	// Verify conn-b sessions were NOT deregistered
	sessionsB := m.SessionsForConnection("conn-b")
	if len(sessionsB) != 1 || sessionsB[0] != "sess-2" {
		t.Fatalf("expected conn-b sessions to remain intact after failed disconnect, got %v", sessionsB)
	}
}
