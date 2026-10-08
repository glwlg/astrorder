package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"testing"
)

type hermesControlFixture struct {
	fixtureRuntime
	daemon *Daemon
}

func (f hermesControlFixture) Spawn(_ context.Context, r core.Request) (core.Result, error) {
	if err := f.daemon.Emit(r.SessionID, "connector.hello", map[string]any{"id": "hermes-test", "status": "ready"}, ""); err != nil {
		return core.Result{}, err
	}
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"agent_id": "hermes-test", "name": "Debian · Hermes", "runtime_control": true, "source_id": "hermes-local-default", "profile_name": "default"}}, nil
}
func TestHermesControlReconnectRepublishesReadiness(t *testing.T) {
	d := New("key")
	defer d.Close()
	d.RegisterRuntime("hermes", hermesControlFixture{daemon: d})
	auth := true
	for i := 0; i < 2; i++ {
		response, _ := d.handle([]byte(`{"action":"session.spawn","agent_type":"hermes","session_id":"control","params":{"runtime_control":true}}`), &auth)
		if response["action"] != "session.spawn.result" {
			t.Fatal(response)
		}
	}
	synced := d.sync(map[string]any{"sessions": map[string]any{"control": float64(0)}})
	frames := synced["sessions"].(map[string]any)["control"].(map[string]any)["frames"].([]map[string]any)
	if len(frames) != 2 {
		t.Fatalf("expected a readiness frame for each App attach, got %d", len(frames))
	}
	if frames[1]["payload"].(map[string]any)["name"] != "Debian · Hermes" {
		t.Fatalf("reconnect lost remote connection name: %v", frames[1])
	}
}
