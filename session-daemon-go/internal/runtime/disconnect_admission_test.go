package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type disconnectAdmissionFixture struct {
	mu                  sync.Mutex
	sessions            []string
	enumerated          chan struct{}
	continueEnumeration chan struct{}
	closed              bool
}

func (a *disconnectAdmissionFixture) Create(ctx context.Context, r Request) (Result, error) {
	return a.Spawn(ctx, r)
}

func (a *disconnectAdmissionFixture) Spawn(_ context.Context, r Request) (Result, error) {
	a.mu.Lock()
	a.sessions = append(a.sessions, r.SessionID)
	a.mu.Unlock()
	state := "idle"
	if r.SessionID == "new-active" {
		state = "running"
	}
	return Result{SessionID: r.SessionID, Status: state}, nil
}

func (a *disconnectAdmissionFixture) SessionsForConnection(string) []string {
	a.mu.Lock()
	ids := append([]string(nil), a.sessions...)
	a.mu.Unlock()
	close(a.enumerated)
	<-a.continueEnumeration
	return ids
}

func (a *disconnectAdmissionFixture) DisconnectConnection(context.Context, string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return append([]string(nil), a.sessions...), nil
}

func TestDisconnectAdmissionPrecedesConnectionSnapshot(t *testing.T) {
	r := NewRegistry()
	a := &disconnectAdmissionFixture{enumerated: make(chan struct{}), continueEnumeration: make(chan struct{})}
	r.Register("ssh", a)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := r.Spawn(ctx, Request{AgentType: "ssh", SessionID: "idle"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.DisconnectRuntime(ctx, "ssh", "private-connection")
		done <- err
	}()
	select {
	case <-a.enumerated:
	case <-ctx.Done():
		t.Fatal("disconnect never enumerated connection sessions")
	}
	_, spawnErr := r.Spawn(ctx, Request{AgentType: "ssh", SessionID: "new-active"})
	close(a.continueEnumeration)
	if !errors.Is(spawnErr, ErrRuntimeBusy) {
		t.Errorf("new active work admitted after disconnect snapshot: %v", spawnErr)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("disconnect did not finish")
	}
	if len(a.sessions) != 1 || !a.closed {
		t.Fatal("disconnect admitted and terminated new active native work", a.sessions, a.closed)
	}
	if r.Busy() {
		t.Fatal("disconnect did not release maintenance admission")
	}
}
