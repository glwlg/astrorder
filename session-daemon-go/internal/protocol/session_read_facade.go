package protocol

import (
	"context"
	"fmt"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

// ReadSession performs only closed read actions through the same daemon and
// native runtime authority. Unlike a legacy control reply it does not track a
// new durable status or reserve an operation.
func (d *Daemon) ReadSession(parent context.Context, request core.Request, identity core.SessionReadIdentity) (core.Result, string, error) {
	if err := parent.Err(); err != nil {
		return core.Result{}, "", err
	}
	if err := d.beginControl(); err != nil {
		return core.Result{}, "", fmt.Errorf("%w: %v", core.ErrRuntimeBusy, err)
	}
	defer d.endControl()
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	defer d.wakeModelReloads()
	return d.registry.ReadSession(ctx, request, identity)
}
