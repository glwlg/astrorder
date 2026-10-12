package grpcapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/events"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (service *service) AppendEvent(_ context.Context, request *sessiondv1.AppendEventRequest) (*sessiondv1.AppendEventResponse, error) {
	if request == nil || request.Session == nil || request.Session.SessionId == "" || request.EventType == "" || request.Payload == nil {
		return nil, status.Error(codes.InvalidArgument, "session event is invalid")
	}
	if len(request.Payload.Utf8Json) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "native JSON business payload exceeds 16000000 bytes")
	}
	mappedStatus, ok := sessionStatusName(request.Status)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "session event status is invalid")
	}
	if err := verifyPayloadHashes(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	payload, err := protocol.DecodeNativeJSON(request.Payload.Utf8Json)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	frame, err := service.daemon.AppendEvent(protocol.EventInput{
		SessionID:       request.Session.SessionId,
		Event:           request.EventType,
		Status:          mappedStatus,
		Payload:         payload,
		TimestampMicros: request.TimestampUnixMicros,
	})
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &sessiondv1.AppendEventResponse{
		DaemonId: service.daemon.DaemonID(),
		Session:  cloneSessionRef(request.Session),
		SeqId:    frame.SeqID,
	}, nil
}

func verifyPayloadHashes(request *sessiondv1.AppendEventRequest) error {
	digest := sha256.Sum256(request.Payload.Utf8Json)
	hexDigest := hex.EncodeToString(digest[:])
	for label, supplied := range map[string]*string{
		"payload_hash": request.PayloadHash,
		"sha256":       request.Payload.Sha256,
	} {
		if supplied != nil && *supplied != hexDigest {
			return fmt.Errorf("%s does not match native JSON bytes", label)
		}
	}
	return nil
}

