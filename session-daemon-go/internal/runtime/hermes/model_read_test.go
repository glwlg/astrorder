package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"testing"
	"time"
)

func TestModelReadDoesNotLoadLargeTranscript(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}, AgentID: "test-hermes"})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := a.Spawn(ctx, core.Request{SessionID: "stored-large-transcript"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		model, err := a.Command(ctx, core.Request{SessionID: r.SessionID, Action: "session.model.read"})
		if err != nil {
			t.Fatalf("model read killed native transport: %v", err)
		}
		if model.Payload["model"] != "test-model" {
			t.Fatalf("bad model: %+v", model)
		}
	}
}
