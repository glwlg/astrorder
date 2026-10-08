package ssh

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"astrorder.dev/session-daemon/internal/process"
)

// Transport manages remote command execution over OpenSSH.
type Transport struct {
	config Config
}

// New creates a new Transport after validating configuration.
func New(config Config) (*Transport, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Transport{config: config}, nil
}

// Start launches the remote command asynchronously and returns an active Session
// providing full-duplex stdin/stdout and bounded stderr.
func (t *Transport) Start(ctx context.Context, spec CommandSpec) (*Session, error) {
	remoteCmd, err := BuildRemoteCommand(spec)
	if err != nil {
		return nil, err
	}

	argv, err := t.config.BuildArgv(remoteCmd)
	if err != nil {
		return nil, err
	}

	bootstrap, err := environmentBootstrap(spec.Environment)
	if err != nil {
		return nil, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = process.NativeEnvironment(t.config.MockEnv)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return nil, err
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		cancel()
		return nil, err
	}

	owner, err := process.StartOwnedCommand(cmd)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderrPipe.Close()
		cancel()
		return nil, err
	}

	maxStderr := t.config.MaxStderrBytes
	if maxStderr <= 0 {
		maxStderr = 64 * 1024
	}
	stderrBuf := NewBoundedBuffer(maxStderr)

	sess := &Session{
		cmd:    cmd,
		owner:  owner,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderrBuf,
		ctx:    childCtx,
		cancel: cancel,
		done:   make(chan struct{}),
	}

	// Drain stderr in background to avoid pipe deadlocks and store bounded output
	go func() {
		defer stderrPipe.Close()
		buf := make([]byte, 4096)
		for {
			n, readErr := stderrPipe.Read(buf)
			if n > 0 {
				_, _ = stderrBuf.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
	}()

	// Monitor context cancellation and kill process tree via owner if context expires
	go func() {
		select {
		case <-childCtx.Done():
			sess.mu.Lock()
			sess.localKilled = true
			sess.mu.Unlock()
			_ = owner.Close()
		case <-sess.done:
		}
	}()

	// Wait loop in background to notify sess.done
	go func() {
		_, _ = sess.Wait()
		close(sess.done)
	}()

	if len(bootstrap) > 0 {
		written := make(chan error, 1)
		go func() { _, err := stdin.Write(bootstrap); written <- err }()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case err := <-written:
			if err != nil {
				_ = sess.Close()
				return nil, fmt.Errorf("remote environment bootstrap failed")
			}
		case <-timer.C:
			_ = sess.Close()
			return nil, fmt.Errorf("remote environment bootstrap timed out")
		case <-ctx.Done():
			_ = sess.Close()
			return nil, ctx.Err()
		}
	}
	return sess, nil
}

// Execute is a convenience helper that runs a command to completion, returning
// captured stdout, stderr, and ExitStatus.
func (t *Transport) Execute(ctx context.Context, spec CommandSpec) ([]byte, []byte, ExitStatus, error) {
	sess, err := t.Start(ctx, spec)
	if err != nil {
		return nil, nil, ExitStatus{}, err
	}
	defer sess.Close()

	stdoutBytes, readErr := io.ReadAll(sess.Stdout())
	status, waitErr := sess.Wait()

	if readErr != nil && waitErr == nil {
		return stdoutBytes, sess.Stderr(), status, readErr
	}
	return stdoutBytes, sess.Stderr(), status, waitErr
}
