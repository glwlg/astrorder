package protocol

import (
	"context"
	"fmt"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func (d *Daemon) SwitchAgyAccount(ctx context.Context, request core.Request, connectionID string, force bool) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := d.beginControl(); err != nil {
		return nil, fmt.Errorf("%w: %v", core.ErrRuntimeBusy, err)
	}
	defer d.endControl()
	defer d.wakeModelReloads()
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	result, released, err := d.registry.SwitchAgyAccount(ctx, request, connectionID, force)
	for _, id := range released {
		_ = d.journal.TrackSession(id, "idle")
	}
	return result, err
}
