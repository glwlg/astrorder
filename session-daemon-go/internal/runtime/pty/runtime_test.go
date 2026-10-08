package pty_test

import (
	"astrorder.dev/session-daemon/internal/events"
	"context"
	"os"
	"path/filepath"
	platform "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/pty"
)

type emittedEvent struct {
	SessionID string
	Event     string
	Payload   map[string]any
	Status    string
}

func setupTestWorkspace(t *testing.T) (allowedRoot, subDir string) {
	t.Helper()
	root := t.TempDir()
	sub := filepath.Join(root, "allowed-sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}
	return root, sub
}

func TestPTYCreateUnsupported(t *testing.T) {
	root, _ := setupTestWorkspace(t)
	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	_, err = rt.Create(context.Background(), core.Request{
		AgentType: "pty",
		SessionID: "sess-1",
	})
	if err == nil {
		t.Fatal("expected Create to return error, got nil")
	}
	if !strings.Contains(err.Error(), "spawn") {
		t.Fatalf("expected error mentioning spawn, got: %v", err)
	}
}

func TestPTYSpawnAllowlistAndSymlink(t *testing.T) {
	root, sub := setupTestWorkspace(t)
	outsideDir := t.TempDir()

	symlinkEscape := filepath.Join(root, "escape-link")
	if err := os.Symlink(outsideDir, symlinkEscape); err != nil {
		t.Fatalf("failed to create escape symlink: %v", err)
	}

	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	// 1. Outside directory
	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-outside",
		Fields: map[string]any{
			"cwd": outsideDir,
		},
	})
	if err == nil {
		t.Fatal("expected spawn outside allowlist to fail")
	}

	// 2. Symlink escaping allowlist
	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-symlink",
		Fields: map[string]any{
			"cwd": symlinkEscape,
		},
	})
	if err == nil {
		t.Fatal("expected spawn via escaping symlink to fail")
	}

	// 3. Inside allowed sub directory
	res, err := rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-valid",
		Fields: map[string]any{
			"cwd": sub,
		},
	})
	if err != nil {
		t.Fatalf("expected spawn inside allowlist to succeed, got: %v", err)
	}
	if res.Status != "running" {
		t.Fatalf("expected status running, got: %s", res.Status)
	}
}

func TestPTYSessionSendValidationAndClosed(t *testing.T) {
	root, sub := setupTestWorkspace(t)
	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-send",
		Fields: map[string]any{
			"cwd": sub,
		},
	})
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}

	// Empty input
	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-send",
		Action:    "session.send",
		Fields: map[string]any{
			"input": "",
		},
	})
	if err == nil {
		t.Fatal("expected empty input to be rejected")
	}

	// Oversized input > 65536
	huge := strings.Repeat("a", 65537)
	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-send",
		Action:    "session.send",
		Fields: map[string]any{
			"input": huge,
		},
	})
	if err == nil {
		t.Fatal("expected input > 65536 to be rejected")
	}

	// Valid input <= 65536
	res, err := rt.Command(context.Background(), core.Request{
		SessionID: "sess-send",
		Action:    "session.send",
		Fields: map[string]any{
			"input": "echo hello\n",
		},
	})
	if err != nil {
		t.Fatalf("valid send failed: %v", err)
	}
	if res.Status != "running" {
		t.Fatalf("expected status running, got %s", res.Status)
	}

	// Close session
	closeRes, err := rt.Command(context.Background(), core.Request{
		SessionID: "sess-send",
		Action:    "session.close",
	})
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if closeRes.Status != "idle" {
		t.Fatalf("expected idle status on close, got %s", closeRes.Status)
	}

	// Writing to closed session must fail
	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-send",
		Action:    "session.send",
		Fields: map[string]any{
			"input": "echo after close\n",
		},
	})
	if err == nil {
		t.Fatal("expected send to closed session to fail")
	}
}

func TestPTYResizeValidation(t *testing.T) {
	root, sub := setupTestWorkspace(t)
	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-resize",
		Fields: map[string]any{
			"cwd": sub,
		},
	})
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}

	// cols out of range: 19 (< 20)
	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-resize",
		Action:    "session.resize",
		Fields: map[string]any{
			"cols": 19,
			"rows": 50,
		},
	})
	if err == nil {
		t.Fatal("expected cols 19 to fail")
	}

	// cols out of range: 401 (> 400)
	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-resize",
		Action:    "session.resize",
		Fields: map[string]any{
			"cols": 401,
			"rows": 50,
		},
	})
	if err == nil {
		t.Fatal("expected cols 401 to fail")
	}

	// rows out of range: 9 (< 10)
	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-resize",
		Action:    "session.resize",
		Fields: map[string]any{
			"cols": 80,
			"rows": 9,
		},
	})
	if err == nil {
		t.Fatal("expected rows 9 to fail")
	}

	// rows out of range: 201 (> 200)
	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-resize",
		Action:    "session.resize",
		Fields: map[string]any{
			"cols": 80,
			"rows": 201,
		},
	})
	if err == nil {
		t.Fatal("expected rows 201 to fail")
	}

	// Valid resize
	res, err := rt.Command(context.Background(), core.Request{
		SessionID: "sess-resize",
		Action:    "session.resize",
		Fields: map[string]any{
			"cols": 120,
			"rows": 30,
		},
	})
	if err != nil {
		t.Fatalf("valid resize failed: %v", err)
	}
	if res.Status != "running" {
		t.Fatalf("expected running status, got: %s", res.Status)
	}
}

