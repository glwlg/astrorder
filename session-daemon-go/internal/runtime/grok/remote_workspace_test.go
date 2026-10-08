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
		{"relative", "/", "tmp", false},
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
