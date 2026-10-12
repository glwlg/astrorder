package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestActivityDirHonoursHermesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	if got, want := ActivityDir(), filepath.Join(home, "astrorder-observer", "events"); got != want {
		t.Fatalf("ActivityDir = %q, want %q", got, want)
	}
}

func TestActivityObserverReportsHermesSessionWithoutStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	dir := ActivityDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	emit := func(sid, ev string, payload map[string]any, status string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, fmt.Sprintf("%s|%s|%s|%v", sid, ev, status, payload["agent_id"]))
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go StartActivityObserver(ctx, "hermes-local", emit, func(string) bool { return true }, 10*time.Millisecond)
	time.Sleep(40 * time.Millisecond)

	id := fmt.Sprintf("%032x", 7)
	data, _ := json.Marshal(map[string]any{"id": id, "event": "UserPromptSubmit", "session_id": "20261011_x", "observed_at": float64(time.Now().Unix())})
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] != "20261011_x|native.activity||hermes-local" {
		t.Fatalf("calls = %v", calls)
	}
}
