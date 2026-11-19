package antigravity

import (
	"context"
	"fmt"
	"io"

	"astrorder.dev/session-daemon/internal/transport/ssh"
)

// SSHSession defines the minimal streaming interface satisfied by *ssh.Session.
type SSHSession interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() []byte
	Wait() (ssh.ExitStatus, error)
	Close() error
}

// SSHStarter launches a remote command over SSH.
type SSHStarter func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error)

// RemoteHelperError carries optional pre-dispatch rejection metadata from the
// remote helper when no mutating RPC was sent to the native LanguageServer.
type RemoteHelperError struct {
	Message        string
	BeforeDispatch bool
}

func (e *RemoteHelperError) Error() string {
	return e.Message
}

// RemoteHelperFunc executes one structured Antigravity helper operation on the
// remote target over stdin/stdout JSON.
type RemoteHelperFunc func(ctx context.Context, payload map[string]any) (map[string]any, error)

// NewWithSSH constructs an Antigravity adapter backed by an SSH starter and
// remote helper invoker.
func NewWithSSH(c Config, starter SSHStarter, helper RemoteHelperFunc) *Adapter {
	if c.AgentID == "" && c.ConnectionID != "" {
		c.AgentID = fmt.Sprintf("ssh-antigravity-%s", c.ConnectionID)
	}
	a := New(c)
	a.sshStarter = starter
	a.remoteHelper = helper
	return a
}
