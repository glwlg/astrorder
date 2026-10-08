//go:build linux

package process

import (
	"errors"
	"golang.org/x/sys/unix"
	"os/exec"
)

// WaitAndClose observes a zombie without reaping it. Its PID remains reserved
// until the owned process group has been signalled, so teardown cannot hit a
// recycled unrelated group. This owner is the command's sole waiter.
func (o *CommandOwner) WaitAndClose(cmd *exec.Cmd) error {
	var info unix.Siginfo
	var err error
	for {
		err = unix.Waitid(unix.P_PID, o.pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		_ = cmd.Wait()
		return err
	}
	cleanup := o.Close()
	_ = cmd.Wait()
	return cleanup
}
