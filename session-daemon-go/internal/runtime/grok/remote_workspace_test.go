package grok

import (
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"testing"
)

func TestRemoteWorkspaceAllowlistBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, root, dir string
		allowed         bool
	}{
		{"root subtree", "/", "/tmp", true},
		{"root itself", "/", "/", true},
		{"subtree", "/work", "/work/project", true},
		{"exact", "/work", "/work", true},
		{"sibling prefix", "/work", "/workspace", false},
		{"parent escape", "/work", "/work/../elsewhere", false},
		{"relative", "/work", "tmp", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewWithSSHStarter(Config{Allowed: []string{tc.root}}, func(context.Context, sshtrans.CommandSpec) (SSHSession, error) {
				t.Fatal("workspace validation started SSH")
				return nil, nil
			})
			_, err := a.workspace(tc.dir)
			if (err == nil) != tc.allowed {
				t.Fatalf("workspace %q under %q: %v", tc.dir, tc.root, err)
			}
		})
	}
}

func TestRemoteWorkspaceAdaptiveFallback(t *testing.T) {
	starter := func(context.Context, sshtrans.CommandSpec) (SSHSession, error) {
		t.Fatal("workspace validation started SSH")
		return nil, nil
	}
	// Case 1: Configured POSIX workspace fallback on Windows path
	a := NewWithSSHStarter(Config{
		Workspace: "/home/luwei",
		Allowed:   []string{"/home/luwei"},
	}, starter)
	ws, err := a.workspace(`P:\DevApp\Astrorder\server`)
	if err != nil || ws != "/home/luwei" {
		t.Fatalf("expected fallback to /home/luwei, got %q, err: %v", ws, err)
	}

	// Case 2: No configured workspace, fallback to "/"
	b := NewWithSSHStarter(Config{
		Allowed: []string{"/"},
	}, starter)
	ws, err = b.workspace(`C:\Some\Path`)
	if err != nil || ws != "/" {
		t.Fatalf("expected fallback to /, got %q, err: %v", ws, err)
	}

	// Case 3: Explicit valid POSIX path
	c := NewWithSSHStarter(Config{
		Workspace: "/home/luwei",
		Allowed:   []string{"/var/data"},
	}, starter)
	ws, err = c.workspace("/var/data")
	if err != nil || ws != "/var/data" {
		t.Fatalf("expected /var/data, got %q, err: %v", ws, err)
	}
}
