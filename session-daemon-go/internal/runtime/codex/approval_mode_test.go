package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestApprovalModeMapping(t *testing.T) {
	cases := map[string]string{"manual": "untrusted", "auto": "on-request", "full_access": "never"}
	for mode, policy := range cases {
		options, ok := approvalTurnOptions(mode)
		if !ok || options["approvalPolicy"] != policy {
			t.Fatalf("mode %s mapped to %+v", mode, options)
		}
	}
	if _, ok := approvalTurnOptions("bogus"); ok {
		t.Fatal("invalid mode accepted")
	}
	if got := approvalModeFromThread(map[string]any{"approvalPolicy": "never"}); got != "full_access" {
		t.Fatalf("never => %q", got)
	}
	if got := approvalModeFromThread(map[string]any{"approvalPolicy": "on-request", "approvalsReviewer": "auto_review"}); got != "auto" {
		t.Fatalf("auto_review => %q", got)
	}
	if got := approvalModeFromThread(map[string]any{"thread": map[string]any{"approvalPolicy": "untrusted"}}); got != "manual" {
		t.Fatalf("untrusted => %q", got)
	}
}

func TestCodexApprovalSetReadAndSendOptions(t *testing.T) {
	fake := newFakeSSHSession()
	defer fake.Close()
	turnParams := make(chan map[string]any, 4)
	go func() {
		decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
		for {
			var request map[string]any
			if decoder.Decode(&request) != nil {
				return
			}
			method := request["method"]
			if method == "initialized" {
				continue
			}
			result := map[string]any{}
			switch method {
			case "thread/start":
				result["thread"] = map[string]any{"id": "codex-thread-1", "model": "o3-mini", "modelProvider": "openai"}
			case "thread/read":
				result["thread"] = map[string]any{"id": "codex-thread-1", "model": "o3-mini"}
			case "turn/start":
				params, _ := request["params"].(map[string]any)
				turnParams <- params
				result["turn"] = map[string]any{"id": "turn-1"}
			}
			if encoder.Encode(map[string]any{"id": request["id"], "result": result}) != nil {
				return
			}
		}
	}()
	a := NewSSHAdapter(
		Config{Executable: "/synthetic/codex", Workspace: "/work", Allowed: []string{"/work"}, AgentID: "agent-codex"},
		nil,
		func(context.Context, ssh.CommandSpec) (SSHSession, error) { return fake, nil },
	)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.Create(ctx, core.Request{}); err != nil {
		t.Fatalf("create: %v", err)
	}

	read, err := a.Command(ctx, core.Request{SessionID: "codex-thread-1", Action: "session.approval.read"})
	if err != nil || read.Payload["mode"] != "manual" {
		t.Fatalf("default read = %+v err=%v", read.Payload, err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: "codex-thread-1", Action: "session.approval.set", Fields: map[string]any{"mode": "nope"}}); err == nil {
		t.Fatal("invalid mode accepted")
	}
	set, err := a.Command(ctx, core.Request{SessionID: "codex-thread-1", Action: "session.approval.set", Fields: map[string]any{"mode": "full_access"}})
	if err != nil || set.Payload["mode"] != "full_access" {
		t.Fatalf("set = %+v err=%v", set.Payload, err)
	}
	read, err = a.Command(ctx, core.Request{SessionID: "codex-thread-1", Action: "session.approval.read"})
	if err != nil || read.Payload["mode"] != "full_access" {
		t.Fatalf("read after set = %+v err=%v", read.Payload, err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: "codex-thread-1", Action: "session.send", Fields: map[string]any{"prompt": "hi", "command_id": "c1"}}); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case params := <-turnParams:
		if params["approvalPolicy"] != "never" {
			t.Fatalf("turn/start missing approval policy: %+v", params)
		}
		if sandbox, _ := params["sandboxPolicy"].(map[string]any); sandbox["type"] != "dangerFullAccess" {
			t.Fatalf("turn/start sandbox = %+v", params["sandboxPolicy"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for turn/start")
	}
}
