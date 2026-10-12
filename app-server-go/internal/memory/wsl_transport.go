package memory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var wslDistroPattern = regexp.MustCompile(`^[\p{L}\p{N}._+-]{1,128}$`)

type WSLCommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type WSLCommandExecutor interface {
	Execute(context.Context, string, []string, []byte) (WSLCommandResult, error)
}

type NativeWSLCommandExecutor struct{}

func (NativeWSLCommandExecutor) Execute(ctx context.Context, executable string, args []string, stdin []byte) (WSLCommandResult, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := WSLCommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if exitError, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitError.ExitCode()
	}
	return result, err
}

// WSLTransport invokes a pre-installed native helper over framed stdin/stdout.
// It never invokes sh, PowerShell, Python, or command-string interpolation.
type WSLTransport struct {
	Executable string
	HelperPath string
	Executor   WSLCommandExecutor
}

func (t WSLTransport) Exchange(ctx context.Context, target Target, frame []byte) ([]byte, error) {
	if target.Kind != TargetWSL {
		return nil, fmt.Errorf("WSL transport cannot serve %s target", target.Kind)
	}
	if !wslDistroPattern.MatchString(target.Distro) {
		return nil, errors.New("invalid WSL distribution identifier")
	}
	executable := t.Executable
	if executable == "" {
		executable = "wsl.exe"
	}
	if strings.TrimSpace(t.HelperPath) == "" {
		return nil, errors.New("WSL memory helper path is required")
	}
	executor := t.Executor
	if executor == nil {
		executor = NativeWSLCommandExecutor{}
	}
	result, err := executor.Execute(ctx, executable, []string{"-d", target.Distro, "--exec", t.HelperPath, "target-fs-v1"}, frame)
	if err != nil || result.ExitCode != 0 {
		message := strings.TrimSpace(string(result.Stderr))
		if message == "" && err != nil {
			message = err.Error()
		}
		if message == "" {
			message = fmt.Sprintf("exit code %d", result.ExitCode)
		}
		return nil, fmt.Errorf("WSL target filesystem helper failed: %s", message)
	}
	return result.Stdout, nil
}
