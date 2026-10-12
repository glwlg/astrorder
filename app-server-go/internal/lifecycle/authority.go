package lifecycle

import (
	"context"
	"errors"
	"fmt"
)

// Authority is the typed daemon-control boundary supplied by the gRPC owner.
// Lifecycle code deliberately has no legacy WebSocket implementation.
type Authority interface {
	State(context.Context) (AuthorityState, error)
	Shutdown(context.Context, bool) error
}

// AuthorityState is complete only when every owned session and durable pending
// operation was returned by the daemon's authoritative typed API.
type AuthorityState struct {
	Complete          bool              `json:"complete"`
	PendingKnown      bool              `json:"pending_known"`
	PendingOperations int               `json:"pending_operations"`
	Sessions          map[string]string `json:"sessions,omitempty"`
	ActiveSessions    map[string]string `json:"active_sessions,omitempty"`
	Runtimes          map[string]any    `json:"runtimes,omitempty"`
	StartupTask       string            `json:"startup_task,omitempty"`
	DaemonID          string            `json:"daemon_id,omitempty"`
	State             string            `json:"state,omitempty"`
	Error             string            `json:"error,omitempty"`
}

// StopDaemon requests a typed, authenticated daemon shutdown. It never kills a
// daemon process. Without explicit confirmation, only complete authoritative
// idle state is safe.
func StopDaemon(ctx context.Context, authority Authority, confirmActive bool) error {
	if authority == nil {
		return errors.New("daemon authority is required")
	}
	state, stateErr := authority.State(ctx)
	if !confirmActive {
		if stateErr != nil {
			return fmt.Errorf("refusing unconfirmed daemon stop: authoritative state unavailable: %w", stateErr)
		}
		if err := state.safeForUnconfirmedStop(); err != nil {
			return err
		}
	}
	if err := authority.Shutdown(ctx, confirmActive); err != nil {
		return fmt.Errorf("daemon authority refused shutdown: %w", err)
	}
	return nil
}

func (state AuthorityState) safeForUnconfirmedStop() error {
	if !state.Complete || !state.PendingKnown || state.PendingOperations < 0 {
		return errors.New("refusing unconfirmed daemon stop: authoritative state is incomplete")
	}
	if state.PendingOperations != 0 {
		return errors.New("refusing unconfirmed daemon stop: pending operations exist")
	}
	for _, status := range state.Sessions {
		if status != "idle" {
			return fmt.Errorf("refusing unconfirmed daemon stop: session state %q is not confirmed idle", status)
		}
	}
	return nil
}
