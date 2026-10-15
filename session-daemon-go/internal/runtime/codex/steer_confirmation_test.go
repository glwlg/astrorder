package codex

import (
	"context"
	"strings"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestSteerRejectsUnconfirmedNativeTurnWithoutAnotherSend(t *testing.T) {
	for _, reply := range []map[string]any{{}, {"turnId": "other-turn"}} {
		rt := New(Config{})
		rt.setSession("thread", "running")
		rt.activeTurnID = "original-turn"
		frames := make(chan map[string]any, 2)
		rt.stdin = captureWriter{frames}
		a := NewAdapter(Config{}, nil)
		a.sessions["thread"] = &adapterSession{runtime: rt}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		go func() {
			select {
			case request := <-frames:
				id := int(request["id"].(float64))
				rt.mu.Lock()
				waiter := rt.pending[id]
				delete(rt.pending, id)
				rt.mu.Unlock()
				waiter.response <- reply
			case <-ctx.Done():
			}
		}()
		_, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "follow-up"}})
		if err == nil || !strings.Contains(err.Error(), "acknowledgement") {
			t.Errorf("native reply incorrectly accepted: reply=%v err=%v", reply, err)
		}
		if len(frames) != 0 {
			t.Error("uncertain guidance automatically retried")
		}
		cancel()
		a.Close()
	}
}

func TestActiveCodexWithoutNativeTurnDoesNotGuessSteerTarget(t *testing.T) {
	rt := New(Config{})
	defer rt.Close()
	rt.setSession("thread", "running")
	frames := make(chan map[string]any, 2)
	rt.stdin = captureWriter{frames}
	a := NewAdapter(Config{}, nil)
	a.sessions["thread"] = &adapterSession{runtime: rt}
	defer a.Close()
	_, err := a.Command(context.Background(), core.Request{Action: "session.send", SessionID: "thread", Fields: map[string]any{"prompt": "follow-up"}})
	if err == nil || len(frames) != 0 {
		t.Fatalf("missing native turn caused IO: err=%v frames=%d", err, len(frames))
	}
}
