package targetfs

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// shellOutputLimit bounds what one remote command may return (skill archives
// and config files are the largest legitimate payloads).
const shellOutputLimit = 64 << 20

// ShellResult is the outcome of a script that ran to completion on a target.
// A non-zero ExitCode is a result, not an error: the script decides what it
// means. Errors are reserved for failing to run the script at all.
type ShellResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

var shellSlots sync.Map // connection id -> chan struct{}

func shellSlot(connectionID string) chan struct{} {
	value, _ := shellSlots.LoadOrStore(connectionID, make(chan struct{}, remoteStartSlots))
	return value.(chan struct{})
}

// Shell runs `sh -c script name args...` on a remote target (a "wsl:<distro>"
// or an SSH connection id) with input on stdin. Like file operations it is
// bounded per target and retried when sshd refuses connections that arrive
// faster than MaxStartups allows.
func (r *Resolver) Shell(ctx context.Context, connectionID string, input []byte, script string, args ...string) (ShellResult, error) {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" || connectionID == LocalConnectionID {
		return ShellResult{}, errorf(400, "remote_target_required", "Shell needs a remote connection", nil)
	}
	run, err := r.remoteShellRunner(ctx, connectionID)
	if err != nil {
		return ShellResult{}, err
	}
	slot := shellSlot(connectionID)
	var last ShellResult
	for attempt := 0; attempt < remoteStartRetries; attempt++ {
		select {
		case slot <- struct{}{}:
		case <-ctx.Done():
			return ShellResult{}, ctx.Err()
		}
		result, runErr := runShellFull(ctx, run, input, script, args)
		<-slot
		if runErr != nil {
			return ShellResult{}, runErr
		}
		last = result
		if result.ExitCode != 255 || !transientSSHFailure(string(result.Stderr)) {
			return result, nil
		}
		select {
		case <-time.After(time.Duration(attempt+1) * 250 * time.Millisecond):
		case <-ctx.Done():
			return ShellResult{}, ctx.Err()
		}
	}
	return last, nil
}

func transientSSHFailure(stderr string) bool {
	return strings.Contains(stderr, "kex_exchange_identification") || strings.Contains(stderr, "Connection reset") ||
		strings.Contains(stderr, "Connection closed by") || strings.Contains(stderr, "Connection timed out during banner")
}

type capWriter struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.limit - w.buffer.Len(); room > 0 {
		if len(p) <= room {
			w.buffer.Write(p)
			return len(p), nil
		}
		w.buffer.Write(p[:room])
	}
	w.truncated = true
	return len(p), nil
}

func runShellFull(ctx context.Context, run shellRunner, input []byte, script string, args []string) (ShellResult, error) {
	cmd, err := run(ctx, script, args...)
	if err != nil {
		return ShellResult{}, err
	}
	cmd.Stdin = bytes.NewReader(input)
	stdout, stderr := &capWriter{limit: shellOutputLimit}, &capWriter{limit: 64 * 1024}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	configureChildProcess(cmd)
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return ShellResult{}, errorf(504, "shell_timeout", "Remote command timed out", ctx.Err())
	}
	var exit *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exit):
	default:
		return ShellResult{}, errorf(502, "shell_start_failed", "Failed to start the remote command", runErr)
	}
	if stdout.truncated {
		return ShellResult{}, errorf(502, "shell_output_too_large", "Remote command produced too much output", nil)
	}
	result := ShellResult{Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes()}
	if exit != nil {
		result.ExitCode = exit.ExitCode()
	}
	return result, nil
}
