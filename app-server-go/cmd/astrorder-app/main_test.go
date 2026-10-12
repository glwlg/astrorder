package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/config"
)

func TestConfiguredMigrationUpstreamIsRejected(t *testing.T) {
	cfg := config.Config{Listen: "127.0.0.1:0", Migration: config.MigrationConfig{Upstream: "http://127.0.0.1:1"}}
	err := runServer(context.Background(), cfg, io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "no longer supported") {
		t.Fatalf("migration upstream was not rejected: %v", err)
	}
}

func TestRunServerEmitsReadyAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readyReader, readyWriter := io.Pipe()
	defer readyReader.Close()
	result := make(chan error, 1)
	cfg := config.Config{Listen: "127.0.0.1:0"}
	go func() {
		result <- runServer(ctx, cfg, readyWriter, slog.New(slog.NewTextHandler(io.Discard, nil)))
		_ = readyWriter.Close()
	}()

	var payload struct {
		Ready   bool   `json:"ready"`
		Address string `json:"address"`
		PID     int    `json:"pid"`
	}
	if err := json.NewDecoder(readyReader).Decode(&payload); err != nil {
		t.Fatalf("decode ready output: %v", err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !payload.Ready || payload.Address == "" || payload.PID <= 0 || strings.Contains(string(encoded), "secret") {
		t.Fatalf("ready payload = %#v", payload)
	}

	response, err := http.Get("http://" + payload.Address + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("runServer: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down gracefully")
	}
}

func TestRunServerDrainsHijackedConnections(t *testing.T) {
	tracker := newHijackedConnections()
	defer tracker.CloseAll()
	upstreamClosed := make(chan struct{})
	upstream := http.Server{ConnState: tracker.Observe, Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, _, err := http.NewResponseController(response).Hijack()
		if err != nil {
			t.Errorf("upstream hijack: %v", err)
			return
		}
		defer connection.Close()
		_, _ = io.WriteString(connection, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_, _ = io.Copy(io.Discard, connection)
		close(upstreamClosed)
	})}
	upstreamListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstreamListener.Close()
	go func() { _ = upstream.Serve(upstreamListener) }()
	defer upstream.Close()

	ready := readyMessage{Address: upstreamListener.Addr().String()}

	connection, err := net.Dial("tcp", ready.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, err = io.WriteString(connection, "GET /ws/v1/events?token=browser-secret HTTP/1.1\r\nHost: "+ready.Address+"\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 256)
	if _, err := connection.Read(buffer); err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}

	tracker.CloseAll()
	select {
	case <-upstreamClosed:
	case <-time.After(time.Second):
		t.Fatal("upstream hijacked connection remained open")
	}
}
