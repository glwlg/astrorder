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

type queryDummyChild struct {
	dummyChild
	exe string
}

func (q *queryDummyChild) Query(_ context.Context, _ Request) (map[string]any, error) {
	return map[string]any{"executable": q.exe}, nil
}

func TestMultiplexRegistryRefreshesUnownedChildWhenSettingsChange(t *testing.T) {
	var created []*queryDummyChild
	m := NewMultiplexRegistry(func(connID string, settings map[string]any) (Adapter, error) {
		exe, _ := settings["antigravity_executable"].(string)
		c := &queryDummyChild{dummyChild: dummyChild{connID: connID}, exe: exe}
		created = append(created, c)
		return c, nil
	})
	defer m.Close()

	ctx := context.Background()
	out1, err := m.Query(ctx, Request{
		AgentType: "antigravity-ssh",
		Fields: map[string]any{
			"params": map[string]any{
				"connection_id": "conn-1",
				"ssh_settings":  map[string]any{"host": "127.0.0.1", "antigravity_executable": "/old/agy"},
			},
		},
	})
	if err != nil || out1["executable"] != "/old/agy" {
		t.Fatalf("query 1: %+v %v", out1, err)
	}

	out2, err := m.Query(ctx, Request{
		AgentType: "antigravity-ssh",
		Fields: map[string]any{
			"params": map[string]any{
				"connection_id": "conn-1",
				"ssh_settings":  map[string]any{"host": "127.0.0.1", "antigravity_executable": "/new/agy"},
			},
		},
	})
	if err != nil || out2["executable"] != "/new/agy" {
		t.Fatalf("expected updated settings to recreate unowned child, got %+v %v", out2, err)
	}
	if len(created) != 2 || !created[0].closed {
		t.Fatalf("expected old unowned child to be closed and replaced, created=%d firstClosed=%v", len(created), created[0].closed)
	}
}

type shutdownDummyChild struct {
	dummyChild
	shutdownCalls int
	shutdownErr   error
}

func (s *shutdownDummyChild) Shutdown(_ context.Context) ([]string, error) {
	s.shutdownCalls++
	if s.shutdownErr != nil {
		return nil, s.shutdownErr
	}
	return []string{}, nil
}

func TestMultiplexRegistryDisconnectPrefersShutdownBeforeClose(t *testing.T) {
	var child *shutdownDummyChild
	m := NewMultiplexRegistry(func(connID string, settings map[string]any) (Adapter, error) {
		child = &shutdownDummyChild{
			dummyChild:  dummyChild{connID: connID},
			shutdownErr: fmt.Errorf("active conversation prevents disconnect"),
		}
		return child, nil
	})
	defer m.Close()

	ctx := context.Background()
	req := Request{
		AgentType: "antigravity-ssh",
		SessionID: "sess-active",
		Fields: map[string]any{
			"params": map[string]any{
				"connection_id": "conn-agy",
				"ssh_settings":  map[string]any{"host": "127.0.0.1"},
			},
		},
	}
	if _, err := m.Spawn(ctx, req); err != nil {
		t.Fatal(err)
	}

	if _, err := m.DisconnectConnection(ctx, "conn-agy"); err == nil {
		t.Fatal("expected failed Shutdown to reject DisconnectConnection")
	}
	if child.shutdownCalls != 1 || child.closed {
		t.Fatalf("expected Shutdown called without Close stranding child: shutdownCalls=%d closed=%v", child.shutdownCalls, child.closed)
	}
	if _, err := m.Spawn(ctx, Request{
		AgentType: "antigravity-ssh",
		SessionID: "sess-still-usable",
		Fields:    req.Fields,
	}); err != nil {
		t.Fatalf("expected child adapter to remain usable after rejected disconnect: %v", err)
	}
}
