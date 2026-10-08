package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDaemonShutdownExitsProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_DAEMON_CHILD") == "1" {
		main()
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	address := listener.Addr().String()
	listener.Close()
	_ = port
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonShutdownExitsProcess$")
	_, portString, _ := net.SplitHostPort(address)
	cmd.Env = append(os.Environ(), "ASTRORDER_TEST_DAEMON_CHILD=1", "ASTRORDER_SESSION_DAEMON_SECRET=shutdown-test", "ASTRORDER_SESSION_DAEMON_PORT="+portString, "ASTRORDER_SESSION_DAEMON_DB="+filepath.Join(t.TempDir(), "daemon.db"), "ASTRORDER_SESSION_DAEMON_CONFIG=")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var conn *websocket.Conn
	for {
		conn, _, err = websocket.Dial(ctx, "ws://"+address, nil)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer conn.CloseNow()
	call := func(request string, action string) {
		t.Helper()
		if err := conn.Write(ctx, websocket.MessageText, []byte(request)); err != nil {
			t.Fatal(err)
		}
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		if response["action"] != action {
			t.Fatalf("unexpected response: %s", raw)
		}
	}
	call(`{"action":"daemon.shutdown","request_id":"unauthorized"}`, "error")
	call(`{"action":"daemon.handshake","secret":"shutdown-test"}`, "daemon.handshake.result")
	call(`{"action":"daemon.shutdown","request_id":"stop"}`, "daemon.shutdown.result")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon acknowledged shutdown but process did not exit")
	}
	if c, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		c.Close()
		t.Fatal("daemon listener survived shutdown")
	}
}
