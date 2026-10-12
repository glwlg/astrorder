package connections

import (
	"context"
	"fmt"
	"log/slog"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// AutoConnect restores agent connections asynchronously without blocking server startup.
func (h *Handler) AutoConnect(ctx context.Context) {
	go func() {
		if err := h.Restore(ctx); err != nil {
			slog.Default().Warn("connections auto-connect finished with error", "error", err)
		}
	}()
}

// Restore synchronously restores active local and remote agent connections.
func (h *Handler) Restore(ctx context.Context) error {
	if h.client == nil || h.store == nil {
		return nil
	}

	// 1. 本机 Codex 与 Hermes 自动连接
	for _, localKind := range []string{"codex", "hermes"} {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		enabled, present, err := h.store.GetAgentConnectionChoice(ctx, "local:"+localKind)
		if err != nil {
			continue
		}
		if !present || enabled {
			if err := h.attachEnvironmentAgent(ctx, "local", localKind, nil); err != nil {
				slog.Default().Debug("auto-connect local runtime not ready", "kind", localKind, "error", err)
			}
		}
	}

	// 2. 远端已启用环境恢复
	rows, err := h.store.ListSSHConnections(ctx)
	if err != nil {
		return fmt.Errorf("list ssh connections: %w", err)
	}

	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		connID, _ := row["id"].(string)
		if connID == "" {
			continue
		}
		for _, kind := range environmentKinds {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			choiceKey := connID + ":" + kind
			enabled, present, err := h.store.GetAgentConnectionChoice(ctx, choiceKey)
			if err != nil {
				continue
			}
			shouldRestore := false
			if present && enabled {
				shouldRestore = true
			} else if !present && kind == "hermes" && row["state"] == "connected" {
				shouldRestore = true
			}
			if shouldRestore {
				if err := h.attachEnvironmentAgent(ctx, connID, kind, row); err != nil {
					slog.Default().Warn("failed to restore remote agent connection", "connection_id", connID, "kind", kind, "error", err)
				}
			}
		}
	}

	return nil
}

func (h *Handler) attachEnvironmentAgent(ctx context.Context, connID, kind string, sshConn map[string]any) error {
	if h.client == nil {
		return fmt.Errorf("daemon client unavailable")
	}

	var connPtr *string
	if connID != "local" {
		connPtr = &connID
		if sshConn == nil {
			var err error
			sshConn, err = h.store.GetSSHConnection(ctx, connID)
			if err != nil {
				return fmt.Errorf("get ssh connection: %w", err)
			}
			if sshConn == nil {
				return fmt.Errorf("connection %s not found", connID)
			}
		}
		settings, _ := sshConn["settings"].(map[string]any)
		if settings == nil {
			settings = sshConn
		}
		if _, err := validateSSHSettings(settings); err != nil {
			return fmt.Errorf("validate ssh settings: %w", err)
		}

		if kind == "codex" || kind == "grok" {
			resolver := h.executables
			if resolver == nil {
				resolver = &NativeRemoteExecutableResolver{SSHBinary: h.sshBinary}
			}
			executable, err := resolver.Resolve(ctx, kind, sshConn)
			if err != nil {
				return fmt.Errorf("resolve remote %s executable: %w", kind, err)
			}
			sshConn = withResolvedExecutable(sshConn, kind, executable)
		}
	}

	target := runtimeTarget(kind, connPtr, sshConn)
	expected := &sessiondv1.SessionRef{
		SessionId:    fmt.Sprintf("probe-%s-%s", kind, connID),
		ConnectionId: connPtr,
	}

	receipt, err := h.client.Session.AttachSession(ctx, &sessiondv1.AttachSessionRequest{
		Session:        expected,
		Target:         target,
		RuntimeControl: true,
	})
	if err != nil {
		return fmt.Errorf("attach session: %w", err)
	}
	if err := h.attachReceipt(receipt, expected); err != nil {
		return fmt.Errorf("attach receipt: %w", err)
	}

	if connID != "local" && kind == "hermes" {
		state, detail, event := "connected", "Connected successfully", "ssh_connection.connected"
		updated, err := h.store.UpdateSSHConnectionState(ctx, connID, state, detail, nil)
		if err == nil && updated != nil && h.publisher != nil {
			_ = h.publisher.Publish(ctx, event, updated)
		}
	}
	return nil
}
