package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"testing"
	"time"
)

func TestSessionCloseRequiresIdleAndConfirmedExit(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		t.Run(map[bool]string{true: "confirmed", false: "unconfirmed"}[confirmed], func(t *testing.T) {
			rt := New(Config{})
			defer rt.Close()
			rt.mu.Lock()
			rt.status = "idle"
			rt.sessionID = "summary"
			rt.processDone = make(chan struct{})
			rt.exitConfirmed = confirmed
			close(rt.processDone)
			rt.mu.Unlock()
			a := NewAdapter(Config{}, nil)
			a.sessions["summary"] = &adapterSession{runtime: rt}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := a.Command(ctx, core.Request{SessionID: "summary", Action: "session.close"})
			if confirmed {
				if err != nil || result.Payload["closed"] != true {
					t.Fatalf("close: %v %v", result, err)
				}
				if len(a.Snapshot()) != 0 {
					t.Fatal("binding retained")
				}
			} else {
				if err == nil {
					t.Fatal("unknown remote exit was accepted")
				}
				if len(a.Snapshot()) != 1 {
					t.Fatal("unknown binding released")
				}
			}
		})
	}
}
