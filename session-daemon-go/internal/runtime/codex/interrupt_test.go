package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/codex"
)

func TestCodexInterruptDoesNotWaitForTurnCompletion(t *testing.T) {
	if os.Getenv("ASTRORDER_FAKE_CODEX_TURN") == "1" {
		fakeTurn()
		return
	}
	root := t.TempDir()
	runtime := codex.New(codex.Config{
		Executable: os.Args[0], Arguments: []string{"-test.run=TestCodexInterruptDoesNotWaitForTurnCompletion"},
		Workspace: root, Allowed: []string{root}, AgentID: "local-codex",
	})
	created, err := runtime.Create(context.Background(), codex.Request{Cwd: root, Env: []string{"ASTRORDER_FAKE_CODEX_TURN=1"}})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = runtime.Command(context.Background(), codex.Command{Action: "session.send", SessionID: created.SessionID, Text: "long task"})
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	result, err := runtime.Command(ctx, codex.Command{Action: "session.interrupt", SessionID: created.SessionID, TurnID: "turn-1"})
	if err != nil || result.Status != "idle" {
		t.Fatalf("interrupt did not return promptly: result=%+v err=%v", result, err)
	}
	runtime.Close()
}

func fakeTurn() {
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
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"thread": map[string]any{"id": "thread-1"}}})
		case "turn/start":
			go func(id any) {
				time.Sleep(2 * time.Second)
				_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn-1"}}})
			}(request["id"])
		case "turn/interrupt":
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"status": "interrupted"}})
		}
	}
}
