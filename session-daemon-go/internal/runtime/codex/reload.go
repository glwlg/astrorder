package codex

import (
	"context"
	"fmt"
)

// ReloadConfig matches Python's catalog-only reload semantics. This adapter
// owns no shared catalog process: every new session already starts a fresh
// app-server which reads native files/environment. Never replace live session
// transports or claim existing threads adopted a newly written catalog.
func (a *Adapter) ReloadConfig(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fmt.Errorf("codex adapter is closed")
	}
	return nil
}
