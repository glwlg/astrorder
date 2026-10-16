package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationBurstWaitsForConsumerWithoutClosingTransport(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	delivered := make(chan int, 1024)
	r := New(Config{OnNotification: func(n Notification) {
		params, _ := n.Frame["params"].(map[string]any)
		seq, _ := params["seq"].(json.Number)
		i, _ := seq.Int64()
		if i == 0 {
			close(entered)
			<-release
		}
		delivered <- int(i)
	}})
	defer r.Close()
	defer unblock()
	r.setSession("thread", "running")
	r.stdin = captureWriter{make(chan map[string]any, 1)}
	const count = 700
	var input strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&input, "{\"method\":\"item/commandExecution/outputDelta\",\"params\":{\"threadId\":\"thread\",\"seq\":%d,\"delta\":\"line\\r\\n\"}}\n", i)
	}
	// Keep the pipe open after the burst so EOF cannot account for closure.
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go func() { _, _ = io.WriteString(writer, input.String()) }()
	go r.readLoop(reader)
	<-entered
	select {
	case <-r.done:
		t.Fatal("output burst closed the native transport while its consumer was busy")
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	for i := 0; i < count; i++ {
		select {
		case got := <-delivered:
			if got != i {
				t.Fatalf("notification order: got %d, want %d", got, i)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("lost output notification %d", i)
		}
	}
	if r.transportError() != nil {
		t.Fatal("burst invalidated native transport")
	}
	r.Close()
}

func TestCloseReleasesBackpressuredNotificationProducer(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once, enteredOnce sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	r := New(Config{OnNotification: func(Notification) { enteredOnce.Do(func() { close(entered) }); <-release }})
	defer r.Close()
	defer unblock()
	r.setSession("thread", "running")
	var sent atomic.Int32
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := 0; i < 700; i++ {
			r.handleNotification("item/commandExecution/outputDelta", map[string]any{"params": map[string]any{"threadId": "thread", "delta": "line"}})
			sent.Add(1)
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("consumer did not start")
	}
	deadline := time.Now().Add(time.Second)
	for sent.Load() < int32(cap(r.notifications)) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if sent.Load() < int32(cap(r.notifications)) {
		t.Fatal("producer did not fill the queue")
	}
	select {
	case <-finished:
		t.Fatal("producer did not wait on full queue")
	case <-time.After(20 * time.Millisecond):
	}
	r.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("close left notification producer blocked")
	}
	unblock()
	select {
	case <-r.eventDone:
	case <-time.After(time.Second):
		t.Fatal("notification dispatcher did not retire")
	}
}
