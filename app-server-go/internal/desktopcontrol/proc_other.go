//go:build !windows

package desktopcontrol

import "os/exec"

func configureHiddenCommand(_ *exec.Cmd) {}
