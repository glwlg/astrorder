//go:build windows

package desktopcontrol

import (
	"os/exec"
	"syscall"
)

const createNoWindowFlag = 0x08000000

func configureHiddenCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindowFlag,
	}
}
