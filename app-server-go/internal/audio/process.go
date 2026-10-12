package audio

import (
	"context"
	"os/exec"
)

// ProcessFactory allows injecting mock/fake process creation in tests.
type ProcessFactory func(ctx context.Context, name string, args ...string) (*exec.Cmd, error)
