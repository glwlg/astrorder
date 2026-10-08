package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/codex"
)

func TestCodexNotificationsPreserveTransportOrder(t *testing.T) {
	if os.Getenv("ASTRORDER_FAKE_CODEX_ORDER") == "1" {
		fakeOrderedNotifications()
		return
	}
	root := t.TempDir()
	methods := make(chan string, 2)
	runtime := codex.New(codex.Config{
		Executable: os.Args[0], Arguments: []string{"-test.run=TestCodexNotificationsPreserveTransportOrder"},
		Workspace: root, Allowed: []string{root},
		OnNotification: func(notification codex.Notification) {
			if notification.Method == "turn/started" {
				time.Sleep(50 * time.Millisecond)
			}
			methods <- notification.Method
		},
	})
	created, err := runtime.Create(context.Background(), codex.Request{Cwd: root, Env: []string{"ASTRORDER_FAKE_CODEX_ORDER=1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	_ = created
	first, second := <-methods, <-methods
	if first != "turn/started" || second != "turn/completed" {
		t.Fatalf("notifications were reordered: %q then %q", first, second)
	}
}

func fakeOrderedNotifications() {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}
		switch request["method"] {
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{}})
		case "thread/start":
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"thread": map[string]any{"id": "ordered-thread"}}})
			_ = encoder.Encode(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": "ordered-thread", "turn": map[string]any{"id": "ordered-turn"}}})
			_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "ordered-thread", "turn": map[string]any{"id": "ordered-turn", "status": "completed"}}})
		}
	}
}
