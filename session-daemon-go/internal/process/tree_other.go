//go:build !windows

package process

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
)

type CommandOwner struct {
	mu     sync.Mutex
	pid    int
	closed bool
	err    error
}

func StartOwnedCommand(cmd *exec.Cmd) (*CommandOwner, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &CommandOwner{pid: cmd.Process.Pid}, nil
}

func (o *CommandOwner) PID() int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pid
}
func (o *CommandOwner) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return o.err
	}
	o.closed = true
	err := syscall.Kill(-o.pid, syscall.SIGKILL)
	if !errors.Is(err, syscall.ESRCH) {
		o.err = err
	}
	return o.err
}
