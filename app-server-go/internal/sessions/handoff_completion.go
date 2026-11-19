package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"sort"
)

// Idle state, process EOF, and operation acceptance do not establish successful
// model completion. Replay the exact fork's native completion notification.
func (h *Handler) awaitHandoffCompletion(ctx context.Context, ref *pb.SessionRef, receipt *pb.CommandReceipt, commandID string) error {
	afterBySession := map[string]int64{ref.SessionId: 0}
	refreshOtherCursors := func() {
		if h.client == nil || h.client.Daemon == nil {
			return
		}
		st, err := h.client.Daemon.GetStatus(ctx, &pb.GetStatusRequest{})
		if err != nil || st == nil || st.GetDaemonId() != h.client.DaemonID() {
			return
		}
		for _, snap := range st.GetSessions() {
			if snap == nil || snap.GetSession() == nil {
				continue
			}
			sid := snap.GetSession().GetSessionId()
			if sid == "" {
				continue
			}
			if sid == ref.SessionId {
				if afterBySession[sid] == 0 && snap.GetMinSeqId() > 1 {
					afterBySession[sid] = snap.GetMinSeqId() - 1
				}
				continue
			}
			if through := snap.GetReplayThroughSeqId(); through > afterBySession[sid] {
				afterBySession[sid] = through
			}
		}
	}
	refreshOtherCursors()

	buildCursors := func() []*pb.SessionCursor {
		cursors := []*pb.SessionCursor{{
			Session:    ref,
			AfterSeqId: afterBySession[ref.SessionId],
		}}
		others := make([]string, 0, len(afterBySession))
		for sid := range afterBySession {
			if sid != ref.SessionId {
				others = append(others, sid)
			}
		}
		sort.Strings(others)
		for _, sid := range others {
			cursors = append(cursors, &pb.SessionCursor{
				Session:    &pb.SessionRef{SessionId: sid},
				AfterSeqId: afterBySession[sid],
			})
		}
		return cursors
	}

	const maxSubscribeAttempts = 64
	for attempt := 0; attempt < maxSubscribeAttempts; attempt++ {
		if ctx.Err() != nil {
			return commandFailure(409, "Handoff cancelled or timed out")
		}
		resubscribe, err := func() (bool, error) {
			streamCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			stream, err := h.client.Events.Subscribe(streamCtx, &pb.SubscribeRequest{
				ExpectedDaemonId: optString(h.client.DaemonID()),
				Cursors:          buildCursors(),
				ReplayPageSize:   200,
			})
			if err != nil {
				return false, commandFailure(503, "Native summary completion stream unavailable")
			}
			for {
				frame, err := stream.Recv()
				if err != nil {
					if ctx.Err() != nil {
						return false, commandFailure(409, "Handoff cancelled or timed out")
					}
					return false, commandFailure(503, "Native summary completion stream failed")
				}
				if ov := frame.GetOverflow(); ov != nil {
					for _, cur := range ov.GetLastEmitted() {
						if cur == nil || cur.GetSession() == nil {
							continue
						}
						sid := cur.GetSession().GetSessionId()
						if sid != "" && cur.GetAfterSeqId() > afterBySession[sid] {
							afterBySession[sid] = cur.GetAfterSeqId()
						}
					}
					cancel()
					refreshOtherCursors()
					return true, nil
				}
				if resync := frame.GetResyncRequired(); resync != nil {
					sid := resync.GetSession().GetSessionId()
					if sid != "" && sid != ref.SessionId {
						switch resync.GetReason() {
						case pb.ResyncReason_RESYNC_REASON_CURSOR_EXPIRED:
							if resync.GetMinRetainedSeqId() > 0 && afterBySession[sid] < resync.GetMinRetainedSeqId()-1 {
								afterBySession[sid] = resync.GetMinRetainedSeqId() - 1
							} else {
								afterBySession[sid] = resync.GetCommittedThroughSeqId()
							}
							cancel()
							return true, nil
						case pb.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION:
							delete(afterBySession, sid)
							cancel()
							return true, nil
						}
					}
					return false, commandFailure(503, "Native summary completion requires reconciliation")
				}
				event := frame.GetLive()
				if event == nil {
					event = frame.GetReplay()
				}
				if event == nil {
					continue
				}
				if event.Session != nil && event.Session.SessionId != "" && event.GetSeqId() > afterBySession[event.Session.SessionId] {
					afterBySession[event.Session.SessionId] = event.GetSeqId()
				}
				if event.DaemonId != h.client.DaemonID() || event.Session == nil || event.Session.SessionId != ref.SessionId {
					continue
				}
				if event.Session.AgentId != nil && ref.GetAgentId() != "" && event.Session.GetAgentId() != ref.GetAgentId() {
					continue
				}
				if event.Session.ConnectionId != nil && normalizeConnectionID(event.Session.GetConnectionId()) != normalizeConnectionID(ref.GetConnectionId()) {
					continue
				}
				if event.EventType == "runtime.ownership_lost" {
					return false, commandFailure(503, "Summary runtime lost ownership")
				}
				var payload map[string]any
				native := event.NativePayload
				if native == nil {
					native = event.GetNative()
				}
				if parseNativeJSON(native, &payload) != nil {
					continue
				}
				if payloadAgent := stringValue(payload, "agent_id"); payloadAgent != "" && ref.GetAgentId() != "" && payloadAgent != ref.GetAgentId() {
					continue
				}
				if _, hasConn := payload["connection_id"]; hasConn && normalizeConnectionID(stringValue(payload, "connection_id")) != normalizeConnectionID(ref.GetConnectionId()) {
					continue
				}
				if event.EventType == "hermes.command_complete" && stringValue(payload, "command_id") == commandID {
					if payload["status"] != "complete" || payload["partial"] == true || payload["error"] != nil && payload["error"] != "" {
						return false, commandFailure(502, "Native summary generation failed")
					}
					return false, nil
				}
				if event.EventType == "connector.event" || event.EventType == "command.upsert" {
					data := payload
					if payload["type"] == "command.upsert" {
						if nested, ok := payload["data"].(map[string]any); ok {
							data = nested
						}
					}
					if (event.EventType == "command.upsert" || payload["type"] == "command.upsert") && stringValue(data, "id") == commandID {
						state := stringValue(data, "state")
						if state == "completed" && (data["error"] == nil || data["error"] == "") {
							return false, nil
						}
						if state == "failed" || state == "unknown" || data["error"] != nil && data["error"] != "" {
							return false, commandFailure(502, "Native summary generation failed")
						}
					}
				}
				if event.EventType == "codex.notification" {
					if nested, ok := payload["frame"].(map[string]any); ok {
						payload = nested
					}
					params, _ := payload["params"].(map[string]any)
					turn, _ := params["turn"].(map[string]any)
					if payload["method"] == "turn/completed" && receipt.GetTurnId() != "" && stringValue(turn, "id") == receipt.GetTurnId() {
						if turn["status"] != "completed" || turn["error"] != nil {
							return false, commandFailure(502, "Native summary turn failed or was interrupted")
						}
						return false, nil
					}
				}
			}
		}()
		if err != nil {
			return err
		}
		if !resubscribe {
			return nil
		}
	}
	return commandFailure(503, "Native summary completion exceeded reconnect limit")
}
