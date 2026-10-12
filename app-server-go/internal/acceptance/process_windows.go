//go:build windows

package acceptance

import (
	"os/exec"
	"syscall"
)

func configureHiddenProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
