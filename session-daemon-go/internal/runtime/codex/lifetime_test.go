package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"astrorder.dev/session-daemon/internal/runtime/codex"
)

func TestCodexTransportOutlivesCreateContext(t *testing.T) {
	if os.Getenv("ASTRORDER_FAKE_CODEX_LIFETIME") == "1" {
		fakeLifetime()
		return
	}
	root := t.TempDir()
	runtime := codex.New(codex.Config{
		Executable: os.Args[0], Arguments: []string{"-test.run=TestCodexTransportOutlivesCreateContext"},
		Workspace: root, Allowed: []string{root},
	})
	ctx, cancel := context.WithCancel(context.Background())
	created, err := runtime.Create(ctx, codex.Request{Cwd: root, Env: []string{"ASTRORDER_FAKE_CODEX_LIFETIME=1"}})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	result, err := runtime.Command(context.Background(), codex.Command{
		Action: "session.send", SessionID: created.SessionID, Text: "still alive",
	})
	if err != nil || result.TurnID != "turn-after-cancel" {
		t.Fatalf("create context killed owned transport: result=%+v err=%v", result, err)
	}
	runtime.Close()
}

func fakeLifetime() {
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
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"thread": map[string]any{"id": "lifetime-thread"}}})
		case "turn/start":
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"turn": map[string]any{"id": "turn-after-cancel"}}})
		}
	}
}
