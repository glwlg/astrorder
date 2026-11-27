package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
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

type stubResolver struct {
	calls int
	path  string
	err   error
}

func (s *stubResolver) Resolve(_ context.Context, _ string, sshConn map[string]any) (string, error) {
	s.calls++
	if explicit, _ := sshConn["antigravity_executable"].(string); explicit != "" {
		return explicit, nil
	}
	return s.path, s.err
}

func TestCachedRemoteExecutableResolverCachesPositiveAndRetriesErrors(t *testing.T) {
	inner := &stubResolver{err: errors.New("ssh offline")}
	cached := newCachedRemoteExecutableResolver(inner, 30*time.Second)
	conn := map[string]any{"id": "wsl", "host": "127.0.0.1", "port": 22, "user": "fixture"}

	// Errors must never be cached.
	if _, err := cached.Resolve(context.Background(), "antigravity", conn); err == nil {
		t.Fatal("expected error when inner fails")
	}
	inner.err = nil
	inner.path = "/usr/local/bin/agy"

	got1, err := cached.Resolve(context.Background(), "antigravity", conn)
	if err != nil || got1 != "/usr/local/bin/agy" || inner.calls != 2 {
		t.Fatalf("unexpected first positive resolution: got=%q err=%v calls=%d", got1, err, inner.calls)
	}

	// Second resolution within TTL reuses cached positive path without extra probe.
	got2, err := cached.Resolve(context.Background(), "antigravity", conn)
	if err != nil || got2 != "/usr/local/bin/agy" || inner.calls != 2 {
		t.Fatalf("expected cached hit without extra inner call: got=%q err=%v calls=%d", got2, err, inner.calls)
	}

	// Explicit override bypasses cached path.
	got3, err := cached.Resolve(context.Background(), "antigravity", map[string]any{
		"id": "wsl", "host": "127.0.0.1", "port": 22, "user": "fixture",
		"antigravity_executable": "/custom/agy",
	})
	if err != nil || got3 != "/custom/agy" {
		t.Fatalf("expected explicit override /custom/agy, got=%q err=%v", got3, err)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunServerDegradedStartupWhenDaemonUnreachable(t *testing.T) {
	// Allocate a loopback port and close it immediately so the daemon target is unreachable.
	closedLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unreachableTarget := closedLn.Addr().String()
	_ = closedLn.Close()

	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logBuf := &syncBuffer{}
	readyReader, readyWriter := io.Pipe()
	defer readyReader.Close()

	cfg := config.Config{
		Listen:           "127.0.0.1:0",
		DatabasePath:     filepath.Join(root, "app.db"),
		AttachmentsDir:   filepath.Join(root, "attachments"),
		DefaultWorkspace: root,
		BrowserSecret:    "degraded-browser-secret",
		ConnectorSecret:  "degraded-connector-secret",
		Daemon: config.DaemonConfig{
			Target:         unreachableTarget,
			Credential:     "degraded-daemon-cred",
			RequestTimeout: 300 * time.Millisecond,
		},
	}

	result := make(chan error, 1)
	go func() {
		err := runServer(ctx, cfg, readyWriter, slog.New(slog.NewJSONHandler(logBuf, nil)))
		_ = readyWriter.CloseWithError(err)
		result <- err
	}()

	var ready readyMessage
	if err := json.NewDecoder(readyReader).Decode(&ready); err != nil {
		t.Fatalf("expected degraded startup to emit ready message instead of crashing: %v (logs: %s)", err, logBuf.String())
	}
	if !ready.Ready || ready.Address == "" {
		t.Fatalf("unexpected ready payload: %+v", ready)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	checkGet := func(path string, auth bool) []byte {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "http://"+ready.Address+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth {
			req.Header.Set("Authorization", "Bearer "+cfg.BrowserSecret)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, resp.StatusCode, string(body))
		}
		return body
	}

	healthBody := checkGet("/health", false)
	if !strings.Contains(string(healthBody), `"status":"ok"`) {
		t.Fatalf("unexpected /health body: %s", string(healthBody))
	}

	targetsBody := checkGet("/api/v1/toolchain/targets", true)
	if !strings.Contains(string(targetsBody), `"id":"local"`) {
		t.Fatalf("expected local target in degraded toolchain API, got: %s", string(targetsBody))
	}

	_ = checkGet("/api/v1/preferences", true)

	cancel()
	select {
	case runErr := <-result:
		if runErr != nil {
			t.Fatalf("degraded server did not shut down cleanly: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("degraded server timed out during graceful shutdown")
	}

	if !strings.Contains(logBuf.String(), "starting in degraded mode with background reconnect") {
		t.Fatalf("expected structured degraded-mode startup log, got: %s", logBuf.String())
	}
}
