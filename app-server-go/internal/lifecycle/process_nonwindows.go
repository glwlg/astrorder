//go:build !windows

package lifecycle

import (
	"context"
	"os/exec"
)

func IndependentProcessFlags() uint32             { return 0x08000000 | 0x01000000 | 0x00000200 }
func configureIndependentProcess(*exec.Cmd) error { return ErrLifecycleUnsupported }
func InspectProcess(int) (ProcessIdentity, error) { return ProcessIdentity{}, ErrLifecycleUnsupported }
func ListeningPIDs(int) ([]int, error)            { return nil, ErrLifecycleUnsupported }
func acquireLifecycleLock(context.Context, string) (func(), error) {
	return nil, ErrLifecycleUnsupported
}
