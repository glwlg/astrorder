package grok

import (
	"context"
	"fmt"
)

// Grok has no shared catalog client. Config is read by each fresh ACP process;
// existing native sessions retain their model/transport, matching Python.
func (a *Adapter) ReloadConfig(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.closed {
		return fmt.Errorf("grok adapter is closed")
	}
	return nil
}
