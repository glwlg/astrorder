//go:build windows

package process

import "os/exec"

// Windows owns a Job handle, not a reusable numeric process group.
func (o *CommandOwner) WaitAndClose(cmd *exec.Cmd) error { _ = cmd.Wait(); return o.Close() }
