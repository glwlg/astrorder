//go:build windows

package digest

import (
	"os/exec"
	"syscall"
)

func configureSubprocess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
