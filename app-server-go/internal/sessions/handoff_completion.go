package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
)

// Idle state, process EOF, and operation acceptance do not establish successful
// model completion. Replay the exact fork's native completion notification.
func (h *Handler) awaitHandoffCompletion(ctx context.Context, ref *pb.SessionRef, receipt *pb.CommandReceipt, commandID string) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := h.client.Events.Subscribe(streamCtx, &pb.SubscribeRequest{ExpectedDaemonId: optString(h.client.DaemonID()), Cursors: []*pb.SessionCursor{{Session: ref}}, ReplayPageSize: 200})
	if err != nil {
		return commandFailure(503, "Native summary completion stream unavailable")
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return commandFailure(409, "Handoff cancelled or timed out")
			}
			return commandFailure(503, "Native summary completion stream failed")
		}
		if frame.GetOverflow() != nil || frame.GetResyncRequired() != nil {
			return commandFailure(503, "Native summary completion requires reconciliation")
		}
		event := frame.GetLive()
		if event == nil {
			event = frame.GetReplay()
		}
		if event == nil {
			continue
		}
		if event.DaemonId != h.client.DaemonID() || event.Session == nil || event.Session.GetAgentId() != ref.GetAgentId() || event.Session.SessionId != ref.SessionId || event.Session.GetConnectionId() != ref.GetConnectionId() {
			continue
		}
		if event.EventType == "runtime.ownership_lost" {
			return commandFailure(503, "Summary runtime lost ownership")
		}
		var payload map[string]any
		native := event.NativePayload
		if native == nil {
			native = event.GetNative()
		}
		if parseNativeJSON(native, &payload) != nil {
			continue
		}
		if event.EventType == "hermes.command_complete" && stringValue(payload, "command_id") == commandID {
			if payload["status"] != "complete" || payload["partial"] == true || payload["error"] != nil && payload["error"] != "" {
				return commandFailure(502, "Native summary generation failed")
			}
			return nil
		}
		if event.EventType == "codex.notification" {
			if nested, ok := payload["frame"].(map[string]any); ok {
				payload = nested
			}
			params, _ := payload["params"].(map[string]any)
			turn, _ := params["turn"].(map[string]any)
			if payload["method"] == "turn/completed" && receipt.GetTurnId() != "" && stringValue(turn, "id") == receipt.GetTurnId() {
				if turn["status"] != "completed" || turn["error"] != nil {
					return commandFailure(502, "Native summary turn failed or was interrupted")
				}
				return nil
			}
		}
	}
}
