package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"testing"
)

type grokRegistrationRuntime struct{}

func (grokRegistrationRuntime) Spawn(_ context.Context, r core.Request) (core.Result, error) {
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"runtime_control": true, "control_state": "owned", "agent_id": "ssh-grok-saved", "name": "Debian · Grok", "connection_id": "saved", "runtime_id": "ssh-grok-saved", "source_id": "ssh-grok-saved"}}, nil
}
func (grokRegistrationRuntime) Create(context.Context, core.Request) (core.Result, error) {
	panic("unexpected create")
}
func TestGrokHelloReannouncementKeepsRemoteIdentity(t *testing.T) {
	d := New("key")
	d.RegisterRuntime("grok-ssh", grokRegistrationRuntime{})
	auth := true
	for i := 0; i < 2; i++ {
		result, _ := d.handle([]byte(`{"action":"session.spawn","agent_type":"grok-ssh","session_id":"probe-grok-saved","params":{"runtime_control":true,"connection_id":"saved"}}`), &auth)
		if result["action"] == "error" {
			t.Fatal(result)
		}
	}
	synced := d.sync(map[string]any{"sessions": map[string]any{"probe-grok-saved": float64(0)}})
	frames := synced["sessions"].(map[string]any)["probe-grok-saved"].(map[string]any)["frames"].([]map[string]any)
	if len(frames) != 2 {
		t.Fatalf("missing reannouncement: %v", frames)
	}
	for _, frame := range frames {
		payload := frame["payload"].(map[string]any)
		for key, want := range map[string]string{"id": "ssh-grok-saved", "connection_id": "saved", "runtime_id": "ssh-grok-saved", "source_id": "ssh-grok-saved", "name": "Debian · Grok"} {
			if payload[key] != want {
				t.Fatalf("hello lost %s: %v", key, payload)
			}
		}
	}
}
