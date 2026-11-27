//go:build !windows

package pluginhost

import (
	"os/exec"
	"syscall"
)

type pluginProcessGuard struct{}

func configurePluginProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

func startPluginProcess(cmd *exec.Cmd) (*pluginProcessGuard, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &pluginProcessGuard{}, nil
}

func (g *pluginProcessGuard) Terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
}

func (g *pluginProcessGuard) ForceKill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = cmd.Process.Kill()
}

func (g *pluginProcessGuard) Release() error {
	return nil
}
