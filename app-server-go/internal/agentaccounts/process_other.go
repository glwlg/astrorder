//go:build !windows

package agentaccounts

import (
	"context"
	"os/exec"
)

func hiddenCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
