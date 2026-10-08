package grok

import (
	"context"
	"io"

	"astrorder.dev/session-daemon/internal/transport/ssh"
)

// SSHSession defines the minimal interface satisfied by *ssh.Session
type SSHSession interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() []byte
	Wait() (ssh.ExitStatus, error)
	Close() error
}

// SSHStarter launches a remote command over SSH
type SSHStarter func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error)

// NewWithSSHStarter creates a Grok adapter using an SSH starter
func NewWithSSHStarter(config Config, starter SSHStarter) *Adapter {
	adapter := New(config)
	adapter.sshStarter = starter
	return adapter
}
