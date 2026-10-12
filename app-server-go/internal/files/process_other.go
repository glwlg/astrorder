//go:build !windows

package files

import "os/exec"

func configureProcess(_ *exec.Cmd) {}
