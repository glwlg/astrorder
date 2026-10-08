package events

import "testing"

func TestFullSubscriberIsDisconnectedInsteadOfDroppingFrames(t *testing.T) {
	journal := New(4)
	subscriber := journal.SubscribeWithCapacity(1)
	journal.Publish("session-1", "one", map[string]any{}, "running")
	journal.Publish("session-1", "two", map[string]any{}, "running")

	if subscriber.Healthy() {
		t.Fatal("a subscriber that cannot keep up remained connected after a frame would have been dropped")
	}
	replay := journal.After("session-1", 0)
	if len(replay) != 2 || replay[0].SeqID != 1 || replay[1].SeqID != 2 {
		t.Fatalf("disconnect did not preserve replayable frames: %+v", replay)
	}
}
