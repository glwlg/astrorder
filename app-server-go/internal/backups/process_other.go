//go:build !windows

package backups

import (
	"context"
	"os/exec"
)

func configureChildProcess(_ *exec.Cmd) {}

func localRestoreHookCommand(ctx context.Context, command string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "/bin/sh", "-c", command), nil
}