func (service *service) Subscribe(request *sessiondv1.SubscribeRequest, stream grpc.ServerStreamingServer[sessiondv1.EventStreamFrame]) error {
	if request == nil {
		return status.Error(codes.InvalidArgument, "subscribe request is required")
	}
	pageSize := int(request.ReplayPageSize)
	if pageSize == 0 {
		pageSize = defaultReplayPageSize
	}
	if pageSize < 1 || pageSize > 1000 {
		return status.Error(codes.InvalidArgument, "replay page size must be between 1 and 1000")
	}
	cursors := make([]protocol.ReplayCursor, 0, len(request.Cursors))
	for _, cursor := range request.Cursors {
		if cursor == nil || cursor.Session == nil {
			return status.Error(codes.InvalidArgument, "replay cursor session is required")
		}
		cursors = append(cursors, protocol.ReplayCursor{SessionID: cursor.Session.SessionId, After: cursor.AfterSeqId})
	}
	plan, err := service.daemon.BeginReplay(request.ExpectedDaemonId, cursors, service.options.subscriptionQueueCapacity)
	if err != nil {
		if err.Error() == "daemon epoch changed" {
			current := plan.DaemonID
			return service.sendAndFinish(stream, "resync", &sessiondv1.EventStreamFrame{Frame: &sessiondv1.EventStreamFrame_ResyncRequired{ResyncRequired: &sessiondv1.ResyncRequired{
				Reason:          sessiondv1.ResyncReason_RESYNC_REASON_DAEMON_EPOCH_CHANGED,
				CurrentDaemonId: &current,
			}}})
		}
		return status.Error(classifyProtocolError(err), err.Error())
	}
	defer service.daemon.EndReplay(plan)

	for sessionID, after := range plan.Cursors {
		boundary, exists := plan.Sessions[sessionID]
		if !exists {
			return service.sendAndFinish(stream, "resync", resyncFrame(sessionID, after, 0, 0, sessiondv1.ResyncReason_RESYNC_REASON_UNKNOWN_SESSION, nil))
		}
		if after > boundary.MaxSeqID || (boundary.MinSeqID > 0 && after < boundary.MinSeqID-1) {
			return service.sendAndFinish(stream, "resync", resyncFrame(sessionID, after, boundary.MinSeqID, boundary.MaxSeqID, sessiondv1.ResyncReason_RESYNC_REASON_CURSOR_EXPIRED, nil))
		}
	}

	sessionIDs := sortedSessionIDs(plan.Sessions)
	lastEmitted := make(map[string]int64, len(plan.Sessions))
	for _, sessionID := range sessionIDs {
		lastEmitted[sessionID] = plan.Cursors[sessionID]
	}
	if request.IncludeSnapshot {
		snapshot := &sessiondv1.StateSnapshot{DaemonId: plan.DaemonID}
		for _, sessionID := range sessionIDs {
			boundary := plan.Sessions[sessionID]
			mappedStatus, ok := sessionStatus(boundary.Status)
			if !ok {
				return status.Error(codes.Unavailable, "authoritative session status is invalid")
			}
			snapshot.Sessions = append(snapshot.Sessions, &sessiondv1.SessionSnapshot{
				Session:            &sessiondv1.SessionRef{SessionId: sessionID},
				Status:             mappedStatus,
				MinSeqId:           boundary.MinSeqID,
				ReplayThroughSeqId: boundary.MaxSeqID,
			})
		}
		if err := service.send(stream, "snapshot", &sessiondv1.EventStreamFrame{Frame: &sessiondv1.EventStreamFrame_Snapshot{Snapshot: snapshot}}); err != nil {
			return err
		}
	}

	for _, sessionID := range sessionIDs {
		boundary := plan.Sessions[sessionID]
		after := plan.Cursors[sessionID]
		pageNumber := 0
		for after < boundary.MaxSeqID {
			pageNumber++
			if service.options.replayPageHook != nil {
				service.options.replayPageHook(sessionID, pageNumber)
			}
			page, pageErr := service.daemon.ReplayPage(sessionID, after, boundary.MaxSeqID, pageSize)
			if pageErr != nil {
				return status.Error(codes.Unavailable, pageErr.Error())
			}
			if page.MinSeq > 0 && after < page.MinSeq-1 {
				return service.sendAndFinish(stream, "resync", resyncFrame(sessionID, after, page.MinSeq, boundary.MaxSeqID, sessiondv1.ResyncReason_RESYNC_REASON_CURSOR_EXPIRED, nil))
			}
			if len(page.Frames) == 0 && page.HasMore {
				return status.Error(codes.Internal, "replay page made no progress")
			}
			for _, frame := range page.Frames {
				envelope, convertErr := service.eventEnvelope(plan.DaemonID, frame)
				if convertErr != nil {
					return status.Error(codes.Unavailable, convertErr.Error())
				}
				if err := service.send(stream, "replay", &sessiondv1.EventStreamFrame{Frame: &sessiondv1.EventStreamFrame_Replay{Replay: envelope}}); err != nil {
					return err
				}
				after = frame.SeqID
				lastEmitted[sessionID] = frame.SeqID
			}
			if !page.HasMore {
				break
			}
		}
		if !plan.Subscription.Healthy() {
			return service.sendOverflow(stream, lastEmitted)
		}
	}

	ready := &sessiondv1.LiveReady{DaemonId: plan.DaemonID}
	for _, sessionID := range sessionIDs {
		ready.Watermarks = append(ready.Watermarks, &sessiondv1.SessionWatermark{
			Session:            &sessiondv1.SessionRef{SessionId: sessionID},
			ReplayThroughSeqId: plan.Sessions[sessionID].MaxSeqID,
		})
	}
	if err := service.send(stream, "live-ready", &sessiondv1.EventStreamFrame{Frame: &sessiondv1.EventStreamFrame_LiveReady{LiveReady: ready}}); err != nil {
		return err
	}
	if !plan.Subscription.Healthy() {
		return service.sendOverflow(stream, lastEmitted)
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case frame := <-plan.Subscription.Frames():
			envelope, convertErr := service.eventEnvelope(plan.DaemonID, frame)
			if convertErr != nil {
				return status.Error(codes.Unavailable, convertErr.Error())
			}
			if err := service.send(stream, "live", &sessiondv1.EventStreamFrame{Frame: &sessiondv1.EventStreamFrame_Live{Live: envelope}}); err != nil {
				return err
			}
			lastEmitted[frame.SessionID] = frame.SeqID
		case <-ticker.C:
			if !plan.Subscription.Healthy() {
				return service.sendOverflow(stream, lastEmitted)
			}
		}
	}
}

func (service *service) eventEnvelope(daemonID string, frame events.Frame) (*sessiondv1.EventEnvelope, error) {
	mappedStatus := sessiondv1.SessionStatus_SESSION_STATUS_UNSPECIFIED
	if frame.Status != "" {
		var ok bool
		mappedStatus, ok = sessionStatus(frame.Status)
		if !ok {
			return nil, fmt.Errorf("stored event status is invalid")
		}
	}
	envelope := &sessiondv1.EventEnvelope{
		DaemonId:            daemonID,
		Session:             &sessiondv1.SessionRef{SessionId: frame.SessionID},
		SeqId:               frame.SeqID,
		TimestampUnixMicros: int64(math.Round(frame.Timestamp * 1e6)),
		EventType:           frame.Event,
		Status:              mappedStatus,
	}
	raw, err := json.Marshal(frame.Payload)
	if err != nil {
		return nil, fmt.Errorf("stored event payload is invalid")
	}
	envelope.NativePayload = &sessiondv1.NativeJson{Schema: "astrorder.event.payload.v1", Utf8Json: raw}
	switch frame.Event {
	case "runtime.owner":
		owner := &sessiondv1.RuntimeOwnerEvent{}
		if value, ok := exactInt64(frame.Payload["owner_pid"]); ok {
			owner.OwnerPid = &value
		}
		if value, ok := frame.Payload["runtime_id"].(string); ok {
			owner.RuntimeId = &value
		}
		envelope.Payload = &sessiondv1.EventEnvelope_RuntimeOwner{RuntimeOwner: owner}
	case "runtime.ownership_lost":
		lost := &sessiondv1.OwnershipLostEvent{}
		lost.Adopted, _ = frame.Payload["adopted"].(bool)
		lost.OrphanAlive, _ = frame.Payload["orphan_alive"].(bool)
		lost.Reason, _ = frame.Payload["reason"].(string)
		if previous, ok := frame.Payload["previous_status"].(string); ok {
			lost.PreviousStatus, _ = sessionStatus(previous)
		}
		envelope.Payload = &sessiondv1.EventEnvelope_OwnershipLost{OwnershipLost: lost}
	case "pty.output":
		data, _ := frame.Payload["data"].(string)
		chunk, _ := exactInt64(frame.Payload["chunk_seq"])
		envelope.Payload = &sessiondv1.EventEnvelope_PtyOutput{PtyOutput: &sessiondv1.PtyOutputEvent{Data: []byte(data), ChunkSeq: chunk}}
	default:
		envelope.Payload = &sessiondv1.EventEnvelope_Native{Native: &sessiondv1.NativeJson{Schema: "astrorder.event.payload.v1", Utf8Json: raw}}
	}
	return envelope, nil
}

func exactInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	case int64:
		return number, true
	case int:
		return int64(number), true
	case float64:
		if math.Trunc(number) == number && number >= math.MinInt64 && number <= math.MaxInt64 {
			return int64(number), true
		}
	}
	return 0, false
}

func withSendTimeout(timeout time.Duration, send func() error) error {
	started := time.Now()
	if err := send(); err != nil {
		return err
	}
	if time.Since(started) > timeout {
		return status.Error(codes.DeadlineExceeded, "event stream send timed out")
	}
	return nil
}

func (service *service) runSendHook(kind string) error {
	if service.options.sendHook == nil {
		return nil
	}
	return withSendTimeout(service.options.sendTimeout, func() error {
		service.options.sendHook(kind)
		return nil
	})
}

func (service *service) send(stream grpc.ServerStreamingServer[sessiondv1.EventStreamFrame], kind string, frame *sessiondv1.EventStreamFrame) error {
	if err := service.runSendHook(kind); err != nil {
		return err
	}
	// At most one send is in flight per subscription. On timeout the handler
	// returns, gRPC cancels its transport context, and the blocked sender exits.
	// Waiting for Send here would prevent that cancellation from ever happening.
	done := make(chan error, 1)
	go func() { done <- stream.Send(frame) }()
	timer := time.NewTimer(service.options.sendTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return err
		}
		return stream.Context().Err()
	case <-stream.Context().Done():
		return stream.Context().Err()
	case <-timer.C:
		return status.Error(codes.DeadlineExceeded, "event stream send timed out")
	}
}

func (service *service) sendAndFinish(stream grpc.ServerStreamingServer[sessiondv1.EventStreamFrame], kind string, frame *sessiondv1.EventStreamFrame) error {
	return service.send(stream, kind, frame)
}

func (service *service) sendOverflow(stream grpc.ServerStreamingServer[sessiondv1.EventStreamFrame], lastEmitted map[string]int64) error {
	ids := make([]string, 0, len(lastEmitted))
	for id := range lastEmitted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	overflow := &sessiondv1.StreamOverflow{QueueCapacity: uint32(service.options.subscriptionQueueCapacity), Reason: "subscriber queue overflow; resume from durable cursors"}
	for _, id := range ids {
		overflow.LastEmitted = append(overflow.LastEmitted, &sessiondv1.SessionCursor{Session: &sessiondv1.SessionRef{SessionId: id}, AfterSeqId: lastEmitted[id]})
	}
	return service.sendAndFinish(stream, "overflow", &sessiondv1.EventStreamFrame{Frame: &sessiondv1.EventStreamFrame_Overflow{Overflow: overflow}})
}

func resyncFrame(sessionID string, after, minimum, through int64, reason sessiondv1.ResyncReason, current *string) *sessiondv1.EventStreamFrame {
	return &sessiondv1.EventStreamFrame{Frame: &sessiondv1.EventStreamFrame_ResyncRequired{ResyncRequired: &sessiondv1.ResyncRequired{
		Session:               &sessiondv1.SessionRef{SessionId: sessionID},
		RequestedAfterSeqId:   after,
		MinRetainedSeqId:      minimum,
		CommittedThroughSeqId: through,
		Reason:                reason,
		CurrentDaemonId:       current,
	}}}
}

func cloneSessionRef(reference *sessiondv1.SessionRef) *sessiondv1.SessionRef {
	if reference == nil {
		return nil
	}
	clone := &sessiondv1.SessionRef{SessionId: reference.SessionId}
	if reference.AgentId != nil {
		value := *reference.AgentId
		clone.AgentId = &value
	}
	if reference.ConnectionId != nil {
		value := *reference.ConnectionId
		clone.ConnectionId = &value
	}
	return clone
}
