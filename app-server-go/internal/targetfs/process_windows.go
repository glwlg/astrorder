//go:build windows

package targetfs

import (
	"os/exec"
	"syscall"
)

func configureChildProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
}
