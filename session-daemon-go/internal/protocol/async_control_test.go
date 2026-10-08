package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type delayedControl struct {
	fixtureRuntime
	started chan struct{}
	release chan struct{}
}

func (r *delayedControl) Command(ctx context.Context, request core.Request) (core.Result, error) {
	if request.Action == "session.send" {
		close(r.started)
		select {
		case <-r.release:
		case <-ctx.Done():
			return core.Result{}, ctx.Err()
		}
	} else if request.Action == "session.interrupt" {
		close(r.release)
	}
	return core.Result{SessionID: request.SessionID, Status: "idle", Payload: map[string]any{"accepted": true}}, nil
}
func TestSlowControlDoesNotBlockSameSocketInterrupt(t *testing.T) {
	d := New("key")
	adapter := &delayedControl{started: make(chan struct{}), release: make(chan struct{})}
	d.RegisterRuntime("test", adapter)
	if _, err := d.registry.Create(context.Background(), core.Request{AgentType: "test"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(d.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	write := func(raw string) {
		t.Helper()
		if err := socket.Write(ctx, websocket.MessageText, []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]any {
		t.Helper()
		_, raw, err := socket.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err = json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	write(`{"action":"daemon.handshake","secret":"key"}`)
	read()
	write(`{"action":"session.send","session_id":"created","request_id":"send"}`)
	select {
	case <-adapter.started:
	case <-ctx.Done():
		t.Fatal("send not started")
	}
	write(`{"action":"daemon.status","request_id":"status"}`)
	if r := read(); r["request_id"] != "status" {
		t.Fatal("control blocked status", r)
	}
	if d.shutdown(map[string]any{})["action"] != "error" {
		t.Fatal("pending native operation was not protected")
	}
	write(`{"action":"session.interrupt","session_id":"created","request_id":"interrupt"}`)
	seen := map[any]bool{}
	for i := 0; i < 2; i++ {
		r := read()
		seen[r["request_id"]] = true
	}
	if !seen["send"] || !seen["interrupt"] {
		t.Fatal(seen)
	}
}
