package protocol

import (
	"context"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

type compactionDeadlineRuntime struct {
	fixtureRuntime
	remaining time.Duration
}

func (a *compactionDeadlineRuntime) Command(ctx context.Context, r core.Request) (core.Result, error) {
	deadline, ok := ctx.Deadline()
	if ok {
		a.remaining = time.Until(deadline)
	}
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"completed": true}}, nil
}

func TestHermesCompactionHasTimeToFinishNativeSummary(t *testing.T) {
	for _, tc := range []struct {
		name, kind, action, text string
		attachments              any
		long                     bool
	}{
		{name: "compress", kind: "hermes", action: "session.send", text: "/compress", long: true},
		{name: "compact alias with instructions", kind: "hermes", action: "session.send", text: " /compact preserve decisions ", long: true},
		{name: "remote compress", kind: "ssh", action: "session.send", text: "/compress", long: true},
		{name: "typed compact", kind: "hermes", action: "session.compact", long: true},
		{name: "ordinary prompt", kind: "hermes", action: "session.send", text: "hello"},
		{name: "different slash", kind: "hermes", action: "session.send", text: "/status"},
		{name: "prefix only", kind: "hermes", action: "session.send", text: "/compress-more"},
		{name: "attached prompt", kind: "hermes", action: "session.send", text: "/compress", attachments: []any{map[string]any{"path": "fixture"}}},
		{name: "codex prompt", kind: "codex", action: "session.send", text: "/compress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := New("fixture-key")
			defer d.Close()
			a := &compactionDeadlineRuntime{}
			d.RegisterRuntime(tc.kind, a)
			created := d.ExecuteSessionControl(context.Background(), map[string]any{"action": "session.create", "agent_type": tc.kind})
			if created["action"] == "error" {
				t.Fatal(created)
			}
			// Official Send requests omit agent_type: resolve the actual owner.
			request := map[string]any{"action": tc.action, "session_id": "created", "text": tc.text}
			if tc.attachments != nil {
				request["attachments"] = tc.attachments
			}
			response := d.ExecuteSessionControl(context.Background(), request)
			if response["action"] == "error" {
				t.Fatal(response)
			}
			if tc.long && a.remaining <= 10*time.Minute {
				t.Fatalf("native compression can take 10 minutes; control expires after %v", a.remaining)
			}
			if !tc.long && (a.remaining <= 0 || a.remaining > 30*time.Second) {
				t.Fatalf("ordinary command deadline changed: %v", a.remaining)
			}
		})
	}
}

func TestHermesCompactionRespectsEarlierCallerDeadline(t *testing.T) {
	d := New("fixture-key")
	defer d.Close()
	a := &compactionDeadlineRuntime{}
	d.RegisterRuntime("hermes", a)
	d.ExecuteSessionControl(context.Background(), map[string]any{"action": "session.create", "agent_type": "hermes"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response := d.ExecuteSessionControl(ctx, map[string]any{"action": "session.send", "session_id": "created", "text": "/compress"})
	if response["action"] == "error" || a.remaining <= 0 || a.remaining > time.Second {
		t.Fatalf("caller deadline was lost: %v, remaining=%v", response, a.remaining)
	}
}
