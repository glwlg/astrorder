//go:build windows

package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const StartupTaskName = "AstrorderSessionDaemon"

// QueryStartupTask checks whether the Windows Task Scheduler task is registered.
func QueryStartupTask() string {
	cmd := exec.Command("schtasks", "/query", "/tn", StartupTaskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	err := cmd.Run()
	if err == nil {
		return "installed"
	}
	return "not_installed"
}

// InstallStartupTask registers the current user logon task to launch the Session Daemon via astrorderctl.
func InstallStartupTask(ctlExecutable, stateDir, rootDir string) error {
	user := os.Getenv("USERNAME")
	if user == "" {
		user = os.Getenv("USERDOMAIN") + "\\" + os.Getenv("USERNAME")
	}
	execPath := filepath.Clean(ctlExecutable)
	taskRun := fmt.Sprintf("\"%s\" daemon start --root \"%s\" --state-dir \"%s\"", execPath, filepath.Clean(rootDir), filepath.Clean(stateDir))
	args := []string{
		"/create",
		"/tn", StartupTaskName,
		"/tr", taskRun,
		"/sc", "onlogon",
		"/rl", "limited",
		"/f",
	}
	cmd := exec.Command("schtasks", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &exec.ExitError{
			ProcessState: cmd.ProcessState,
			Stderr:       []byte(strings.TrimSpace(string(output))),
		}
	}
	return nil
}

// UninstallStartupTask removes the scheduled task if present.
func UninstallStartupTask() error {
	cmd := exec.Command("schtasks", "/delete", "/tn", StartupTaskName, "/f")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}
