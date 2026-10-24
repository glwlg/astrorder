package connections

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// AutoConnect keeps desired connections reconciled while the App is alive,
// including when only the daemon restarts. It never restarts a runtime.
func (h *Handler) AutoConnect(ctx context.Context) {
	h.autoOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				if err := h.Restore(ctx); err != nil && ctx.Err() == nil {
					slog.Default().Debug("connections auto-connect waiting", "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

// Restore attaches missing control channels, using live daemon ownership rather
// than durable journal rows or stored UI state to decide what is connected.
func (h *Handler) Restore(ctx context.Context) error {
	h.restoreMu.Lock()
	defer h.restoreMu.Unlock()
	if h.client == nil || h.store == nil {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	snapshot, err := h.client.Daemon.GetStatus(probeCtx, &sessiondv1.GetStatusRequest{})
	cancel()
	if err != nil {
		return fmt.Errorf("read daemon ownership: %w", err)
	}
	if snapshot == nil || snapshot.GetDaemonId() != h.client.DaemonID() || snapshot.StorageError != nil {
		return fmt.Errorf("daemon ownership snapshot is unconfirmed")
	}
	rows, err := h.store.ListSSHConnections(ctx)
	if err != nil {
		return fmt.Errorf("list ssh connections: %w", err)
	}
	agents, err := h.store.ListCurrentAgents(ctx)
	if err != nil {
		return fmt.Errorf("list previously connected agents: %w", err)
	}
	type target struct {
		id  string
		row map[string]any
	}
	targets := []target{{id: "local"}}
	for _, row := range rows {
		if id, _ := row["id"].(string); id != "" {
			targets = append(targets, target{id: id, row: row})
		}
	}
	var workers sync.WaitGroup
	limit := make(chan struct{}, 6)
	for _, node := range targets {
		for _, kind := range environmentKinds {
			if kind == "antigravity" && node.id != "local" {
				continue
			}
			if ctx.Err() != nil {
				workers.Wait()
				return ctx.Err()
			}
			enabled, present, err := h.store.GetAgentConnectionChoice(ctx, node.id+":"+kind)
			if err != nil {
				workers.Wait()
				return err
			}
			if present && !enabled {
				h.forgetSessionRecovery(node.id, kind)
				continue
			}
			var connectionID *string
			if node.id != "local" {
				connectionID = &node.id
			}
			expectedConnection := ""
			if node.id != "local" {
				expectedConnection = node.id
			}
			runtimeKind := runtimeTarget(kind, connectionID, node.row).GetRuntime()
			if !present {
				// Preserve confirmed connections from before choices were recorded,
				// but never opt into every installed runtime or saved host.
				previouslyConnected := kind == "hermes" && node.row != nil && node.row["state"] == "connected"
				for _, agent := range agents {
					connection, _ := agent["connection_id"].(string)
					if connection == "" {
						connection = "local"
					}
					if connection == node.id && agent["kind"] == kind && agent["status"] == "ready" {
						previouslyConnected = true
					}
				}
				for _, session := range snapshot.Sessions {
					if session.GetSession().GetSessionId() == "probe-"+kind+"-"+node.id && session.GetSession().GetConnectionId() == expectedConnection && session.GetRuntime() == runtimeKind && session.GetRuntimeOwned() {
						previouslyConnected = true
					}
				}
				if !previouslyConnected {
					continue
				}
				if err := h.store.SetAgentConnectionChoice(ctx, node.id+":"+kind, true); err != nil {
					workers.Wait()
					return err
				}
			}
			if kind == "antigravity" {
				registered := false
				for _, runtime := range snapshot.Runtimes {
					if runtime.GetRuntime() == runtimeKind && runtime.GetRegistered() {
						registered = true
					}
				}
				if !registered {
					continue
				}
			}
			owned := false
			for _, session := range snapshot.Sessions {
				if session.GetSession().GetSessionId() == "probe-"+kind+"-"+node.id &&
					session.GetSession().GetConnectionId() == expectedConnection &&
					session.GetRuntime() == runtimeKind && session.GetRuntimeOwned() {
					owned = true
					break
				}
			}
			if owned {
				h.scheduleSessionRecovery(node.id, kind, false)
				continue
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				select {
				case limit <- struct{}{}:
					defer func() { <-limit }()
				case <-ctx.Done():
					return
				}
				attachCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				if err := h.attachEnvironmentAgent(attachCtx, node.id, kind, node.row); err != nil {
					slog.Default().Debug("auto-connect runtime not ready", "connection_id", node.id, "kind", kind, "error", err)
				} else {
					h.scheduleSessionRecovery(node.id, kind, true)
				}
			}()
		}
	}
	workers.Wait()
	return ctx.Err()
}

func (h *Handler) attachEnvironmentAgent(ctx context.Context, connID, kind string, sshConn map[string]any) error {
	if kind == "antigravity" && connID != "local" {
		return fmt.Errorf("Antigravity SSH runtime is not supported yet")
	}
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
