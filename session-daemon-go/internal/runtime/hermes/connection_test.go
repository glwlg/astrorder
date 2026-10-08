package hermes

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestRuntimeControlConnectDoesNotResumeConversation(t *testing.T) {
	if os.Getenv("HERMES_CONNECTION_HELPER") == "1" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "gateway.ready"}})
		decoder := json.NewDecoder(os.Stdin)
		for {
			var r map[string]any
			if decoder.Decode(&r) != nil {
				os.Exit(0)
			}
			if r["method"] == "session.list" || r["method"] == "session.active_list" {
				encoder.Encode(map[string]any{"id": r["id"], "result": map[string]any{"sessions": []any{}}})
			} else {
				encoder.Encode(map[string]any{"id": r["id"], "error": map[string]any{"code": 4007, "message": "no conversation may be opened during connect"}})
			}
		}
	}
	for _, isolated := range []bool{false, true} {
		t.Run(map[bool]string{false: "adapter", true: "isolated"}[isolated], func(t *testing.T) {
			root := t.TempDir()
			var mu sync.Mutex
			var hello map[string]any
			cfg := Config{Executable: os.Args[0], Arguments: []string{"-test.run=^TestRuntimeControlConnectDoesNotResumeConversation$"}, Environment: []string{"HERMES_CONNECTION_HELPER=1"}, Workspace: root, Allowed: []string{root}, AgentID: "test-hermes", Emit: func(id, event string, p map[string]any, status string) error {
				mu.Lock()
				defer mu.Unlock()
				if event == "connector.hello" {
					hello = p
				}
				return nil
			}}
			cfg.AgentName = "WSL · Hermes"
			var adapter core.Adapter = New(cfg)
			if isolated {
				adapter = NewIsolated(cfg)
			}
			defer adapter.(interface{ Close() error }).Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r, err := adapter.(core.Spawner).Spawn(ctx, core.Request{SessionID: "control-only", Fields: map[string]any{"params": map[string]any{"runtime_control": true}}})
			if err != nil {
				t.Fatal(err)
			}
			if r.SessionID != "control-only" || r.Payload["agent_id"] != "test-hermes" {
				t.Fatal(r)
			}
			mu.Lock()
			ready := hello != nil && hello["status"] == "ready" && hello["id"] == "test-hermes"
			mu.Unlock()
			if !ready {
				t.Fatal("native readiness was not projected")
			}
			if hello["name"] != cfg.AgentName || r.Payload["name"] != cfg.AgentName {
				t.Fatalf("connection name lost: hello=%v result=%v", hello["name"], r.Payload["name"])
			}
			q, ok := adapter.(core.Querier)
			if !ok {
				t.Fatal("runtime discovery unavailable")
			}
			out, err := q.Query(ctx, core.Request{Fields: map[string]any{"method": "session.list", "request_params": map[string]any{}}})
			if err != nil || out["result"] == nil {
				t.Fatalf("query: %v %v", out, err)
			}
			if _, err = q.Query(ctx, core.Request{Fields: map[string]any{"method": "session.create"}}); err == nil {
				t.Fatal("mutating discovery allowed")
			}
			if len(adapter.(core.Snapshotter).Snapshot()) != 1 {
				t.Fatal("connect created hidden conversations")
			}
		})
	}
}
