package codex

import (
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestClosedCodexRuntimeCannotKeepRunningStatus(t *testing.T) {
	r := New(Config{})
	r.setSession("thread", "running")
	r.Close()
	if got := r.Status(); got != "error" {
		t.Fatalf("closed transport still reports %s", got)
	}
	r.setStatus("running")
	if got := r.Status(); got != "error" {
		t.Fatalf("late result revived closed transport: %s", got)
	}
}

func TestCodexExitedOwnerCanResumeExactThread(t *testing.T) {
	root := t.TempDir()
	a := NewAdapter(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestAdapterOwnsMultipleNativeSessions"}, Environment: []string{"ASTRORDER_ADAPTER_HELPER=1"}, Workspace: root, Allowed: []string{root}}, nil)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := a.session(created.SessionID)
	first.runtime.Close()
	select {
	case <-first.runtime.eventDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.model.read"}); !errors.Is(err, core.ErrSessionNotOwned) {
		t.Fatalf("failed model binding hid recovery: %v", err)
	}
	resumed, err := a.Spawn(ctx, core.Request{SessionID: created.SessionID, Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatalf("confirmed exited owner permanently locks thread: %v", err)
	}
	if resumed.SessionID != created.SessionID {
		t.Fatal("recovery changed native identity")
	}
	next, _ := a.session(created.SessionID)
	if next == first {
		t.Fatal("failed owner reused")
	}
}

func TestCodexEOFProjectsErrorAfterQueuedOutput(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	events := []string{}
	a := NewAdapter(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestAdapterOwnsMultipleNativeSessions"}, Environment: []string{"ASTRORDER_ADAPTER_HELPER=1"}, Workspace: root, Allowed: []string{root}}, func(_ string, event string, _ map[string]any, _ string) error {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
		return nil
	})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := a.session(result.SessionID)
	s.runtime.readLoop(strings.NewReader(""))
	select {
	case <-s.runtime.eventDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 || events[len(events)-1] != "session.error" {
		t.Fatalf("EOF failed to project final error: %v", events)
	}
	if s.runtime.recoverable() {
		t.Fatal("EOF alone permitted replacement of a live process")
	}
}

func TestCodexRecoveryRefusesUnconfirmedExitOrPendingProjection(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.transportFailed = true
	r.processDone = make(chan struct{})
	close(r.processDone)
	if r.recoverable() {
		t.Fatal("unconfirmed exit was accepted")
	}
	r.exitConfirmed = true
	if r.recoverable() {
		t.Fatal("live dispatcher was accepted")
	}
	r.Close()
	<-r.eventDone
	if !r.recoverable() {
		t.Fatal("confirmed retired runtime could not recover")
	}
}

func TestCodexDeleteRecoversExitedOwner(t *testing.T) {
	root := t.TempDir()
	a := NewAdapter(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestAdapterOwnsMultipleNativeSessions"}, Environment: []string{"ASTRORDER_ADAPTER_HELPER=1"}, Workspace: root, Allowed: []string{root}}, nil)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := a.session(result.SessionID)
	s.runtime.Close()
	<-s.runtime.eventDone
	if _, err := a.Spawn(ctx, core.Request{SessionID: result.SessionID}); err != nil {
		t.Fatal(err)
	}
	deleted, err := a.Command(ctx, core.Request{SessionID: result.SessionID, Action: "session.delete"})
	if err != nil || deleted.Payload["deleted"] != result.SessionID {
		t.Fatalf("native deletion stayed wedged: %+v %v", deleted, err)
	}
}

func TestCodexRecoveryKeepsErrorSnapshotWhenNativeWriterRefuses(t *testing.T) {
	root := t.TempDir()
	a := NewAdapter(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestAdapterOwnsMultipleNativeSessions"}, Environment: []string{"ASTRORDER_ADAPTER_HELPER=1"}, Workspace: root, Allowed: []string{root}}, nil)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := a.session(created.SessionID)
	first.runtime.Close()
	<-first.runtime.eventDone
	a.config.Allowed = []string{"/audit"}
	a.starter = func(context.Context, sshtrans.CommandSpec) (SSHSession, error) {
		f := newFakeSSHSession()
		go func() {
			d := json.NewDecoder(f.stdinR)
			e := json.NewEncoder(f.stdoutW)
			for {
				var r map[string]any
				if d.Decode(&r) != nil {
					return
				}
				if r["method"] == "initialized" {
					continue
				}
				frame := map[string]any{"id": r["id"], "result": map[string]any{}}
				if r["method"] == "thread/resume" {
					delete(frame, "result")
					frame["error"] = map[string]any{"code": -32000, "message": "already has an active writer"}
				}
				_ = e.Encode(frame)
			}
		}()
		return f, nil
	}
	if _, err := a.Spawn(ctx, core.Request{SessionID: created.SessionID, Fields: map[string]any{"cwd": "/audit"}}); err == nil || !strings.Contains(err.Error(), "active writer") {
		t.Fatalf("native writer guard bypassed: %v", err)
	}
	if got := a.Snapshot()[created.SessionID].Status; got != "error" {
		t.Fatalf("failed recovery lost error snapshot: %s", got)
	}
}