func TestPTYGlobalLockFreeDuringIO(t *testing.T) {
	root, sub := setupTestWorkspace(t)
	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	// Spawn two sessions
	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-1",
		Fields:    map[string]any{"cwd": sub},
	})
	if err != nil {
		t.Fatalf("spawn sess-1 error: %v", err)
	}

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-2",
		Fields:    map[string]any{"cwd": sub},
	})
	if err != nil {
		t.Fatalf("spawn sess-2 error: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_, _ = rt.Command(context.Background(), core.Request{
				SessionID: "sess-1",
				Action:    "session.send",
				Fields:    map[string]any{"input": "echo tick\n"},
			})
			time.Sleep(5 * time.Millisecond)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			sn := rt.Snapshot()
			if len(sn) < 2 {
				t.Errorf("expected at least 2 sessions in snapshot, got %d", len(sn))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	wg.Wait()
}

func TestPTYBrowserDisconnectDoesNotClosePTY(t *testing.T) {
	root, sub := setupTestWorkspace(t)

	journal := events.New(64)
	subscriber := journal.Subscribe()
	journal.Unsubscribe(subscriber)

	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			_, err := journal.Commit(sessionID, event, payload, status)
			return err
		},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-disc",
		Fields:    map[string]any{"cwd": sub},
	})
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}

	// Send command while browser is disconnected
	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-disc",
		Action:    "session.send",
		Fields:    map[string]any{"input": "echo still_alive\n"},
	})
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Session must still be running
	sn := rt.Snapshot()
	sess, ok := sn["sess-disc"]
	if !ok {
		t.Fatal("session was unexpectedly removed")
	}
	if sess.Status != "running" {
		t.Fatalf("session should still be running despite browser disconnect, got: %s", sess.Status)
	}
}

func TestPTYRealOutputAndExitEvents(t *testing.T) {
	root, sub := setupTestWorkspace(t)
	// Use a deterministic native shell rather than user startup profiles.
	shell := "/bin/sh"
	input := "stty -echo; printf '%s%s\\n' PTY_OUTPUT_ MAGIC; exit 0\n"
	if platform.GOOS == "windows" {
		shell = os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
		input = "@echo off\r\nset PTY_MARKER=PTY_OUTPUT_\r\necho %PTY_MARKER%MAGIC\r\nexit 0\r\n"
	}

	var eventsMu sync.Mutex
	var events []emittedEvent
	eventArrived := make(chan struct{}, 100)

	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
		Shell:   shell,
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			eventsMu.Lock()
			events = append(events, emittedEvent{
				SessionID: sessionID,
				Event:     event,
				Payload:   payload,
				Status:    status,
			})
			eventsMu.Unlock()
			select {
			case eventArrived <- struct{}{}:
			default:
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-events",
		Fields:    map[string]any{"cwd": sub},
	})
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}

	// Send echo command and exit
	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-events",
		Action:    "session.send",
		Fields:    map[string]any{"input": input},
	})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}

	// Wait for pty.output and pty.closed
	timeout := time.After(3 * time.Second)
	var gotOutput, gotClosed bool
	for !gotClosed {
		select {
		case <-eventArrived:
			eventsMu.Lock()
			for _, ev := range events {
				if ev.Event == "pty.output" {
					if data, ok := ev.Payload["data"].(string); ok && strings.Contains(data, "PTY_OUTPUT_MAGIC") {
						gotOutput = true
					}
				}
				if ev.Event == "pty.closed" {
					gotClosed = true
				}
			}
			eventsMu.Unlock()
		case <-timeout:
			t.Fatalf("timed out waiting for events, gotOutput: %v, gotClosed: %v", gotOutput, gotClosed)
		}
	}

	if !gotOutput {
		t.Fatal("did not receive expected pty.output event with magic string")
	}
	if !gotClosed {
		t.Fatal("did not receive expected pty.closed event")
	}
}

func TestPTYSessionInterrupt(t *testing.T) {
	root, sub := setupTestWorkspace(t)
	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-interrupt",
		Fields:    map[string]any{"cwd": sub},
	})
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}

	res, err := rt.Command(context.Background(), core.Request{
		SessionID: "sess-interrupt",
		Action:    "session.interrupt",
	})
	if err != nil {
		t.Fatalf("interrupt failed: %v", err)
	}
	if res.Status != "running" {
		t.Fatalf("expected status running, got: %s", res.Status)
	}
}

func TestPTYDuplicateSpawnRejected(t *testing.T) {
	root, sub := setupTestWorkspace(t)
	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-dup",
		Fields:    map[string]any{"cwd": sub},
	})
	if err != nil {
		t.Fatalf("first spawn error: %v", err)
	}

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-dup",
		Fields:    map[string]any{"cwd": sub},
	})
	if err == nil {
		t.Fatal("expected second spawn to fail with duplicate session error")
	}
	if !strings.Contains(err.Error(), "already daemon-owned") {
		t.Fatalf("expected already daemon-owned error, got: %v", err)
	}
}

func TestPTYUnsupportedAction(t *testing.T) {
	root, sub := setupTestWorkspace(t)
	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}
	defer rt.Close()

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-unsupported",
		Fields:    map[string]any{"cwd": sub},
	})
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}

	_, err = rt.Command(context.Background(), core.Request{
		SessionID: "sess-unsupported",
		Action:    "session.invalid_action",
	})
	if err == nil {
		t.Fatal("expected unsupported action to return error")
	}
}

func TestPTYRuntimeClose(t *testing.T) {
	root, sub := setupTestWorkspace(t)
	rt, err := pty.New(pty.Config{
		Allowed: []string{root},
	})
	if err != nil {
		t.Fatalf("unexpected New error: %v", err)
	}

	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-close-all",
		Fields:    map[string]any{"cwd": sub},
	})
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Spawning after close must fail
	_, err = rt.Spawn(context.Background(), core.Request{
		SessionID: "sess-after-close",
		Fields:    map[string]any{"cwd": sub},
	})
	if err == nil {
		t.Fatal("expected spawn after Close to fail")
	}
}
