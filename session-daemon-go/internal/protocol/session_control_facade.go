package protocol

import (
	"context"
	"fmt"

	core "astrorder.dev/session-daemon/internal/runtime"
)

// ExecuteSessionControl uses the existing control path, admission, journal and
// native adapter. Transport validation must finish before this entry point.
func (d *Daemon) ExecuteSessionControl(ctx context.Context, request map[string]any) map[string]any {
	if err := ctx.Err(); err != nil {
		return errorResponse(request["request_id"], err.Error())
	}
	return d.control(ctx, request)
}

func (d *Daemon) ResolveSessionOwner(id string, expected core.SessionReadIdentity) (string, error) {
	return d.registry.ResolveSessionOwner(id, expected)
}

func (d *Daemon) SessionIdentity(id string) (core.SessionReadIdentity, error) {
	return d.registry.SessionIdentity(id)
}

// ObserveSession only promotes positive native activity. Absence from an
// observer is never sufficient evidence for ending a task or approval.
func (d *Daemon) ObserveSession(id string, expected core.SessionReadIdentity, activity string, observer string, observedAt int64) (core.Result, error) {
	if activity != "running" && activity != "waiting_approval" || observer == "" || observedAt <= 0 {
		return core.Result{}, fmt.Errorf("invalid activity observation")
	}
	if err := d.beginControl(); err != nil {
		return core.Result{}, err
	}
	defer d.endControl()
	result, err := d.registry.ObserveSession(id, expected, activity)
	if err != nil {
		return core.Result{}, err
	}
	_, err = d.journal.Commit(id, "session.status", map[string]any{"status": result.Status, "observer": observer, "observed_at_unix_micros": observedAt}, result.Status)
	return result, err
}
