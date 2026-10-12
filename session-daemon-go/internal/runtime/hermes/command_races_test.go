package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

type completionWriter struct{ beforeReturn func() }

func (w completionWriter) Write(p []byte) (int, error) { w.beforeReturn(); return len(p), nil }
func (w completionWriter) Close() error                { return nil }

func TestHermesApprovalCompletionBeforeReplyPreservesIdle(t *testing.T) {
	var decisionStatus string
	a := New(Config{AgentID: "fixture", Emit: func(sid, event string, payload map[string]any, status string) error {
		if event == "hermes.approval_resolved" {
			decisionStatus = status
		}
		return nil
	}})
	s := &SessionState{SessionID: "s", CommandID: "original", Status: "waiting_approval", Metadata: map[string]any{}}
	a.sessions["s"] = s
	a.pendingApprovals["exact"] = &approvalRecord{NativeID: "srq-1", SessionID: "s", Method: "approval"}
	a.stdin = completionWriter{beforeReturn: func() { a.handleGatewayEvent(map[string]any{"type": "message.complete", "session_id": "s"}) }}
	result, err := a.handleApprove(context.Background(), s, core.Request{Fields: map[string]any{"approval_id": "exact", "choice": "deny", "command_id": "decision"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "idle" || result.Status != "idle" || decisionStatus != "idle" {
		t.Fatalf("approval reopened completed command: state=%s result=%s event=%s", s.Status, result.Status, decisionStatus)
	}
}

func TestHermesCompletionCannotInventCommandBinding(t *testing.T) {
	var completed map[string]any
	a := New(Config{AgentID: "fixture", Emit: func(sid, event string, payload map[string]any, status string) error { completed = payload; return nil }})
	a.sessions["s"] = &SessionState{SessionID: "s", Status: "running", Metadata: map[string]any{}}
	a.handleGatewayEvent(map[string]any{"type": "message.complete", "session_id": "s", "payload": map[string]any{"command_id": "foreign"}})
	if _, exists := completed["command_id"]; exists {
		t.Fatalf("unbound completion claimed command: %+v", completed)
	}
}

func TestHermesRefusedSubmitCannotSteerExistingWork(t *testing.T) {
	a := New(Config{})
	s := &SessionState{SessionID: "s", Handle: "native", Status: "idle", Metadata: map[string]any{}}
	a.sessions["s"] = s
	reader, writer := io.Pipe()
	a.stdin = writer
	a.processDone = make(chan struct{})
	done := make(chan []string, 1)
	go func() {
		methods := []string{}
		decoder := json.NewDecoder(reader)
		for {
			var request map[string]any
			if decoder.Decode(&request) != nil {
				done <- methods
				return
			}
			method := request["method"].(string)
			methods = append(methods, method)
			response := &rpcMessage{Result: map[string]any{}}
			if method == "prompt.submit" {
				response.Error = &rpcErrorObject{Code: -32000, Message: "already has a live owner"}
			}
			if method == "session.active_list" {
				response.Result = map[string]any{"sessions": []any{map[string]any{"id": "native", "status": "running"}}}
			}
			a.mu.RLock()
			pending := a.pending[request["id"].(string)]
			a.mu.RUnlock()
			pending.ch <- response
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := a.handleSend(ctx, s, core.Request{Fields: map[string]any{"text": "new", "command_id": "new-command"}})
	writer.Close()
	reader.Close()
	methods := <-done
	if err == nil {
		t.Fatalf("refused submission accepted: %v", methods)
	}
	for _, method := range methods {
		if method == "session.steer" {
			t.Fatalf("refused submit altered existing work: %v", methods)
		}
	}
}
