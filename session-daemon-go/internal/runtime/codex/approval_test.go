package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/codex"
)

func TestCodexApprovalRoutesNativeRequestID(t *testing.T) {
	if os.Getenv("ASTRORDER_FAKE_CODEX_APPROVAL") == "1" {
		fakeApproval()
		return
	}
	root := t.TempDir()
	notifications := make(chan codex.Notification, 1)
	runtime := codex.New(codex.Config{
		Executable:     os.Args[0],
		Arguments:      []string{"-test.run=TestCodexApprovalRoutesNativeRequestID"},
		Workspace:      root,
		Allowed:        []string{root},
		OnNotification: func(notification codex.Notification) { notifications <- notification },
	})
	created, err := runtime.Create(context.Background(), codex.Request{Cwd: root, Env: []string{"ASTRORDER_FAKE_CODEX_APPROVAL=1"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case notification := <-notifications:
		if notification.SessionID != created.SessionID || notification.ApprovalID != "codex:91" || notification.Status != "waiting_approval" {
			t.Fatalf("unexpected approval notification: %+v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("approval notification was not delivered")
	}
	result, err := runtime.Command(context.Background(), codex.Command{
		Action: "session.approve", SessionID: created.SessionID, ApprovalID: "codex:91", Decision: "accept",
	})
	if err != nil || !result.Accepted {
		t.Fatalf("approval was not accepted: result=%+v err=%v", result, err)
	}
	runtime.Close()
}

func fakeApproval() {
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
			_ = encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"thread": map[string]any{"id": "approval-thread"}}})
			_ = encoder.Encode(map[string]any{
				"id":     91,
				"method": "item/commandExecution/requestApproval",
				"params": map[string]any{"threadId": "approval-thread"},
			})
		default:
			if request["id"] == float64(91) {
				result, _ := request["result"].(map[string]any)
				if result["decision"] != "accept" {
					os.Exit(6)
				}
				return
			}
		}
	}
}
