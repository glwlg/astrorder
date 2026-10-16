package sessions

import "context"

const hermesAttachmentGuidanceRejection = "attachments must be queued for the next Hermes turn"

// Older daemons marked every adapter error uncertain. This exact local guard
// returns before any native RPC, so its persisted rejection proves non-delivery.
// Do not infer non-delivery from session completion or arbitrary error substrings.
func (h *Handler) reconcileHermesAttachmentRejections(ctx context.Context) error {
	rows, err := h.store.QueueCommands(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row["action"] != "enqueue" || row["state"] != "unknown" || row["error"] != "Native result is unconfirmed: native command outcome is uncertain: "+hermesAttachmentGuidanceRejection {
			continue
		}
		aid, sid, cid := stringValue(row, "agent_id"), stringValue(row, "session_id"), stringValue(row, "id")
		agent, err := h.store.GetAgent(ctx, aid)
		if err != nil {
			return err
		}
		if agent == nil || agent["kind"] != "hermes" {
			continue
		}
		updated, changed, err := h.store.ResolveRejectedQueueCommand(ctx, aid, sid, cid, stringValue(row, "error"), hermesAttachmentGuidanceRejection)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if err := h.publisher.Publish(ctx, "command.upsert", updated); err != nil {
			return err
		}
	}
	return nil
}
