//go:build !windows

package application

import "os/exec"

func configureChildProcess(_ *exec.Cmd) {}
