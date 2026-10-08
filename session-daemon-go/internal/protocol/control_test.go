package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"testing"
)

type fixtureRuntime struct{}

func (fixtureRuntime) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "created", Status: "idle", Payload: map[string]any{"session_id": "created", "status": "idle"}}, nil
}
func (fixtureRuntime) Spawn(_ context.Context, r core.Request) (core.Result, error) {
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"status": "idle"}}, nil
}
func (fixtureRuntime) Command(_ context.Context, r core.Request) (core.Result, error) {
	return core.Result{SessionID: r.SessionID, Status: "running", Payload: map[string]any{"accepted": true, "status": "running"}}, nil
}
func TestControlRoutesCreateSpawnAndOwnedCommands(t *testing.T) {
	d := New("key")
	d.RegisterRuntime("test", fixtureRuntime{})
	auth := true
	response, _ := d.handle([]byte(`{"action":"session.create","agent_type":"test","request_id":"c","params":{}}`), &auth)
	if response["action"] != "session.create.result" || response["session_id"] != "created" {
		t.Fatal(response)
	}
	synced := d.sync(map[string]any{"sessions": map[string]any{"created": float64(0)}})
	replay, ok := synced["sessions"].(map[string]any)["created"].(map[string]any)
	if !ok || replay["status"] != "idle" || len(replay["frames"].([]map[string]any)) != 0 {
		t.Fatal("empty runtime session missing from replay", synced)
	}
	response, _ = d.handle([]byte(`{"action":"session.send","session_id":"created","prompt":"hello","request_id":"s"}`), &auth)
	if response["action"] != "session.send.result" || response["request_id"] != "s" {
		t.Fatal(response)
	}
	status := d.status("r")
	sessions := status["sessions"].(map[string]any)
	if sessions["created"].(map[string]any)["status"] != "running" {
		t.Fatal("status ignored runtime ownership", status)
	}
	if d.shutdown(map[string]any{})["action"] != "error" {
		t.Fatal("shutdown bypassed native activity")
	}
	for _, raw := range []string{`{"action":"session.send","session_id":"foreign"}`, `{"action":"session.create","agent_type":"missing"}`, `{"action":"session.spawn","agent_type":"test","session_id":"created","params":[]}`} {
		response, _ = d.handle([]byte(raw), &auth)
		if response["action"] != "error" {
			t.Fatal("bad control accepted", response)
		}
	}
}
