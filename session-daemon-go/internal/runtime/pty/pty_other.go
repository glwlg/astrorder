//go:build !linux && !windows

package pty

import "errors"

func defaultPlatformTerminal(shell string, cwd string, env []string, cols, rows int) (Terminal, error) {
	return nil, errors.New("pty runtime is not supported on this platform")
}

func findDefaultShell() string {
	return "/bin/sh"
}
