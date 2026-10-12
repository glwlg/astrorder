package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/codex"
)

func TestDesktopStopObserverForwardsStops(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("CODEX_HOME", tempDir)

	eventsDir := filepath.Join(tempDir, "astrorder-observer", "events")
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var calls []string
	emit := func(sid, ev string, payload map[string]any, status string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, fmt.Sprintf("%s:%s:%s", sid, ev, status))
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go codex.StartDesktopStopObserver(ctx, codex.Config{AgentID: "codex-test"}, emit, 10*time.Millisecond)

	time.Sleep(30 * time.Millisecond)

	eventID := strings.Repeat("a", 32)
	eventFile := filepath.Join(eventsDir, eventID+".json")
	data, _ := json.Marshal(map[string]any{
		"id":          eventID,
		"event":       "Stop",
		"session_id":  "thread-desktop-1",
		"turn_id":     "turn-1",
		"observed_at": float64(time.Now().Unix()),
	})
	if err := os.WriteFile(eventFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		mu.Lock()
		if len(calls) > 0 {
			found = true
			mu.Unlock()
			break
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}

	if !found {
		t.Fatalf("expected forward_codex_desktop_stops event to be emitted")
	}

	mu.Lock()
	defer mu.Unlock()
	if calls[0] != "thread-desktop-1:codex.notification:idle" {
		t.Fatalf("unexpected emitted call: %s", calls[0])
	}
}

func writeSpoolEvent(t *testing.T, dir string, n int, event, session string) {
	t.Helper()
	id := fmt.Sprintf("%032x", n)
	data, _ := json.Marshal(map[string]any{
		"id": id, "event": event, "session_id": session,
		"observed_at": float64(time.Now().Unix()),
	})
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopObserverForwardsActivityWithoutOverridingStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	dir := filepath.Join(home, "astrorder-observer", "events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	emit := func(sid, ev string, _ map[string]any, status string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, fmt.Sprintf("%s|%s|%s", sid, ev, status))
		return nil
	}
	known := func(id string) bool { return id == "tracked" }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go codex.StartDesktopObserver(ctx, codex.Config{AgentID: "codex-test"}, emit, known, 10*time.Millisecond)
	time.Sleep(40 * time.Millisecond)

	writeSpoolEvent(t, dir, 1, "UserPromptSubmit", "tracked")
	writeSpoolEvent(t, dir, 2, "UserPromptSubmit", "stranger")
	// Tool hooks for the same session inside the throttle window collapse to one.
	writeSpoolEvent(t, dir, 3, "PreToolUse", "tracked")
	writeSpoolEvent(t, dir, 4, "PostToolUse", "tracked")
	writeSpoolEvent(t, dir, 5, "Stop", "tracked")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	want := map[string]int{
		"tracked|native.activity|":        1, // empty status: never overrides running/waiting
		"stranger|native.activity|idle":   1, // untracked session registers as idle
		"tracked|codex.notification|idle": 1, // Stop keeps its old behaviour
	}
	got := map[string]int{}
	for _, c := range calls {
		got[c]++
	}
	// The prompt arrives first and starts the throttle window; tool hooks are dropped.
	for key, count := range want {
		if got[key] != count {
			t.Fatalf("calls = %v, want %q x%d", calls, key, count)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %v, want exactly 3", calls)
	}
}
