package main

import (
	"sync"
	"testing"
	"time"
)

type blockedGRPCDrain struct {
	started chan struct{}
	release chan struct{}
	forced  chan struct{}
	once    sync.Once
}

func (server *blockedGRPCDrain) GracefulStop() { close(server.started); <-server.release }
func (server *blockedGRPCDrain) Stop() {
	server.once.Do(func() { close(server.forced); close(server.release) })
}

func TestGRPCDrainWaitsForAcceptedResponses(t *testing.T) {
	server := &blockedGRPCDrain{started: make(chan struct{}), release: make(chan struct{}), forced: make(chan struct{})}
	done := make(chan struct{})
	go func() { drainGRPC(server, time.Second); close(done) }()
	select {
	case <-server.started:
	case <-time.After(time.Second):
		t.Fatal("graceful drain not started")
	}
	select {
	case <-done:
		t.Fatal("drain returned before active response completed")
	default:
	}
	close(server.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain did not finish after response completion")
	}
	select {
	case <-server.forced:
		t.Fatal("completed RPC forced closed")
	default:
	}
}

func TestGRPCDrainBoundsStalledTransport(t *testing.T) {
	server := &blockedGRPCDrain{started: make(chan struct{}), release: make(chan struct{}), forced: make(chan struct{})}
	done := make(chan struct{})
	go func() { drainGRPC(server, 10*time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stalled drain did not terminate")
	}
	select {
	case <-server.forced:
	default:
		t.Fatal("stalled transport not stopped")
	}
}
