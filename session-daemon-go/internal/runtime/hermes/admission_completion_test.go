package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestCanonicalCompletionWithoutPersistedRowsUsesAdmission(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(fmt.Sprint(early), func(t *testing.T) {
			var commandID string
			a := New(Config{AgentID: "a", Emit: func(_, event string, p map[string]any, _ string) error {
				if event == "hermes.command_complete" && p["command_id"] != nil {
					commandID, _ = p["command_id"].(string)
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
			complete := map[string]any{"type": "message.complete", "session_id": "s", "payload": map[string]any{"admission_id": "admission", "outcome": "failed"}}
			go func() {
				var req map[string]any
				if json.NewDecoder(r).Decode(&req) != nil {
					return
				}
				if early {
					a.handleGatewayEvent(complete)
				}
				a.mu.RLock()
				pending := a.pending[req["id"].(string)]
				a.mu.RUnlock()
				pending.ch <- &rpcMessage{Result: map[string]any{"accepted": true, "admission_id": "admission"}}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := a.handleSend(ctx, s, core.Request{Fields: map[string]any{"command_id": "exact", "text": "fixture"}}); err != nil {
				t.Fatal(err)
			}
			if !early {
				a.handleGatewayEvent(complete)
			}
			if commandID != "exact" || s.CommandID != "" || s.Status != "idle" {
				t.Fatalf("terminal admission stranded command: id=%s state=%+v", commandID, s)
			}
		})
	}
}
