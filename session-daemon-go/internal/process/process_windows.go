package process

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
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
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008 | 0x01000000,
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &Process{command: command}, nil
}

func (process *Process) PID() int {
	if process.command.Process == nil {
		return 0
	}
	return process.command.Process.Pid
}

func (process *Process) Release() error {
	return nil
}

func Alive(pid int) bool {
	pidText := strconv.Itoa(pid)
	snapshot, err := exec.Command("tasklist", "/FI", "PID eq "+pidText, "/NH", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return bytes.Contains(snapshot, []byte("\""+pidText+"\""))
}
