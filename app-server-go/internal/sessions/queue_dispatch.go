package sessions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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
	if err := h.reconcileHermesAttachmentRejections(ctx); err != nil {
		return err
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
	seen := map[queueScope]bool{}
	sem := make(chan struct{}, 8)
	errs := make(chan error, len(commands))
	var workers sync.WaitGroup
	for _, command := range commands {
		if command["state"] != "queued" || command["action"] != "enqueue" {
			continue
		}
		aid, sid := stringValue(command, "agent_id"), stringValue(command, "session_id")
		key := queueScope{agentID: aid, sessionID: sid}
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, busy := h.queueBusy.LoadOrStore(key, struct{}{}); busy {
			continue
		}
		workers.Add(1)
		go func(key queueScope, command map[string]any) {
			defer workers.Done()
			defer h.queueBusy.Delete(key)
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
			if err := h.dispatchQueuedSession(ctx, live, command); err != nil {
				errs <- err
			}
		}(key, command)
	}
	workers.Wait()
	close(errs)
	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (h *Handler) dispatchQueuedSession(ctx context.Context, live *pb.GetStatusResponse, command map[string]any) error {
	aid, sid, cid := stringValue(command, "agent_id"), stringValue(command, "session_id"), stringValue(command, "id")
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil || agent == nil || agent["status"] != "ready" {
		return err
	}
	session, err := h.store.GetSession(ctx, aid, sid)
	if err != nil || session == nil || session["control_state"] != "owned" {
		return err
	}
	nativeID := stringValue(session, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	var snapshot *pb.SessionSnapshot
	for _, candidate := range live.Sessions {
		if snapshotMatchesOwnedSession(candidate, agent, aid, nativeID, stringValue(session, "connection_id")) && candidate.Status == pb.SessionStatus_SESSION_STATUS_IDLE {
			snapshot = candidate
			break
		}
	}
	if snapshot == nil {
		return nil
	}
	// Unknown outcomes also block the session, even though ActiveCommands omits them.
	rows, err := h.store.ListCommands(ctx, aid, sid)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row["state"] == "unknown" {
			if err := h.reconcileUnknownQueueReceipt(ctx, agent, session, row); err != nil {
				return err
			}
		}
	}
	rows, err = h.store.ListCommands(ctx, aid, sid)
	if err != nil {
		return err
	}
	for _, row := range rows {
		switch row["state"] {
		case "received", "running", "unknown":
			return nil
		case "accepted":
			if row["action"] == "send" || row["action"] == "enqueue" {
				return nil
			}
		}
	}
	approvals, err := h.store.PendingApprovals(ctx)
	if err != nil {
		return err
	}
	for _, approval := range approvals {
		if approval["agent_id"] == aid && approval["session_id"] == sid {
			return h.queueNotice(ctx, aid, sid, cid, "等待处理会话中的授权或提问")
		}
	}
	reply, err := h.latestQueueReply(ctx, agent, session)
	if err != nil {
		return h.queueNotice(ctx, aid, sid, cid, err.Error())
	}
	ready, err := h.queueIdleSnapshot(ctx, agent, session, nativeID)
	if err != nil || ready == nil {
		return err
	}
	if reply.id != "" {
		if stringValue(command, "error") == "" {
			if err := h.queueNotice(ctx, aid, sid, cid, "正在判断上一轮回复是否需要确认"); err != nil {
				return err
			}
		}
		required, decisionErr := h.queueNeedsConfirmation(ctx, aid, sid, reply)
		if decisionErr != nil {
			return h.queueNotice(ctx, aid, sid, cid, decisionErr.Error())
		}
		if required {
			return h.queueNotice(ctx, aid, sid, cid, "上一轮回复需要你的确认，待发消息已保留")
		}
	}
	// The classifier can take seconds. Re-read the native conversation and
	// ownership before claiming the exact queued command. History reads can
	// themselves emit unrelated native events, so the reply is the boundary.
	current, err := h.queueIdleSnapshot(ctx, agent, session, nativeID)
	if err != nil || current == nil {
		return err
	}
	currentReply, err := h.latestQueueReply(ctx, agent, session)
	if err != nil {
		return h.queueNotice(ctx, aid, sid, cid, err.Error())
	}
	if reply != currentReply {
		return nil
	}
	claimed, ok, err := h.store.ClaimAutomaticQueuedCommand(ctx, aid, sid, cid, stringValue(session, "source_session_id"), stringValue(session, "connection_id"), stringValue(agent, "kind"))
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	h.queueDecisions.Delete(queueScope{agentID: aid, sessionID: sid})
	if err = h.publisher.Publish(ctx, "command.upsert", claimed); err != nil {
		return err
	}
	// submitStored persists definitive failure/uncertainty. Never return work to
	// queued after crossing this durable boundary, including on shutdown.
	claimed["idle_only"] = true
	if _, err = h.submitStored(ctx, claimed); err != nil {
		slog.Warn("queued command outcome", "agent_id", aid, "session_id", sid, "command_id", cid, "error", err)
	}
	return nil
}

func (h *Handler) queueIdleSnapshot(ctx context.Context, agent, session map[string]any, nativeID string) (*pb.SessionSnapshot, error) {
	live, err := h.client.Daemon.GetStatus(ctx, &pb.GetStatusRequest{})
	if err != nil {
		return nil, err
	}
	if live == nil || live.DaemonId != h.client.DaemonID() || live.StorageError != nil {
		return nil, fmt.Errorf("queue requires a healthy current daemon")
	}
	aid := stringValue(session, "agent_id")
	for _, snapshot := range live.Sessions {
		if snapshotMatchesOwnedSession(snapshot, agent, aid, nativeID, stringValue(session, "connection_id")) && snapshot.Status == pb.SessionStatus_SESSION_STATUS_IDLE {
			return snapshot, nil
		}
	}
	return nil, nil
}
