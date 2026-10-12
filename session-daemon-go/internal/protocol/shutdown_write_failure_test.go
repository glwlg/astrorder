package protocol

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type shutdownFailListener struct {
	net.Listener
	daemon *Daemon
	failed chan struct{}
	once   sync.Once
}

func (listener *shutdownFailListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &shutdownFailConnection{Conn: connection, listener: listener}, nil
}

type shutdownFailConnection struct {
	net.Conn
	listener *shutdownFailListener
}

func (connection *shutdownFailConnection) Write(payload []byte) (int, error) {
	daemon := connection.listener.daemon
	daemon.admissionMu.Lock()
	pending := daemon.shutdownPending
	daemon.admissionMu.Unlock()
	if pending {
		connection.listener.once.Do(func() { close(connection.listener.failed) })
		return 0, errors.New("fixture shutdown response transport failure")
	}
	return connection.Conn.Write(payload)
}

func TestLegacyShutdownWriteFailureReleasesAdmission(t *testing.T) {
	daemon := New("fixture")
	defer daemon.Close()
	completed := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(completed)
		daemon.Handler().ServeHTTP(writer, request)
	}))
	listener := &shutdownFailListener{Listener: server.Listener, daemon: daemon, failed: make(chan struct{})}
	server.Listener = listener
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	if err := socket.Write(ctx, websocket.MessageText, []byte(`{"action":"daemon.handshake","secret":"fixture"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := socket.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if err := socket.Write(ctx, websocket.MessageText, []byte(`{"action":"daemon.shutdown"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := socket.Read(ctx); err == nil {
		t.Fatal("fixture did not fail the shutdown response write")
	}
	select {
	case <-listener.failed:
	case <-ctx.Done():
		t.Fatal("server never attempted the accepted response")
	}
	select {
	case <-completed:
	case <-ctx.Done():
		t.Fatal("failed shutdown writer did not exit")
	}
	select {
	case <-daemon.ShutdownRequested():
		t.Fatal("failed response signaled shutdown")
	default:
	}
	if _, err := daemon.AppendEvent(EventInput{SessionID: "resumed", Event: "fixture", Status: "idle"}); err != nil {
		t.Fatalf("failed shutdown response left daemon draining: %v", err)
	}
	if _, err := daemon.AuthorizeShutdown(nil, false); err != nil {
		t.Fatalf("failed response left registry draining: %v", err)
	}
	daemon.CancelShutdown()
}
