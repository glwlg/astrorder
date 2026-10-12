package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestSendPublishesActivityBeforeNativeCompletion(t *testing.T) {
	events := []string{}
	a := New(Config{AgentID: "a", Emit: func(sid, event string, payload map[string]any, status string) error {
		events = append(events, event)
		if event == "session.active" && (sid != "s" || status != "running" || payload["agent_id"] != "a") {
			t.Errorf("invalid activity: %s %s %v", sid, status, payload)
		}
		return nil
	}})
	s := &SessionState{SessionID: "s", Handle: "h", Status: "idle", Metadata: map[string]any{}}
	a.sessions["s"] = s
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	a.stdin = writer
	a.processDone = make(chan struct{})
	go func() {
		var request map[string]any
		if json.NewDecoder(reader).Decode(&request) != nil {
			return
		}
		a.mu.RLock()
		pending := a.pending[request["id"].(string)]
		a.mu.RUnlock()
		pending.ch <- &rpcMessage{Result: map[string]any{"accepted": true}}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := a.handleSend(ctx, s, core.Request{Fields: map[string]any{"text": "test", "command_id": "c"}})
	if err != nil || result.Status != "running" || len(events) != 1 || events[0] != "session.active" {
		t.Fatalf("send: %+v %v events=%v", result, err, events)
	}
	a.handleGatewayEvent(map[string]any{"type": "message.complete", "session_id": "s"})
	if s.Status != "idle" || len(events) != 2 || events[1] != "hermes.command_complete" {
		t.Fatalf("completion: %s %v", s.Status, events)
	}
}
