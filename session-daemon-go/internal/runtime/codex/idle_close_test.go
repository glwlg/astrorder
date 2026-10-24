package codex

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type trackedCloseSession struct {
	*fakeSSHSession
	closed atomic.Bool
}

func (s *trackedCloseSession) Close() error { s.closed.Store(true); return s.fakeSSHSession.Close() }
func TestIdleSSHCloseWaitsForConfirmedExit(t *testing.T) {
	s := &trackedCloseSession{fakeSSHSession: newFakeSSHSession()}
	defer s.Close()
	r := New(Config{})
	defer r.Close()
	r.sshSession = s
	r.stdin = s.Stdin()
	r.processDone = make(chan struct{})
	go func() {
		io.Copy(io.Discard, s.stdinR)
		if s.closed.Load() {
			t.Error("killed SSH before remote exit")
		}
		r.finishProcess(true)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.closeIdleTransport(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.closed.Load() {
		t.Fatal("transport not cleaned")
	}
}
func TestIdleSSHCloseTimeoutDoesNotKillOrConfirm(t *testing.T) {
	s := &trackedCloseSession{fakeSSHSession: newFakeSSHSession()}
	defer s.Close()
	r := New(Config{})
	defer r.Close()
	r.sshSession = s
	r.stdin = s.Stdin()
	r.processDone = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.closeIdleTransport(ctx); err == nil {
		t.Fatal("unknown exit accepted")
	}
	if s.closed.Load() || r.exitConfirmed {
		t.Fatal("timeout destroyed remote evidence")
	}
}
