//go:build !windows

package targetfs

import "os/exec"

func configureChildProcess(_ *exec.Cmd) {}
