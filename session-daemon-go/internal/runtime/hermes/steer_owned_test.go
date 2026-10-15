package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestOwnedRunningFollowupUsesSteerAndPreservesOriginal(t *testing.T) {
	a := New(Config{})
	s := &SessionState{SessionID: "s", Handle: "h", Status: "running", CommandID: "original", Metadata: map[string]any{}}
	a.sessions["s"] = s
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	a.stdin = writer
	a.processDone = make(chan struct{})
	methods := make(chan string, 1)
	go func() {
		var request map[string]any
		if json.NewDecoder(reader).Decode(&request) != nil {
			return
		}
		methods <- request["method"].(string)
		a.mu.RLock()
		pending := a.pending[request["id"].(string)]
		a.mu.RUnlock()
		pending.ch <- &rpcMessage{Result: map[string]any{"status": "queued"}}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := a.handleSend(ctx, s, core.Request{Fields: map[string]any{"text": "followup", "command_id": "new"}})
	if err != nil {
		t.Fatal(err)
	}
	if method := <-methods; method != "session.steer" {
		t.Fatal(method)
	}
	if result.Status != "running" || s.CommandID != "original" || result.Payload["accepted"] != true {
		t.Fatalf("lost original: %+v %+v", result, s)
	}
	s.CommandID = ""
	if _, err := a.handleSend(ctx, s, core.Request{Fields: map[string]any{"text": "foreign"}}); err == nil {
		t.Fatal("unowned work accepted followup")
	}
	writer.Close()
	s.Status = "idle"
	if _, err := a.handleSend(ctx, s, core.Request{Fields: map[string]any{"text": "late guidance", "params": map[string]any{"guidance_only": true}}}); err == nil || err.Error() != "Hermes guidance requires an owned running command" {
		t.Fatalf("late guidance attempted a new turn: %v", err)
	}
}
