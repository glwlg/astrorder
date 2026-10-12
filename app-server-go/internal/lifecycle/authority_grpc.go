package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/app-server/internal/daemonclient"
)

// GRPCAuthority implements Authority over the typed loopback gRPC transport.
type GRPCAuthority struct {
	client *daemonclient.Client
}

// NewGRPCAuthority dials the authoritative daemon and verifies capabilities.
func NewGRPCAuthority(ctx context.Context, target, credential string) (*GRPCAuthority, error) {
	if target == "" {
		return nil, errors.New("daemon target is required")
	}
	if credential == "" {
		return nil, errors.New("daemon credential is required")
	}
	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         target,
		Credential:     credential,
		RequestTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("dial daemon authority: %w", err)
	}
	return &GRPCAuthority{client: client}, nil
}

// Close releases client transport handles.
func (a *GRPCAuthority) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

// State queries authoritative session states, pending operations, and daemon health.
func (a *GRPCAuthority) State(ctx context.Context) (AuthorityState, error) {
	if a.client == nil {
		return AuthorityState{}, errors.New("daemon authority client is not connected")
	}
	resp, err := a.client.Daemon.GetStatus(ctx, &sessiondv1.GetStatusRequest{})
	if err != nil {
		return AuthorityState{
			Complete: false,
			State:    "unhealthy",
			Error:    err.Error(),
		}, fmt.Errorf("daemon GetStatus failed: %w", err)
	}

	sessions := make(map[string]string, len(resp.GetSessions()))
	activeSessions := make(map[string]string)
	for _, sess := range resp.GetSessions() {
		sid := ""
		if sess.GetSession() != nil {
			sid = sess.GetSession().GetSessionId()
		}
		if sid == "" {
			continue
		}
		statusStr := formatSessionStatus(sess.GetStatus())
		sessions[sid] = statusStr
		if statusStr == "running" || statusStr == "waiting_approval" {
			activeSessions[sid] = statusStr
		}
	}

	runtimes := make(map[string]any, len(resp.GetRuntimes()))
	for _, rt := range resp.GetRuntimes() {
		runtimes[rt.GetRuntime().String()] = map[string]any{
			"registered":     rt.GetRegistered(),
			"busy":           rt.GetBusy(),
			"owned_sessions": rt.GetOwnedSessions(),
		}
	}

	hasStorageErr := resp.GetStorageError() != nil
	hasModelCfgErr := resp.GetModelConfigError() != nil
	complete := !hasStorageErr && !hasModelCfgErr

	stateStr := "running"
	if hasStorageErr || hasModelCfgErr {
		stateStr = "unhealthy"
	}

	pendingOps := int(resp.GetPendingOperations())

	return AuthorityState{
		Complete:          complete,
		PendingKnown:      true,
		PendingOperations: pendingOps,
		Sessions:          sessions,
		ActiveSessions:    activeSessions,
		Runtimes:          runtimes,
		DaemonID:          resp.GetDaemonId(),
		State:             stateStr,
	}, nil
}

// Shutdown requests a typed, authenticated daemon shutdown.
func (a *GRPCAuthority) Shutdown(ctx context.Context, confirmActive bool) error {
	if a.client == nil {
		return errors.New("daemon authority client is not connected")
	}
	resp, err := a.client.Daemon.Shutdown(ctx, &sessiondv1.ShutdownRequest{
		ConfirmActive: confirmActive,
	})
	if err != nil {
		return fmt.Errorf("daemon shutdown rpc: %w", err)
	}
	if !resp.GetAccepted() {
		return errors.New("daemon refused graceful shutdown")
	}
	return nil
}

func formatSessionStatus(st sessiondv1.SessionStatus) string {
	switch st {
	case sessiondv1.SessionStatus_SESSION_STATUS_IDLE:
		return "idle"
	case sessiondv1.SessionStatus_SESSION_STATUS_RUNNING:
		return "running"
	case sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
		return "waiting_approval"
	case sessiondv1.SessionStatus_SESSION_STATUS_ERROR:
		return "error"
	default:
		return "unknown"
	}
}
