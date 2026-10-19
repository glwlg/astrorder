package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"errors"
	"testing"
)

func TestExitedHermesViewerReadSignalsReattachment(t *testing.T) {
	for _, retired := range []bool{false, true} {
		a := New(Config{})
		a.sessions["s"] = &SessionState{SessionID: "s", Status: "error", Metadata: map[string]any{}}
		a.failed = true
		a.processDone = make(chan struct{})
		close(a.processDone)
		if retired {
			close(a.eventDone)
		}
		_, err := a.Command(context.Background(), core.Request{SessionID: "s", Action: "session.model.read"})
		if errors.Is(err, core.ErrSessionNotOwned) != retired {
			t.Fatalf("retired=%v error=%v", retired, err)
		}
	}
}
