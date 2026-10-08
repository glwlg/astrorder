package hermes

import (
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"testing"
)

func TestRemoteWorkspacePreservesPOSIXBackslashes(t *testing.T) {
	starter := func(context.Context, sshtrans.CommandSpec) (SSHSession, error) {
		t.Fatal("validation started SSH")
		return nil, nil
	}
	a := NewWithSSHStarter(Config{Allowed: []string{"/work"}}, starter)
	// A backslash is an ordinary filename character on the remote POSIX host.
	input := `/work/a\b`
	got, err := a.validateWorkspace(input)
	if err != nil || got != input {
		t.Fatalf("remote path changed: %q %v", got, err)
	}
	if _, err := a.validateWorkspace(`/work\outside`); err == nil {
		t.Fatal("backslash allowed a sibling outside the POSIX root")
	}
	b := NewWithSSHStarter(Config{Allowed: []string{`/work/a\b`}}, starter)
	if got, err := b.validateWorkspace(`/work/a\b/child`); err != nil || got != `/work/a\b/child` {
		t.Fatalf("remote allowlist root changed: %q %v", got, err)
	}
}
