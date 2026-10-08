package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestCreatePreservesForkAndNativeTitle(t *testing.T) {
	for _, ephemeral := range []bool{false, true} {
		t.Run(map[bool]string{false: "persistent", true: "ephemeral"}[ephemeral], func(t *testing.T) {
			fake := newFakeSSHSession()
			calls := make(chan map[string]any, 8)
			go func() {
				d, e := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
				title := ""
				for {
					var r map[string]any
					if d.Decode(&r) != nil {
						return
					}
					if r["method"] == "initialized" {
						continue
					}
					calls <- r
					p, _ := r["params"].(map[string]any)
					result := map[string]any{}
					switch r["method"] {
					case "thread/start", "thread/fork":
						result = map[string]any{"thread": map[string]any{"id": "child"}, "model": "native-model", "modelProvider": "native-provider"}
					case "thread/name/set":
						title, _ = p["name"].(string)
					case "thread/read":
						result = map[string]any{"thread": map[string]any{"id": "child", "name": title}}
					}
					if e.Encode(map[string]any{"id": r["id"], "result": result}) != nil {
						return
					}
				}
			}()
			a := NewSSHAdapter(Config{Executable: "codex", Workspace: "/audit", Allowed: []string{"/audit"}}, nil, func(context.Context, ssh.CommandSpec) (SSHSession, error) { return fake, nil })
			defer a.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": "/audit", "parent_session_id": "parent", "title": "Fork title", "ephemeral": ephemeral}})
			if err != nil {
				t.Fatal(err)
			}
			if result.SessionID != "child" || result.Payload["model"] != "native-model" || result.Payload["provider"] != "native-provider" {
				t.Errorf("lost native binding: %v", result)
			}
			<-calls // initialize
			fork := <-calls
			params := fork["params"].(map[string]any)
			if fork["method"] != "thread/fork" || params["threadId"] != "parent" || params["ephemeral"] != ephemeral || params["excludeTurns"] != true {
				t.Fatalf("fork contract lost: %v", fork)
			}
			if !ephemeral {
				if params["deferGoalContinuation"] != true {
					t.Fatal("fork can start unrequested goal continuation")
				}
				if call := <-calls; call["method"] != "thread/name/set" {
					t.Fatalf("missing native title write: %v", call)
				}
				if call := <-calls; call["method"] != "thread/read" {
					t.Fatalf("missing native title readback: %v", call)
				}
			} else if len(calls) != 0 {
				t.Fatal("ephemeral create persisted title")
			}
		})
	}
}

func TestCreateRejectsInvalidFieldsBeforeStarting(t *testing.T) {
	for _, fields := range []map[string]any{{"parent_session_id": ""}, {"parent_session_id": 12}, {"ephemeral": "false"}, {"title": "bad\nname"}} {
		started := false
		a := NewSSHAdapter(Config{Workspace: "/audit", Allowed: []string{"/audit"}}, nil, func(context.Context, ssh.CommandSpec) (SSHSession, error) {
			started = true
			return nil, context.Canceled
		})
		_, err := a.Create(context.Background(), core.Request{Fields: fields})
		a.Close()
		if err == nil || started {
			t.Errorf("invalid create reached native launch: %v", fields)
		}
	}
}
