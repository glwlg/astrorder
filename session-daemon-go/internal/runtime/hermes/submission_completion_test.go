package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestCanonicalCompletionUsesExactSubmission(t *testing.T) {
	var completions []map[string]any
	a := New(Config{AgentID: "a", Emit: func(_, event string, p map[string]any, _ string) error {
		if event == "hermes.command_complete" {
			completions = append(completions, p)
		}
		return nil
	}})
	a.canonical = true
	s := &SessionState{SessionID: "s", Handle: "s", Status: "idle", Metadata: map[string]any{}}
	a.sessions["s"] = s
	r, w := io.Pipe()
	a.stdin = w
	a.processDone = make(chan struct{})
	defer r.Close()
	defer w.Close()
	go func() {
		var req map[string]any
		if json.NewDecoder(r).Decode(&req) != nil {
			return
		}
		p := req["params"].(map[string]any)
		if p["submission_id"] != "owned-command" {
			t.Errorf("native admission lost exact submission ID: %v", p["submission_id"])
		}
		// A different source's previous turn finishes while this send awaits admission.
		a.handleGatewayEvent(map[string]any{"type": "message.complete", "session_id": "s", "payload": map[string]any{"persisted_turn": map[string]any{"submission_id": "foreign"}}})
		a.mu.RLock()
		pending := a.pending[req["id"].(string)]
		a.mu.RUnlock()
		pending.ch <- &rpcMessage{Result: map[string]any{"accepted": true}}

	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := a.handleSend(ctx, s, core.Request{Fields: map[string]any{"command_id": "owned-command", "text": "fixture"}}); err != nil {
		t.Fatal(err)
	}
	if len(completions) != 1 || completions[0]["command_id"] != nil || s.CommandID != "owned-command" || s.Status != "running" {
		t.Fatalf("foreign completion stole pending command: events=%v command=%s status=%s", completions, s.CommandID, s.Status)
	}
	a.handleGatewayEvent(map[string]any{"type": "message.complete", "session_id": "s", "payload": map[string]any{"persisted_turn": map[string]any{"submission_id": "owned-command"}}})
	if len(completions) != 2 || completions[1]["command_id"] != "owned-command" || s.CommandID != "" || s.Status != "idle" {
		t.Fatalf("owned completion lost exact identity: %v command=%s status=%s", completions, s.CommandID, s.Status)
	}
}
