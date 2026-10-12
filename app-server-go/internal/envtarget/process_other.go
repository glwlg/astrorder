//go:build !windows

package envtarget

import (
	"context"
	"os/exec"
)

func hiddenCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
