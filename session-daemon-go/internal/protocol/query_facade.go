package protocol

import (
	"context"
	"fmt"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

// QueryNative uses the same registry and shared control admission as legacy
// runtime.request. A read does not reserve an operation or bind a session.
func (d *Daemon) QueryNative(ctx context.Context, request core.Request) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := d.beginControl(); err != nil {
		return nil, fmt.Errorf("%w: %v", core.ErrRuntimeBusy, err)
	}
	defer d.endControl()
	return d.queryNativeAdmitted(ctx, request)
}

func (d *Daemon) queryNativeAdmitted(parent context.Context, request core.Request) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	defer d.wakeModelReloads()
	return d.registry.Query(ctx, request)
}
