package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"testing"
)

func TestQueuedSendNeverSteersAnActiveTurn(t *testing.T) {
	a := New(Config{})
	s := &SessionState{SessionID: "s", Handle: "h", Status: "running", CommandID: "original", Metadata: map[string]any{}}
	a.sessions["s"] = s
	_, err := a.handleSend(context.Background(), s, core.Request{Fields: map[string]any{"text": "queued", "params": map[string]any{"idle_only": true}}})
	if err == nil || err.Error() != "queued send requires an idle session" {
		t.Fatalf("queued send crossed into guidance: %v", err)
	}
	if s.CommandID != "original" || s.sending {
		t.Fatal("queue changed active command")
	}
}
