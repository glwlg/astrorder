package codex

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type gatedWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *gatedWriter) Write(b []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(b), nil
}
func (w *gatedWriter) Close() error { return nil }

func TestConcurrentApprovalCannotSendTwoDecisions(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.setSession("thread", "idle")
	w := &gatedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	r.stdin = w
	r.handleNotification("item/commandExecution/requestApproval", map[string]any{"id": float64(91), "params": map[string]any{"threadId": "thread"}})
	cmd := Command{Action: "session.approve", SessionID: "thread", ApprovalID: "codex:91", Decision: "accept"}
	first := make(chan error, 1)
	go func() { _, err := r.Command(context.Background(), cmd); first <- err }()
	<-w.entered
	second := make(chan error, 1)
	go func() { _, err := r.Command(context.Background(), cmd); second <- err }()
	// Unblock both writes: exactly one decision must be accepted.
	close(w.release)
	a, b := <-first, <-second
	if (a == nil) == (b == nil) {
		t.Fatalf("expected one successful decision, got %v and %v", a, b)
	}
}

func TestEOFInvalidatesTransportBeforeNextRequest(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.setSession("thread", "running")
	r.stdin = captureWriter{make(chan map[string]any, 1)}
	r.readLoop(strings.NewReader(""))
	r.mu.Lock()
	status := r.status
	r.mu.Unlock()
	if status != "error" {
		t.Fatalf("transport exit left status %q", status)
	}
	if _, err := r.Command(context.Background(), Command{Action: "session.send", SessionID: "thread", Text: "hello"}); err == nil {
		t.Fatal("dead transport accepted request")
	}
}

func TestForeignThreadNotificationCannotChangeOwnedState(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.setSession("owned", "running")
	r.handleNotification("turn/completed", map[string]any{"params": map[string]any{"threadId": "foreign", "turn": map[string]any{"id": "turn", "status": "completed"}}})
	if r.status != "running" {
		t.Fatal("foreign completion changed owned state")
	}
}

func TestCloseUnblocksPendingRequest(t *testing.T) {
	r := New(Config{})
	r.stdin = captureWriter{make(chan map[string]any, 1)}
	finished := make(chan error, 1)
	go func() { _, err := r.request(context.Background(), "test", nil); finished <- err }()
	select {
	case <-r.stdin.(captureWriter).frames:
	case <-time.After(time.Second):
		t.Fatal("request did not write")
	}
	r.Close()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("closed request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close left request waiting")
	}
}

var _ io.WriteCloser = (*gatedWriter)(nil)
