package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/codex"
)

func TestCodexRuntimeInitializesOnlyInsideAllowedWorkspace(t *testing.T) {
	if os.Getenv("ASTRORDER_FAKE_CODEX") == "1" {
		fakeCodex()
		return
	}
	root := t.TempDir()
	runtime := codex.New(codex.Config{
		Executable: os.Args[0], Arguments: []string{"-test.run=TestCodexRuntimeInitializesOnlyInsideAllowedWorkspace"},
		Workspace: root, Allowed: []string{root}, AgentID: "local-codex",
	})
	outside := t.TempDir()
	if _, err := runtime.Create(context.Background(), codex.Request{Cwd: outside}); err == nil {
		t.Fatal("workspace outside the allowlist was accepted")
	}
	created, err := runtime.Create(context.Background(), codex.Request{Cwd: root, Env: []string{"ASTRORDER_FAKE_CODEX=1"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "idle" || created.SessionID == "" {
		t.Fatalf("codex thread was not created: %+v", created)
	}
	runtime.Close()
}

func fakeCodex() {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	var request map[string]any
	if err := decoder.Decode(&request); err != nil || request["method"] != "initialize" {
		os.Exit(2)
	}
	_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"userAgent": "fake-codex"}})
	if err := decoder.Decode(&request); err != nil || request["method"] != "initialized" {
		os.Exit(3)
	}
	if err := decoder.Decode(&request); err != nil || request["method"] != "thread/start" {
		os.Exit(4)
	}
	_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"thread": map[string]any{"id": "thread-1"}}})
	time.Sleep(2 * time.Second)
}
