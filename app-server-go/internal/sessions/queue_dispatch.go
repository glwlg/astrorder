package sessions

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// RunQueue owns dispatch independently of browser connections. The durable
// command state is the dispatch boundary; received/unknown work is never replayed.
func (h *Handler) RunQueue(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := h.DispatchQueue(attempt)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("queue dispatch deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// DispatchQueue attempts at most one FIFO command per idle, live-owned session.
func (h *Handler) DispatchQueue(ctx context.Context) error {
	if h.client == nil || h.client.Daemon == nil {
		return nil
	}
	commands, err := h.store.ActiveCommands(ctx)
	if err != nil {
		return err
	}
	hasQueue := false
	for _, command := range commands {
		if command["action"] == "enqueue" && command["state"] == "queued" {
			hasQueue = true
			break
		}
	}
	if !hasQueue {
		return nil
	}
	capabilities, capabilityErr := h.client.Daemon.GetCapabilities(ctx, &pb.GetCapabilitiesRequest{})
	if capabilityErr != nil {
		return capabilityErr
	}
	if capabilities == nil || capabilities.GetProtocolMinor() < 1 {
		return nil
	}
	live, err := h.client.Daemon.GetStatus(ctx, &pb.GetStatusRequest{})
	if err != nil {
		return err
	}
	if live == nil || live.DaemonId != h.client.DaemonID() || live.StorageError != nil {
		return fmt.Errorf("queue requires a healthy current daemon")
	}
	seen := map[string]bool{}
	for _, command := range commands {
		if command["state"] != "queued" || command["action"] != "enqueue" {
			continue
		}
		aid, sid := stringValue(command, "agent_id"), stringValue(command, "session_id")
		key := aid + "\x00" + sid
		if seen[key] {
			continue
		}
		seen[key] = true
		// Unknown outcomes also block the session, even though ActiveCommands omits them.
		rows, err := h.store.ListCommands(ctx, aid, sid)
		if err != nil {
			return err
		}
		blocked := false
		for _, row := range rows {
			switch row["state"] {
			case "received":
				// A claimed request may still be crossing the dispatch boundary.
				blocked = true
			case "accepted":
				// Control receipts acknowledge an operation, not a chat turn.
				if row["action"] == "send" || row["action"] == "enqueue" {
					blocked = true
				}
			case "running", "unknown":
				blocked = true
			}
		}
		if blocked {
			continue
		}
		agent, err := h.store.GetAgent(ctx, aid)
		if err != nil {
			return err
		}
		if agent == nil || agent["status"] != "ready" {
			continue
		}
		session, err := h.store.GetSession(ctx, aid, sid)
		if err != nil {
			return err
		}
		if session == nil || session["control_state"] != "owned" {
			continue
		}
		nativeID := stringValue(session, "source_session_id")
		if nativeID == "" {
			nativeID = sid
		}
		idle := false
		for _, snapshot := range live.Sessions {
			if snapshotMatchesOwnedSession(snapshot, agent, aid, nativeID, stringValue(session, "connection_id")) && snapshot.Status == pb.SessionStatus_SESSION_STATUS_IDLE {
				idle = true
				break
			}
		}
		if !idle {
			continue
		}
		claimed, ok, err := h.store.ClaimQueuedCommand(ctx, aid, sid, stringValue(command, "id"))
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err = h.publisher.Publish(ctx, "command.upsert", claimed); err != nil {
			return err
		}
		// submitStored persists definitive failure/uncertainty. Never return work to
		// queued after crossing this durable boundary, including on shutdown.
		claimed["idle_only"] = true
		if _, err = h.submitStored(ctx, claimed); err != nil {
			slog.Warn("queued command outcome", "agent_id", aid, "session_id", sid, "command_id", command["id"], "error", err)
		}
	}
	return nil
}
