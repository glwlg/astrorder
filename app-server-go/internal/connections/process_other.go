//go:build !windows

package connections

import "os/exec"

func configureChildProcess(_ *exec.Cmd) {}
