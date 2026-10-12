package codex

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func TestOwnedHistoryPageUsesNativeLimitAndRejectsForeignCursor(t *testing.T) {
	var reads atomic.Int32
	fake := newFakeSSHSession()
	defer fake.Close()
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
				result["thread"] = map[string]any{"id": "owned"}
			case "thread/items/list":
				reads.Add(1)
				params := request["params"].(map[string]any)
				if params["limit"] != float64(2) || params["sortDirection"] != "desc" {
					return
				}
				result = map[string]any{"data": []any{map[string]any{"item": map[string]any{"id": "newer"}}, map[string]any{"item": map[string]any{"id": "older"}}}, "nextCursor": nil, "unknown": json.Number("9007199254740993")}
				if params["cursor"] == nil {
					result["nextCursor"] = "native-next"
				}
			}
			if encoder.Encode(map[string]any{"id": request["id"], "result": result}) != nil {
				return
			}
		}
	}()
	a := NewSSHAdapter(Config{Executable: "/synthetic/codex", Workspace: "/work", Allowed: []string{"/work"}, AgentID: "agent"}, nil, func(context.Context, ssh.CommandSpec) (SSHSession, error) { return fake, nil })
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := a.Create(ctx, core.Request{}); err != nil {
		t.Fatal(err)
	}
	request := core.Request{SessionID: "owned", Action: "session.history_page", Fields: map[string]any{"limit": 2}}
	page, err := a.Command(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	items, ok := page.Payload["items"].([]any)
	if !ok || len(items) != 2 || items[0].(map[string]any)["item"].(map[string]any)["id"] != "older" {
		t.Fatalf("native page absent/incorrect: %+v", page)
	}
	cursor, ok := page.Payload["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("missing cursor")
	}
	request.Fields["before"] = cursor
	if _, err := a.Command(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.Fields["before"] = "foreign-token"
	if _, err := a.Command(ctx, request); err == nil {
		t.Fatal("foreign cursor accepted")
	}
	if reads.Load() != 2 {
		t.Fatalf("invalid cursor dispatched: %d", reads.Load())
	}
}
