package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/codex"
)

func TestCodexCreatePublishesLocalOwnerPID(t *testing.T) {
	if os.Getenv("ASTRORDER_FAKE_CODEX_OWNER") == "1" {
		fakeCodexOwner()
		return
	}
	root := t.TempDir()
	var ownerPID int
	var ownerStatus, ownerSession string
	adapter := codex.NewAdapter(codex.Config{
		Executable:  os.Args[0],
		Arguments:   []string{"-test.run=TestCodexCreatePublishesLocalOwnerPID"},
		Environment: []string{"ASTRORDER_FAKE_CODEX_OWNER=1"},
		Workspace:   root,
		Allowed:     []string{root},
	}, func(sessionID, event string, payload map[string]any, status string) error {
		if event != "runtime.owner" {
			return nil
		}
		ownerSession = sessionID
		ownerStatus = "recorded"
		ownerPID, _ = payload["owner_pid"].(int)
		return nil
	})
	defer adapter.Close()
	created, err := adapter.Create(context.Background(), core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	if ownerSession != created.SessionID || ownerStatus != "recorded" || !process.Alive(ownerPID) {
		t.Fatalf("local owner was not published: session=%q status=%q pid=%d created=%+v", ownerSession, ownerStatus, ownerPID, created)
	}
}

func fakeCodexOwner() {
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
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"thread": map[string]any{"id": "owner-thread"}}})
		}
	}
}
