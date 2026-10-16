package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestCanonicalHistoryReadsNativeSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []any
		reject   bool
	}{
		{name: "new empty session", messages: []any{}},
		{name: "existing session", messages: []any{map[string]any{"role": "user", "content": "persisted"}}},
		{name: "native failure", reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(Config{})
			a.canonical = true
			session := &SessionState{SessionID: "chat", Handle: "chat", Status: "idle", Metadata: map[string]any{}}
			a.sessions["chat"] = session
			reader, writer := io.Pipe()
			a.stdin = writer
			a.processDone = make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				decoder := json.NewDecoder(reader)
				for {
					var req map[string]any
					if decoder.Decode(&req) != nil {
						return
					}
					method := req["method"].(string)
					params := req["params"].(map[string]any)
					response := &rpcMessage{Result: map[string]any{}}
					switch method {
					case "session.resume":
						if params["session_id"] != "chat" || params["omit_messages"] == true {
							response.Error = &rpcErrorObject{Code: 4001, Message: "history snapshot parameters invalid"}
						} else if tc.reject {
							response.Error = &rpcErrorObject{Code: 4007, Message: "session not found"}
						} else {
							response.Result = map[string]any{"session_id": "chat", "messages": tc.messages}
						}
					case "session.stats", "goal.get":
					default:
						response.Error = &rpcErrorObject{Code: -32601, Message: fmt.Sprintf("unknown method: %s", method)}
					}
					a.mu.RLock()
					pending := a.pending[req["id"].(string)]
					a.mu.RUnlock()
					pending.ch <- response
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := a.handleHistoryPage(ctx, session, core.Request{Fields: map[string]any{"limit": 50}})
			writer.Close()
			reader.Close()
			<-done
			if tc.reject {
				if err == nil {
					t.Fatal("native failure was hidden as empty history")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			items, ok := result.Payload["items"].([]any)
			if !ok || len(items) != len(tc.messages) || result.Payload["next_cursor"] != nil {
				t.Fatalf("incorrect native history page: %#v", result.Payload)
			}
		})
	}
}

func TestLegacyHistoryVerbUnchanged(t *testing.T) {
	a := New(Config{})
	reader, writer := io.Pipe()
	a.stdin = writer
	a.processDone = make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var req map[string]any
		if json.NewDecoder(reader).Decode(&req) != nil {
			return
		}
		response := &rpcMessage{Result: map[string]any{"messages": []any{}}}
		if req["method"] != "session.history" {
			response.Error = &rpcErrorObject{Code: -32601, Message: "legacy history verb changed"}
		}
		a.mu.RLock()
		pending := a.pending[req["id"].(string)]
		a.mu.RUnlock()
		pending.ch <- response
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := a.rpc(ctx, "session.history", map[string]any{"session_id": "chat"})
	writer.Close()
	reader.Close()
	<-done
	if err != nil {
		t.Fatal(err)
	}
	page, err := gatewayHistoryPage("chat", result, "", 50)
	if err != nil || len(page["items"].([]any)) != 0 {
		t.Fatalf("legacy empty history: %#v %v", page, err)
	}
}
