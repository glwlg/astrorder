package runtime

import (
	"context"
	"testing"
)

type snapshotChild struct {
	dummyChild
	status string
}

func (c *snapshotChild) Snapshot() map[string]Result {
	return map[string]Result{"owned": {SessionID: "owned", Status: c.status, Payload: map[string]any{"agent_id": "remote-hermes", "connection_id": c.connID}}, "foreign": {SessionID: "foreign", Status: "running"}}
}
func TestMultiplexReattachPreservesIdentityAfterReadControl(t *testing.T) {
	child := &snapshotChild{status: "idle", dummyChild: dummyChild{connID: "debian"}}
	m := NewMultiplexRegistry(func(string, map[string]any) (Adapter, error) { return child, nil })
	r := NewRegistry()
	r.Register("ssh", m)
	defer r.Close()
	ctx := context.Background()
	req := Request{AgentType: "ssh", SessionID: "owned", Fields: map[string]any{"params": map[string]any{"connection_id": "debian", "ssh_settings": map[string]any{"host": "host"}}}}
	if _, err := r.Spawn(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Command(ctx, Request{SessionID: "owned", Action: "session.model.read"}); err != nil {
		t.Fatal(err)
	}
	result, err := r.Spawn(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Payload["agent_id"] != "remote-hermes" || result.Status != "idle" {
		t.Fatalf("lost native identity/status after control: %+v", result)
	}
	child.status = "waiting_approval"
	if r.Sessions()["owned"].Status != "waiting_approval" {
		t.Fatal("remote native activity lost")
	}
	if _, ok := r.Sessions()["foreign"]; ok {
		t.Fatal("foreign child snapshot escaped ownership")
	}
}
