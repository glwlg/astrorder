//go:build !windows

package acceptance

import "os/exec"

func configureHiddenProcess(_ *exec.Cmd) {}
