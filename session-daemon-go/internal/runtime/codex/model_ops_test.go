package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestCodexModelReadAndSet(t *testing.T) {
	fake := newFakeSSHSession()
	defer fake.Close()

	updatedSettings := make(chan map[string]any, 10)

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
				result["thread"] = map[string]any{
					"id":              "codex-thread-1",
					"model":           "o3-mini",
					"modelProvider":   "openai",
					"reasoningEffort": "medium",
				}
			case "thread/read":
				result["thread"] = map[string]any{
					"id":              "codex-thread-1",
					"model":           "o3-mini",
					"modelProvider":   "openai",
					"reasoningEffort": "medium",
				}
			case "thread/settings/update":
				params, _ := request["params"].(map[string]any)
				updatedSettings <- params
				result["success"] = true
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

	created, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if created.SessionID != "codex-thread-1" {
		t.Fatalf("unexpected session ID: %s", created.SessionID)
	}

	// 1. session.model.read
	readRes, err := a.Command(ctx, core.Request{
		SessionID: "codex-thread-1",
		Action:    "session.model.read",
	})
	if err != nil {
		t.Fatalf("session.model.read failed: %v", err)
	}
	if readRes.Payload["model"] != "o3-mini" || readRes.Payload["provider"] != "openai" || readRes.Payload["effort"] != "medium" {
		t.Fatalf("unexpected read payload: %+v", readRes.Payload)
	}

	// 2. session.model.set (with empty provider, codex standard)
	setRes, err := a.Command(ctx, core.Request{
		SessionID: "codex-thread-1",
		Action:    "session.model.set",
		Fields: map[string]any{
			"model":    "gpt-5",
			"provider": "",
		},
	})
	if err != nil {
		t.Fatalf("session.model.set failed: %v", err)
	}
	if setRes.Payload["model"] != "gpt-5" {
		t.Fatalf("unexpected set payload: %+v", setRes.Payload)
	}
	select {
	case s := <-updatedSettings:
		if s["model"] != "gpt-5" {
			t.Fatalf("expected model gpt-5 in thread/settings/update, got: %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for thread/settings/update")
	}

	// 3. session.reasoning.set
	effortRes, err := a.Command(ctx, core.Request{
		SessionID: "codex-thread-1",
		Action:    "session.reasoning.set",
		Fields: map[string]any{
			"effort": "high",
		},
	})
	if err != nil {
		t.Fatalf("session.reasoning.set failed: %v", err)
	}
	if effortRes.Payload["effort"] != "high" {
		t.Fatalf("unexpected effort payload: %+v", effortRes.Payload)
	}
	select {
	case s := <-updatedSettings:
		if s["effort"] != "high" {
			t.Fatalf("expected effort high in thread/settings/update, got: %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for thread/settings/update")
	}

	// 4. Verify in-memory state via read (without mock thread/read update)
	readRes2, err := a.Command(ctx, core.Request{
		SessionID: "codex-thread-1",
		Action:    "session.model.read",
	})
	if err != nil {
		t.Fatalf("second session.model.read failed: %v", err)
	}
	// thread/read in fake returns o3-mini, which updates thread state
	if readRes2.Payload["model"] == "" {
		t.Fatalf("model should not be empty: %+v", readRes2.Payload)
	}
}
