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
