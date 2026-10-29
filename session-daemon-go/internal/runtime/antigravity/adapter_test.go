package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamLifecycle(t *testing.T) {
	exe, _ := os.Executable()
	var mu sync.Mutex
	var events []map[string]any
	a := New(Config{Executable: exe, Arguments: []string{"-test.run=TestStreamHelper", "--"}, Environment: []string{"ASTRORDER_AGY_HELPER=1"}, Workspace: t.TempDir(), Allowed: []string{os.TempDir()}, AgentID: "local-antigravity", Emit: func(id, event string, p map[string]any, status string) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, map[string]any{"event": event, "payload": p, "status": status})
		return nil
	}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"effort": "future-effort"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.SessionID != "11111111-1111-4111-8111-111111111111" || created.Payload["agent_id"] != "local-antigravity" {
		t.Fatalf("identity: %#v", created)
	}
	a.mu.Lock()
	args := a.sessions[created.SessionID].cmd.Args
	a.mu.Unlock()
	if !strings.Contains(strings.Join(args, "\x00"), "--effort\x00future-effort") {
		t.Fatalf("effort was not forwarded to Agent: %q", args)
	}
	r := core.Request{SessionID: created.SessionID, Action: "session.send", Fields: map[string]any{"text": "hello", "command_id": "cmd"}}
	if _, err = a.Command(ctx, r); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.Snapshot()[created.SessionID].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if a.Snapshot()[created.SessionID].Status != "idle" {
		t.Fatal("terminal result did not settle")
	}
	mu.Lock()
	for _, event := range events {
		if event["event"] == "runtime.owner" {
			payload := event["payload"].(map[string]any)
			if pid, ok := payload["owner_pid"].(int); !ok || pid <= 0 {
				t.Errorf("runtime owner must identify its actual process: %+v", payload)
			}
		}
	}
	raw, _ := json.Marshal(events)
	mu.Unlock()
	if !strings.Contains(string(raw), "hello response") || !strings.Contains(string(raw), "completed") {
		t.Fatalf("events: %s", raw)
	}
	r.Fields["attachments"] = []any{map[string]any{"name": "image.png"}}
	if _, err = a.Command(ctx, r); err == nil {
		t.Fatal("attachments silently accepted")
	}
	if _, err = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.steer"}); err == nil {
		t.Fatal("unsupported steer accepted")
	}
}
func TestEOFIsFailure(t *testing.T) {
	exe, _ := os.Executable()
	a := New(Config{Executable: exe, Arguments: []string{"-test.run=TestStreamHelper", "--"}, Environment: []string{"ASTRORDER_AGY_HELPER=1", "ASTRORDER_AGY_EOF=1"}, Workspace: t.TempDir(), Allowed: []string{os.TempDir()}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := a.Create(ctx, core.Request{Fields: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Command(ctx, core.Request{SessionID: r.SessionID, Action: "session.send", Fields: map[string]any{"text": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.Snapshot()[r.SessionID].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if a.Snapshot()[r.SessionID].Status != "error" {
		t.Fatal("EOF disguised as completion")
	}
}
func TestStreamHelper(t *testing.T) {
	if os.Getenv("ASTRORDER_AGY_HELPER") != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "/effort" {
			fmt.Println("not adjustable")
			os.Exit(0)
		}
		if arg == "models" {
			fmt.Println("gemini-test	Test Gemini")
			fmt.Println("claude-test	Test Claude")
			os.Exit(0)
		}
	}
	id := "11111111-1111-4111-8111-111111111111"
	model := "gemini-test"
	for i, arg := range os.Args {
		if arg == "--model" && i+1 < len(os.Args) {
			model = os.Args[i+1]
		}
	}
	permission := "request-review"
	for _, arg := range os.Args {
		if arg == "--dangerously-skip-permissions" {
			permission = "always-proceed"
		}
	}
	for i, arg := range os.Args {
		if arg == "--conversation" && i+1 < len(os.Args) {
			id = os.Args[i+1]
		}
	}
	fmt.Printf("{\"event\":\"init\",\"conversation_id\":%q,\"init\":{\"model\":%q,\"permission_mode\":%q}}\n", id, model, permission)
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		if os.Getenv("ASTRORDER_AGY_EOF") == "1" {
			os.Exit(0)
		}
		fmt.Println(`{"event":"step_update","step_update":{"conversation_id":"11111111-1111-4111-8111-111111111111","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"hello response"}}`)
		fmt.Println(`{"event":"result","result":{"conversation_id":"11111111-1111-4111-8111-111111111111","status":"SUCCESS","response":"hello response"}}`)
	}
	os.Exit(0)
}
func TestHistoryRejectsTraversal(t *testing.T) {
	_, _, err := readHistory(context.Background(), filepath.Join(t.TempDir(), "conversations"), "../secrets", "", 50)
	if err == nil {
		t.Fatal("invalid native identity accepted")
	}
}
