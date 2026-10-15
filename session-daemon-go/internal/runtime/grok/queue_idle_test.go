package grok

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"testing"
)

func TestQueuedSendRequiresIdleBeforeStartingPrompt(t *testing.T) {
	a := &Adapter{}
	owned := &ownedSession{status: "waiting_approval", promptRunning: true}
	_, err := a.handleSend(owned, "s", core.Request{Fields: map[string]any{"prompt": "queued", "command_id": "q", "params": map[string]any{"idle_only": true}}})
	if err == nil || err.Error() != "queued send requires an idle session" {
		t.Fatalf("admission: %v", err)
	}
	if owned.promptCancel != nil {
		t.Fatal("queued send started a prompt")
	}
}
