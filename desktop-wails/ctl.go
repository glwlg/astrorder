package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

type CtlClient struct {
	CtlPath  string
	RootDir  string
	StateDir string
}

func NewCtlClient(rootDir, stateDir string) (*CtlClient, error) {
	ctlPath := resolveCtlExecutable(rootDir)
	if ctlPath == "" {
		return nil, errors.New("astrorderctl executable not found")
	}
	return &CtlClient{
		CtlPath:  ctlPath,
		RootDir:  rootDir,
		StateDir: stateDir,
	}, nil
}

func resolveCtlExecutable(rootDir string) string {
	candidates := []string{
		filepath.Join(rootDir, "server", "bin", "astrorderctl.exe"),
		filepath.Join(rootDir, "bin", "astrorderctl.exe"),
		filepath.Join(rootDir, "bin", "astrorderctl"),
		filepath.Join(rootDir, "app-server-go", "bin", "astrorderctl.exe"),
	}
	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append([]string{
			filepath.Join(exeDir, "server", "bin", "astrorderctl.exe"),
			filepath.Join(exeDir, "astrorderctl.exe"),
			filepath.Join(exeDir, "bin", "astrorderctl.exe"),
			filepath.Join(filepath.Dir(exeDir), "bin", "astrorderctl.exe"),
		}, candidates...)
	}

	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

func (c *CtlClient) invoke(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.CtlPath, args...)
	cmd.Dir = c.RootDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return nil, ctlInvocationError(args, stdout.Bytes(), stderr.Bytes(), err)
	}
	return stdout.Bytes(), nil
}

// invokeIndependent gives the update controller its own process group and job
// breakaway. Its output goes to a file, so losing the desktop caller cannot
// break a stdout pipe or cancel the remaining stop/replace/start phases.
func (c *CtlClient) invokeIndependent(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output, err := os.CreateTemp(c.StateDir, "daemon-update-*.log")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(c.CtlPath, args...)
	cmd.Dir = c.RootDir
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 | 0x01000000 | 0x00000200}
	if err := cmd.Start(); err != nil {
		output.Close()
		os.Remove(output.Name())
		return nil, fmt.Errorf("start independent update controller: %w", err)
	}
	output.Close()
	type outcome struct {
		body []byte
		err  error
	}
	finished := make(chan outcome, 1)
	go func() {
		err := cmd.Wait()
		body, readErr := os.ReadFile(output.Name())
		if err != nil {
			err = ctlInvocationError(args, body, nil, err)
		}
		finished <- outcome{body, errors.Join(err, readErr)}
	}()
	select {
	case result := <-finished:
		return result.body, result.err
	case <-ctx.Done():
		return nil, fmt.Errorf("update controller continues independently; result log: %s: %w", output.Name(), ctx.Err())
	}
}

func ctlInvocationError(args []string, stdout, stderr []byte, cause error) error {
	var result struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(stdout, &result) == nil {
		if result.Message != "" {
			return fmt.Errorf("%s: %w", result.Message, cause)
		}
		if result.Error != "" {
			return fmt.Errorf("%s: %w", result.Error, cause)
		}
	}
	return fmt.Errorf("invoke ctl %v failed: %w, stderr: %s", args, cause, string(stderr))
}

func (c *CtlClient) Status(ctx context.Context) (map[string]any, error) {
	res := make(map[string]any)
	for _, target := range []string{"app", "daemon"} {
		out, err := c.invoke(ctx, "service", target, "status", "--root", c.RootDir, "--state-dir", c.StateDir)
		var parsed map[string]any
		if err == nil {
			err = json.Unmarshal(out, &parsed)
		}
		if err != nil {
			// Missing or unreadable state must stay visible as unknown so callers
			// treat the daemon as possibly active.
			res[target] = map[string]any{"ok": false, "state": "unknown", "error": err.Error()}
			continue
		}
		res[target] = parsed
	}
	return res, nil
}

func (c *CtlClient) Run(ctx context.Context, target, action string, confirmActive bool) (map[string]any, error) {
	if target != "app" && target != "daemon" {
		return nil, fmt.Errorf("invalid target: %s", target)
	}
	if action != "start" && action != "stop" && action != "restart" && action != "install" && action != "update" && action != "update-check" {
		return nil, fmt.Errorf("invalid action: %s", action)
	}
	if (action == "install" || action == "update" || action == "update-check") && target != "daemon" {
		return nil, fmt.Errorf("action %s is only valid for daemon", action)
	}

	args := []string{"service", target, action, "--root", c.RootDir, "--state-dir", c.StateDir}
	if confirmActive {
		args = append(args, "--confirm-active")
	}

	var out []byte
	var err error
	if target == "daemon" && action == "update" {
		out, err = c.invokeIndependent(ctx, args...)
	} else {
		out, err = c.invoke(ctx, args...)
	}
	if err != nil {
		return nil, err
	}

	var res map[string]any
	if jsonErr := json.Unmarshal(out, &res); jsonErr != nil {
		return nil, fmt.Errorf("invalid json output from ctl: %w", jsonErr)
	}
	return res, nil
}

func (c *CtlClient) Login(ctx context.Context) (string, error) {
	out, err := c.invoke(ctx, "login", "--state-dir", c.StateDir)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(out)), nil
}
