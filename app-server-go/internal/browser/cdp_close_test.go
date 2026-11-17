package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestClosingCDPConnectionReleasesPendingCalls(t *testing.T) {
	const calls = 6
	received := make(chan struct{})
	release := make(chan struct{})
	var finish sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		for i := 0; i < calls; i++ {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
		close(received)
		<-release
	}))
	defer server.Close()
	defer finish.Do(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := DialCDP(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	results := make(chan error, calls)
	for i := 0; i < calls; i++ {
		go func() {
			_, err := client.Call(ctx, "Runtime.evaluate", nil)
			results <- err
		}()
	}
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("requests did not reach the CDP server")
	}
	if err := client.CloseNow(); err != nil {
		t.Fatalf("close CDP connection: %v", err)
	}
	for i := 0; i < calls; i++ {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("pending call succeeded after connection closed")
			}
		case <-ctx.Done():
			t.Fatal("pending CDP calls did not stop after connection closed")
		}
	}
	finish.Do(func() { close(release) })
}
