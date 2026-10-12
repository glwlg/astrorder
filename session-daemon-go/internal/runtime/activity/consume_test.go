package activity

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func streamEvent(id, session, event string) string {
	return fmt.Sprintf(`{"id":%q,"session_id":%q,"event":%q,"observed_at":%d}`+"\n", id, session, event, time.Now().Unix())
}

func TestConsumeForwardsValidEventsAndSkipsBadLines(t *testing.T) {
	a := strings.Repeat("a", 32)
	b := strings.Repeat("b", 32)
	input := strings.Join([]string{
		"not json\n",
		streamEvent(a, "s1", "UserPromptSubmit"),
		streamEvent(a, "s1", "UserPromptSubmit"), // duplicate id
		`{"id":"short","session_id":"s2","event":"Stop","observed_at":1}` + "\n",
		strings.Repeat("x", maxStreamLine*3) + "\n", // oversized line must not wedge the reader
		streamEvent(b, "s3", "SessionStart"),
	}, "")
	var mu sync.Mutex
	var got []string
	err := Consume(strings.NewReader(input), Options{
		AgentID: "agent",
		Known:   func(id string) bool { return id == "s1" },
		Emit: func(session, event string, _ map[string]any, status string) error {
			mu.Lock()
			got = append(got, session+":"+event+":"+status)
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"s1:native.activity:", "s3:native.activity:idle"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v (tracked sessions keep an empty status; unknown ones register idle)", got, want)
	}
}

func TestConsumeRoutesStopToOnStop(t *testing.T) {
	var stopped []string
	err := Consume(strings.NewReader(streamEvent(strings.Repeat("c", 32), "s9", "Stop")), Options{
		OnStop: func(session, turn, _ string) { stopped = append(stopped, session+"/"+turn) },
		Emit: func(string, string, map[string]any, string) error {
			t.Fatal("Stop must not become plain activity")
			return nil
		},
		Known: func(string) bool { return true },
	})
	if err != nil || len(stopped) != 1 || stopped[0] != "s9/"+strings.Repeat("c", 32) {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
}

func TestConsumeIgnoresStaleEvents(t *testing.T) {
	stale := fmt.Sprintf(`{"id":%q,"session_id":"s","event":"Stop","observed_at":%d}`+"\n", strings.Repeat("d", 32), time.Now().Add(-time.Hour).Unix())
	called := false
	_ = Consume(strings.NewReader(stale), Options{
		OnStop: func(string, string, string) { called = true },
		Emit:   func(string, string, map[string]any, string) error { called = true; return nil },
		Known:  func(string) bool { return true },
	})
	if called {
		t.Fatal("events older than five minutes must be dropped: a replayed Stop would mark a session idle")
	}
}
