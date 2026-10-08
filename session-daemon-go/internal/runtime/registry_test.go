package runtime

import (
	"context"
	"testing"
	"time"
)

type blockingRuntime struct {
	started chan struct{}
	release chan struct{}
}

func (fake blockingRuntime) Create(context.Context, Request) (Result, error) {
	close(fake.started)
	<-fake.release
	return Result{Status: "idle"}, nil
}

type readyRuntime struct{}

func (readyRuntime) Create(context.Context, Request) (Result, error) {
	return Result{Status: "idle", SessionID: "fast-session"}, nil
}

func TestSlowRuntimeDoesNotBlockAnotherRuntime(t *testing.T) {
	registry := NewRegistry()
	slow := blockingRuntime{started: make(chan struct{}), release: make(chan struct{})}
	registry.Register("codex-ssh", slow)
	registry.Register("codex", readyRuntime{})
	defer close(slow.release)

	go registry.Create(context.Background(), Request{AgentType: "codex-ssh"})
	select {
	case <-slow.started:
	case <-time.After(time.Second):
		t.Fatal("slow runtime did not start")
	}

	done := make(chan Result, 1)
	go func() {
		result, err := registry.Create(context.Background(), Request{AgentType: "codex"})
		if err != nil {
			t.Errorf("fast runtime failed: %v", err)
		}
		done <- result
	}()
	select {
	case result := <-done:
		if result.SessionID != "fast-session" {
			t.Fatalf("unexpected result: %+v", result)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("slow codex-ssh runtime blocked local codex")
	}
}
