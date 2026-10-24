package codex

import (
	"context"
	"fmt"
)

// SSH Close kills the local client and destroys remote-exit evidence. For an
// idle close, deliver EOF first and wait for native exit before cleanup.
func (r *Runtime) closeIdleTransport(ctx context.Context) error {
	r.mu.Lock()
	remote, stdin, done := r.sshSession != nil, r.stdin, r.processDone
	r.mu.Unlock()
	if !remote {
		r.Close()
		return nil
	}
	if done == nil {
		return fmt.Errorf("Codex remote exit is unavailable")
	}
	if stdin != nil {
		if err := stdin.Close(); err != nil {
			return fmt.Errorf("Codex remote EOF failed: %w", err)
		}
	}
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	r.mu.Lock()
	confirmed := r.exitConfirmed
	r.mu.Unlock()
	if !confirmed {
		return fmt.Errorf("Codex remote exit was not confirmed")
	}
	r.Close()
	return nil
}
