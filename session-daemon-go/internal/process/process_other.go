//go:build !windows

package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

type Spec struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

type Process struct {
	command *exec.Cmd
}

func Start(ctx context.Context, spec Spec) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := exec.Command(spec.Path, spec.Args...)
	command.Dir = spec.Dir
	command.Env = append(os.Environ(), spec.Env...)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &Process{command: command}, nil
}

func (process *Process) PID() int {
	if process.command == nil || process.command.Process == nil {
		return 0
	}
	return process.command.Process.Pid
}

func (process *Process) Release() error {
	return nil
}

func Alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func Unsupported(detail string) error {
	return fmt.Errorf("process ownership is unavailable: %s", detail)
}
