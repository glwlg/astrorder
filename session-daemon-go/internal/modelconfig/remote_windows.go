//go:build windows

package modelconfig

import (
	"os/exec"
	"syscall"
)

func prepareCmdSysProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// 0x08000000 is CREATE_NO_WINDOW
	cmd.SysProcAttr.CreationFlags |= 0x08000000
}
