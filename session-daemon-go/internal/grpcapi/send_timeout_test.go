package grpcapi

import (
	"testing"
	"time"
)

func TestSendTimeoutDoesNotCreateAdapterGoroutine(t *testing.T) {
	service := &service{options: options{
		sendTimeout: 10 * time.Millisecond,
		sendHook: func(string) {
			time.Sleep(20 * time.Millisecond)
		},
	}}
	started := time.Now()
	err := service.runSendHook("test")
	if err == nil {
		t.Fatal("expected send timeout")
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("send helper returned before the send completed: %v", elapsed)
	}
}
