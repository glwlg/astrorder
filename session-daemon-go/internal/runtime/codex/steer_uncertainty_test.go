package codex

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestWaitingGuidanceDoesNotStartTurnAfterUncertainStart(t *testing.T) {
	rt := New(Config{})
	defer rt.Close()
	rt.setSession("thread", "idle")
	frames := make(chan map[string]any, 4)
	rt.stdin = captureWriter{frames}
	a := NewAdapter(Config{}, nil)
	a.sessions["thread"] = &adapterSession{runtime: rt}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() {
		_, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "initial"}})
		first <- err
	}()
	start := <-frames
	guide := make(chan error, 1)
	go func() {
		_, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "guidance"}})
		guide <- err
	}()
	select {
	case err := <-guide:
		t.Fatalf("did not wait for previous operation: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	id := int(start["id"].(float64))
	rt.mu.Lock()
	waiter := rt.pending[id]
	delete(rt.pending, id)
	rt.mu.Unlock()
	waiter.err <- fmt.Errorf("lost start receipt")
	if err := <-first; err == nil {
		t.Fatal("start unexpectedly confirmed")
	}
	select {
	case err := <-guide:
		if err == nil || !strings.Contains(err.Error(), "unconfirmed") {
			t.Fatalf("uncertain initial outcome not retained: %v", err)
		}
	case next := <-frames:
		t.Fatalf("guidance retried native IO after uncertain start: %v", next)
	case <-ctx.Done():
		t.Fatal("waiting guidance did not finish")
	}
}
