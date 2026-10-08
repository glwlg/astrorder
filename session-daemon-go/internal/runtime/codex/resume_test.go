package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"astrorder.dev/session-daemon/internal/runtime/codex"
)

func TestCodexRuntimeResumesNativeThread(t *testing.T) {
	if os.Getenv("ASTRORDER_FAKE_CODEX_RESUME") == "1" {
		fakeResume()
		return
	}
	root := t.TempDir()
	runtime := codex.New(codex.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestCodexRuntimeResumesNativeThread"},
		Workspace:  root,
		Allowed:    []string{root},
	})
	resumed, err := runtime.Resume(context.Background(), codex.Request{
		Cwd:       root,
		SessionID: "native-thread-42",
		Env:       []string{"ASTRORDER_FAKE_CODEX_RESUME=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SessionID != "native-thread-42" || resumed.Status != "idle" {
		t.Fatalf("unexpected resume result: %+v", resumed)
	}
	runtime.Close()
}

func fakeResume() {
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
		case "thread/resume":
			params, _ := request["params"].(map[string]any)
			if params["threadId"] != "native-thread-42" || params["excludeTurns"] != true {
				os.Exit(5)
			}
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"thread": map[string]any{"id": "native-thread-42"}}})
		}
	}
}
