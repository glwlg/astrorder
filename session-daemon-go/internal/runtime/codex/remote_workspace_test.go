package codex

import (
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"testing"
)

func TestRemoteWorkspaceAdaptiveFallback(t *testing.T) {
	starter := func(context.Context, sshtrans.CommandSpec) (SSHSession, error) {
		t.Fatal("workspace validation started SSH")
		return nil, nil
	}
	// Case 1: Configured POSIX workspace fallback on Windows path
	rt := NewWithSSHStarter(Config{
		Workspace: "/home/luwei",
		Allowed:   []string{"/home/luwei"},
	}, starter)
	ws, err := rt.workspace(`P:\DevApp\Astrorder\server`)
	if err != nil || ws != "/home/luwei" {
		t.Fatalf("expected fallback to /home/luwei, got %q, err: %v", ws, err)
	}

	// Case 2: No configured workspace, fallback to "/"
	rt2 := NewWithSSHStarter(Config{
		Allowed: []string{"/"},
	}, starter)
	ws, err = rt2.workspace(`C:\Some\Path`)
	if err != nil || ws != "/" {
		t.Fatalf("expected fallback to /, got %q, err: %v", ws, err)
	}

	// Case 3: Explicit valid POSIX path
	rt3 := NewWithSSHStarter(Config{
		Workspace: "/home/luwei",
		Allowed:   []string{"/var/data"},
	}, starter)
	ws, err = rt3.workspace("/var/data")
	if err != nil || ws != "/var/data" {
		t.Fatalf("expected /var/data, got %q, err: %v", ws, err)
	}
}
