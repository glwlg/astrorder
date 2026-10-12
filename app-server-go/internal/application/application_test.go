package application

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/memory"
	"astrorder.dev/app-server/internal/store"
)

func TestEventHubPublishAndReplay(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	hub := NewEventHub(st)
	ch, unsub := hub.subscribe()
	defer unsub()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = hub.Publish(ctx, "test.event", map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Type != "test.event" || ev.Cursor <= 0 {
			t.Fatalf("unexpected event: %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}

	resync, events, err := st.ReplayEvents(context.Background(), 0)
	if err != nil || resync || len(events) == 0 {
		t.Fatalf("replay failed: resync=%v, events=%d, err=%v", resync, len(events), err)
	}
}

func TestAppTargetResolverRemoteDiscoveryAndSSHProbe(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "remote_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Add SSH connection with existing remote home in workspace
	_, err = st.SaveSSHConnection(context.Background(), map[string]any{
		"id":           "ssh-known",
		"display_name": "Server 1",
		"host":         "192.168.1.100",
		"port":         22,
		"user":         "ubuntu",
		"remote_os":    "Linux",
		"workspace":    "/home/ubuntu",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Add SSH connection without explicit remote home (should probe or fail per-target without corrupting others)
	_, err = st.SaveSSHConnection(context.Background(), map[string]any{
		"id":           "ssh-unknown",
		"display_name": "Server 2",
		"host":         "192.168.1.101",
		"port":         22,
		"user":         "root",
		"remote_os":    "Linux",
	})
	if err != nil {
		t.Fatal(err)
	}

	resolver := &AppTargetResolver{Store: st, SSHBinary: "non-existent-ssh"}
	targets, err := resolver.ResolveTargets(context.Background())
	if err != nil {
		t.Fatalf("resolve targets failed: %v", err)
	}

	foundKnown := false
	for _, target := range targets {
		if target.ID == "ssh:ssh-known" {
			foundKnown = true
			if target.Home != "/home/ubuntu" {
				t.Fatalf("expected /home/ubuntu, got %s", target.Home)
			}
		}
		if target.ID == "ssh:ssh-unknown" {
			t.Fatalf("expected unresolved target with failed probe to not be included with guessed home, got: %#v", target)
		}
	}
	if !foundKnown {
		t.Fatalf("expected known SSH target discovered, got: %#v", targets)
	}
}

func TestDispatchingRunnerAndFS(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "dispatch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runner := DispatchingRunner{
		Store:       st,
		LocalRunner: LocalRunner{},
	}
	res, err := runner.Run(context.Background(), memory.NativeCommand{
		Target: memory.Target{Kind: memory.TargetLocal},
		Name:   "cmd",
		Args:   []string{"/c", "echo hello"},
	})
	if err != nil || !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("local runner run failed: %v, out=%s", err, res.Stdout)
	}

	fs := DispatchingTargetFS{
		Store:   st,
		LocalFS: memory.LocalTargetFS{},
	}
	targetFile := filepath.Join(tempDir, "test.txt")
	err = fs.WriteFileAtomic(context.Background(), memory.Target{Kind: memory.TargetLocal}, targetFile, []byte("world"), 0o644)
	if err != nil {
		t.Fatalf("local write failed: %v", err)
	}
	read, err := fs.ReadFile(context.Background(), memory.Target{Kind: memory.TargetLocal}, targetFile)
	if err != nil || string(read) != "world" {
		t.Fatalf("local read failed: %v, got=%s", err, string(read))
	}
}

func TestLocalRunnerRejectsRemoteTarget(t *testing.T) {
	runner := LocalRunner{}
	_, err := runner.Run(context.Background(), memory.NativeCommand{
		Target: memory.Target{Kind: memory.TargetSSH},
		Name:   "echo",
		Args:   []string{"hi"},
	})
	if err == nil {
		t.Fatal("expected error when running command for SSH target on LocalRunner")
	}
}

func TestAppTargetResolverRejectsUnknownTargetAndSelects(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	resolver := &AppTargetResolver{Store: st}
	targets, err := resolver.ResolveTargets(context.Background())
	if err != nil {
		t.Fatalf("resolve targets: %v", err)
	}
	if len(targets) == 0 || targets[0].ID != memory.TargetLocal {
		t.Fatalf("expected local target, got: %#v", targets)
	}

	selected, err := resolver.SelectTargets(context.Background(), []string{memory.TargetLocal})
	if err != nil || len(selected) != 1 || selected[0].ID != memory.TargetLocal {
		t.Fatalf("select local target failed: %v, %#v", err, selected)
	}

	_, err = resolver.SelectTargets(context.Background(), []string{"unknown-target-id"})
	if err == nil {
		t.Fatal("expected error for unknown target id")
	}
}

func TestAppTargetResolverClosedDBPropagatesError(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	resolver := &AppTargetResolver{Store: st}
	_, err = resolver.ResolveTargets(context.Background())
	if err == nil {
		t.Fatal("expected error from closed database in ResolveTargets")
	}
}
