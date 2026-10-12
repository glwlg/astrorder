package runtime

import (
	"context"
	"testing"
)

type restartScopeAdapter struct{ closed bool }

func (a *restartScopeAdapter) Create(_ context.Context, r Request) (Result, error) {
	return Result{SessionID: r.SessionID, Status: "idle"}, nil
}
func (a *restartScopeAdapter) Spawn(c context.Context, r Request) (Result, error) {
	return a.Create(c, r)
}
func (a *restartScopeAdapter) Close() error { a.closed = true; return nil }
func TestDisconnectDebianPreservesWSLAdapterAndOwnership(t *testing.T) {
	children := map[string]*restartScopeAdapter{}
	multi := NewMultiplexRegistry(func(id string, _ map[string]any) (Adapter, error) {
		a := &restartScopeAdapter{}
		children[id] = a
		return a, nil
	})
	reg := NewRegistry()
	reg.Register("codex-ssh", multi)
	ctx := context.Background()
	request := func(id string) Request {
		return Request{AgentType: "codex-ssh", SessionID: "session-" + id, Fields: map[string]any{"params": map[string]any{"connection_id": id, "ssh_settings": map[string]any{"host": id}}}}
	}
	for _, id := range []string{"debian", "wsl"} {
		if _, err := reg.Spawn(ctx, request(id)); err != nil {
			t.Fatal(err)
		}
	}
	wsl := children["wsl"]
	released, err := reg.DisconnectRuntime(ctx, "codex-ssh", "debian")
	if err != nil {
		t.Fatal(err)
	}
	if len(released) != 1 || released[0] != "session-debian" {
		t.Fatalf("released %v", released)
	}
	if !children["debian"].closed || wsl.closed {
		t.Fatal("wrong adapter closed")
	}
	if _, ok := reg.Sessions()["session-wsl"]; !ok {
		t.Fatal("WSL ownership lost")
	}
	if _, err := reg.Spawn(ctx, request("debian")); err != nil {
		t.Fatal(err)
	}
	if children["wsl"] != wsl || wsl.closed {
		t.Fatal("restart replaced WSL adapter")
	}
}
