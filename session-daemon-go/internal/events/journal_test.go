package events_test

import (
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/events"
)

func TestSlowSubscriberDoesNotBlockAnotherSession(t *testing.T) {
	journal := events.New(8)
	slow := journal.Subscribe()
	fast := journal.Subscribe()
	defer journal.Unsubscribe(slow)
	defer journal.Unsubscribe(fast)

	done := make(chan events.Frame, 1)
	go func() {
		done <- journal.Publish("session-fast", "token", map[string]any{"n": 1}, "running")
	}()
	select {
	case frame := <-done:
		if frame.SeqID != 1 || frame.SessionID != "session-fast" {
			t.Fatalf("unexpected frame: %+v", frame)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("slow subscriber blocked publication for another session")
	}

	select {
	case frame := <-fast.Frames():
		if frame.SessionID != "session-fast" {
			t.Fatalf("fast subscriber received wrong frame: %+v", frame)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("healthy subscriber did not receive the frame")
	}
	if got := len(slow.Frames()); got != 1 {
		t.Fatalf("slow subscriber queue length = %d, want 1", got)
	}
}

func TestReplayContinuesWithoutLosingFramesPublishedDuringSync(t *testing.T) {
	journal := events.New(8)
	first := journal.Publish("session-1", "started", map[string]any{}, "running")
	subscription := journal.Subscribe()
	defer journal.Unsubscribe(subscription)
	replay := journal.After("session-1", 0)
	second := journal.Publish("session-1", "token", map[string]any{"text": "continued"}, "running")

	if len(replay) != 1 || replay[0].SeqID != first.SeqID {
		t.Fatalf("replay did not capture the checkpoint: %+v", replay)
	}
	select {
	case frame := <-subscription.Frames():
		if frame.SeqID != second.SeqID {
			t.Fatalf("live continuation skipped or duplicated: %+v", frame)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("frame published during sync was lost")
	}
}
