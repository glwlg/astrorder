package pty

import (
	nativeprocess "astrorder.dev/session-daemon/internal/process"
	"io"
)

// Terminal defines the platform-independent interface for an interactive pseudo-terminal.
type Terminal interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

// TerminalFactory instantiates a real or simulated terminal session.
type TerminalFactory func(shell string, cwd string, env []string, cols, rows int) (Terminal, error)

func terminalEnvironment() []string {
	return nativeprocess.NativeEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"})
}
