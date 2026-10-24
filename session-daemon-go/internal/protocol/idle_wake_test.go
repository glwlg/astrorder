package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"testing"
)

type wakeEventRuntime struct {
	emit func(string, string, map[string]any, string) error
}

func (a *wakeEventRuntime) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "wake", Status: "idle", Payload: map[string]any{}}, nil
}
func (a *wakeEventRuntime) Spawn(_ context.Context, r core.Request) (core.Result, error) {
	if err := a.emit(r.SessionID, "codex.notification", map[string]any{"method": "thread/started"}, "idle"); err != nil {
		return core.Result{}, err
	}
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{}}, nil
}
func (a *wakeEventRuntime) Command(_ context.Context, r core.Request) (core.Result, error) {
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"closed": r.Action == "session.close", "deleted": func() string {
		if r.Action == "session.delete" {
			return r.SessionID
		}
		return ""
	}()}}, nil
}
func (*wakeEventRuntime) Close() {}
func TestLegacyRetiredSessionExplicitAttachAcceptsStartupEvents(t *testing.T) {
	d := New("fixture-key")
	defer d.Close()
	d.RegisterRuntime("codex", &wakeEventRuntime{emit: d.Emit})
	if err := d.journal.TrackSession("wake", "idle"); err != nil {
		t.Fatal(err)
	}
	if err := d.journal.ForgetSession("wake"); err != nil {
		t.Fatal(err)
	}
	result := d.ExecuteSessionControl(context.Background(), map[string]any{"action": "session.spawn", "agent_type": "codex", "session_id": "wake"})
	if result["action"] == "error" {
		t.Fatal(result)
	}
}

func TestIdleCloseAllowsWakeEventsBeforeAttachCompletes(t *testing.T) {
	d := New("fixture-key")
	defer d.Close()
	d.RegisterRuntime("codex", &wakeEventRuntime{emit: d.Emit})
	ctx := context.Background()
	for _, request := range []map[string]any{
		{"action": "session.create", "agent_type": "codex"},
		{"action": "session.close", "session_id": "wake"},
		{"action": "session.spawn", "agent_type": "codex", "session_id": "wake"},
		{"action": "session.close", "session_id": "wake"},
		{"action": "session.spawn", "agent_type": "codex", "session_id": "wake"},
		{"action": "session.delete", "session_id": "wake"},
	} {
		if result := d.ExecuteSessionControl(ctx, request); result["action"] == "error" {
			t.Fatalf("%v: %v", request, result)
		}
	}
	if err := d.Emit("wake", "late", nil, "idle"); err == nil {
		t.Fatal("deleted session accepted late event")
	}
}
