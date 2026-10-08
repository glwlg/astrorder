package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestAdapterOwnsMultipleNativeSessions(t *testing.T) {
	if os.Getenv("ASTRORDER_ADAPTER_HELPER") == "1" {
		adapterHelper()
		os.Exit(0)
	}
	root := t.TempDir()
	emitted := make(chan map[string]any, 20)
	a := NewAdapter(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestAdapterOwnsMultipleNativeSessions"}, Environment: []string{"ASTRORDER_ADAPTER_HELPER=1"}, Workspace: root, Allowed: []string{root}, AgentID: "test-codex"}, func(_ string, _ string, p map[string]any, _ string) error { emitted <- p; return nil })
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID == second.SessionID {
		t.Fatal("sessions shared native identity")
	}
	sent, err := a.Command(ctx, core.Request{SessionID: first.SessionID, Action: "session.send", Fields: map[string]any{"prompt": "hello"}})
	if err != nil || sent.Payload["turn_id"] != "turn-a" {
		t.Fatal(sent, err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: first.SessionID, Action: "session.interrupt", Fields: map[string]any{"turn_id": "turn-a"}}); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case n := <-emitted:
			frame, _ := n["frame"].(map[string]any)
			if frame != nil && frame["method"] == "turn/completed" {
				goto completed
			}
		case <-ctx.Done():
			t.Fatal("no native completion")
		}
	}
completed:
	snapshot := a.Snapshot()
	if snapshot[first.SessionID].Status != "idle" || snapshot[second.SessionID].Status != "idle" {
		t.Fatal(snapshot)
	}
	deleted, err := a.Command(ctx, core.Request{SessionID: second.SessionID, Action: "session.delete"})
	if err != nil || deleted.Payload["deleted"] != second.SessionID {
		t.Fatal(deleted, err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: second.SessionID, Action: "session.send", Fields: map[string]any{"prompt": "bad"}}); err == nil {
		t.Fatal("deleted session still owned")
	}
}
func adapterHelper() {
	d := json.NewDecoder(os.Stdin)
	e := json.NewEncoder(os.Stdout)
	id := fmt.Sprintf("thread-%d", os.Getpid())
	for {
		var r map[string]any
		if d.Decode(&r) != nil {
			return
		}
		p, _ := r["params"].(map[string]any)
		var result any = map[string]any{}
		switch r["method"] {
		case "initialized":
			continue
		case "thread/start":
			result = map[string]any{"thread": map[string]any{"id": id}}
		case "thread/resume":
			result = map[string]any{"thread": map[string]any{"id": p["threadId"]}}
		case "turn/start":
			result = map[string]any{"turn": map[string]any{"id": "turn-a"}}
		case "turn/interrupt":
			e.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": id, "turn": map[string]any{"id": "turn-a", "status": "completed"}}})
		}
		e.Encode(map[string]any{"id": r["id"], "result": result})
	}
}
