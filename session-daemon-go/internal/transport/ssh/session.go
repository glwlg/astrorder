package ssh

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"

	"astrorder.dev/session-daemon/internal/process"
)

// ExitStatus represents the terminal state of an SSH session.
// It strictly distinguishes between confirmed remote exits and local process terminations.
type ExitStatus struct {
	// ConfirmedRemoteExit is true ONLY when the remote process completed its execution
	// and its exit code was received via the completed SSH protocol session.
	ConfirmedRemoteExit bool

	// ExitCode is the exit status code reported by the remote process.
	// Valid only when ConfirmedRemoteExit is true.
	ExitCode int

	// LocalKilled is true when the local SSH client process was terminated locally
	// (via context cancellation, timeout, or explicit Close()).
	//
	// NOTE: LocalKilled indicates ONLY that the local process tree was cleaned up.
	// It makes NO guarantee that the remote process has terminated.
	LocalKilled bool

	// Err is any underlying error associated with the execution.
	Err error
}

// Session represents an active or completed SSH execution.
type Session struct {
	cmd         *exec.Cmd
	owner       *process.CommandOwner
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      *BoundedBuffer
	ctx         context.Context
	cancel      context.CancelFunc

	mu          sync.Mutex
	closed      bool
	localKilled bool
	waitOnce    sync.Once
	waitErr     error
	exitStatus  ExitStatus
	done        chan struct{}
}

// Stdin returns the write stream connected to the remote process standard input.
func (s *Session) Stdin() io.WriteCloser {
	return s.stdin
}

// Stdout returns the read stream connected to the remote process standard output.
func (s *Session) Stdout() io.ReadCloser {
	return s.stdout
}

// Read reads from the session stdout stream.
func (s *Session) Read(p []byte) (int, error) {
	return s.stdout.Read(p)
}

// Write writes to the session stdin stream.
func (s *Session) Write(p []byte) (int, error) {
	return s.stdin.Write(p)
}

// CloseWrite closes the session stdin stream to signal EOF to the remote process.
func (s *Session) CloseWrite() error {
	if s.stdin != nil {
		return s.stdin.Close()
	}
	return nil
}

// Stderr returns the captured stderr bytes.
func (s *Session) Stderr() []byte {
	if s.stderr == nil {
		return nil
	}
	return s.stderr.Bytes()
}

// Close terminates the local SSH subprocess tree cleanly using CommandOwner,
// and marks the session as locally killed.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.localKilled = true
	s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}

	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.stdout != nil {
		_ = s.stdout.Close()
	}

	err := s.owner.Close()
	<-s.done
	return err
}

// Wait waits for the SSH session to exit and returns the execution ExitStatus.
func (s *Session) Wait() (ExitStatus, error) {
	s.waitOnce.Do(s.waitInternal)
	return s.exitStatus, s.waitErr
}

func (s *Session) waitInternal() {
	waitErr := s.cmd.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.localKilled || (s.ctx != nil && s.ctx.Err() != nil) {
		s.exitStatus = ExitStatus{
			ConfirmedRemoteExit: false,
			ExitCode:            -1,
			LocalKilled:         true,
			Err:                 ErrSessionClosed,
		}
		if s.ctx != nil && s.ctx.Err() != nil {
			s.exitStatus.Err = s.ctx.Err()
		}
		s.waitErr = s.exitStatus.Err
		return
	}

	if waitErr == nil {
		s.exitStatus = ExitStatus{
			ConfirmedRemoteExit: true,
			ExitCode:            0,
			LocalKilled:         false,
			Err:                 nil,
		}
		s.waitErr = nil
		return
	}

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		code := exitErr.ExitCode()
		confirmedRemote := code != 255
		s.exitStatus = ExitStatus{
			ConfirmedRemoteExit: confirmedRemote,
			ExitCode:            code,
			LocalKilled:         false,
			Err:                 exitErr,
		}
		s.waitErr = exitErr
		return
	}

	s.exitStatus = ExitStatus{
		ConfirmedRemoteExit: false,
		ExitCode:            -1,
		LocalKilled:         false,
		Err:                 waitErr,
	}
	s.waitErr = waitErr
}
