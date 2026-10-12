//go:build windows

package envtarget

import (
	"context"
	"os/exec"
	"syscall"
)

func hiddenCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	return cmd
}
