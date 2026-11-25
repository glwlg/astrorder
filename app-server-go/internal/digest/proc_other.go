//go:build !windows

package digest

import "os/exec"

func configureSubprocess(_ *exec.Cmd) {}
