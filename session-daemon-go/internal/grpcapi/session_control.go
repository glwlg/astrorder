package grpcapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type sessionControlService struct {
	pb.UnimplementedSessionControlServer
	daemon *protocol.Daemon
}

func RegisterSessionControl(server *grpc.Server, daemon *protocol.Daemon) {
	pb.RegisterSessionControlServer(server, &sessionControlService{daemon: daemon})
}

func validateControlRequest(request proto.Message) error {
	if request == nil || !request.ProtoReflect().IsValid() {
		return status.Error(codes.InvalidArgument, "control request is required")
	}
	return rejectNativeQueryUnknown(request.ProtoReflect())
}

func addCommandMeta(fields map[string]any, meta *pb.CommandMeta) error {
	if meta == nil || meta.OperationId == "" || len(meta.OperationId) > 256 {
		return status.Error(codes.InvalidArgument, "durable operation identity is required")
	}
	if meta.Canonicalization != pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 && meta.Canonicalization != pb.CanonicalizationVersion_CANONICALIZATION_VERSION_ASTRORDER_OPERATION_V1 {
		return status.Error(codes.InvalidArgument, "unsupported operation canonicalization")
	}
	// Legacy App command-only envelopes used command_id as the ledger key.
	// Preserve that payload rather than introducing a redundant operation_id.
	if meta.CommandId != nil {
		if *meta.CommandId == "" || len(*meta.CommandId) > 256 {
			return status.Error(codes.InvalidArgument, "command identity is invalid")
		}
		fields["command_id"] = *meta.CommandId
	}
	if meta.CommandId == nil || *meta.CommandId != meta.OperationId {
		fields["operation_id"] = meta.OperationId
	}
	if meta.Canonicalization == pb.CanonicalizationVersion_CANONICALIZATION_VERSION_ASTRORDER_OPERATION_V1 {
		fields["canonicalization"] = "astrorder-operation-v1"
	}
	return nil
}

func validateSessionRef(session *pb.SessionRef) error {
	if session == nil || session.SessionId == "" || len(session.SessionId) > 256 {
		return status.Error(codes.InvalidArgument, "session identity is invalid")
	}
	return nil
}

func controlFields(action string, session *pb.SessionRef) map[string]any {
	fields := map[string]any{"action": action}
	if session != nil {
		fields["session_id"] = session.SessionId
		if session.ConnectionId != nil {
			fields["connection_id"] = *session.ConnectionId
		}
	}
	return fields
}

func controlIdentity(session *pb.SessionRef) core.SessionReadIdentity {
	return core.SessionReadIdentity{AgentID: session.AgentId, ConnectionID: session.ConnectionId}
}

func ownerError(err error) error {
	switch {
	case errors.Is(err, core.ErrSessionNotOwned):
		return status.Error(codes.NotFound, "session is not daemon-owned")
	case errors.Is(err, core.ErrSessionIdentity):
		return status.Error(codes.FailedPrecondition, "session identity does not match owner")
	default:
		return status.Error(codes.FailedPrecondition, "session authority is unavailable")
	}
}

func (s *sessionControlService) execute(ctx context.Context, request proto.Message, session *pb.SessionRef, meta *pb.CommandMeta, fields map[string]any) (*pb.CommandReceipt, error) {
	if err := validateControlRequest(request); err != nil {
		return nil, err
	}
	if err := addCommandMeta(fields, meta); err != nil {
		return nil, err
	}
	if session != nil {
		if err := validateSessionRef(session); err != nil {
			return nil, err
		}
		if _, err := s.daemon.ResolveSessionOwner(session.SessionId, controlIdentity(session)); err != nil {
			// A completed delete/close can already have removed ownership. The
			// ledger still checks payload equality before returning its receipt.
			if !errors.Is(err, core.ErrSessionNotOwned) {
				return nil, ownerError(err)
			}
			action, _ := fields["action"].(string)
			lookup := protocol.OperationLookup{Action: action, OperationID: meta.OperationId, SessionID: &session.SessionId, ConnectionID: session.ConnectionId}
			stored, readErr := s.daemon.ReadStoredOperation(lookup)
			if readErr != nil {
				return nil, status.Error(codes.Unavailable, "operation storage unavailable")
			}
			if stored.State == protocol.StoredOperationStateMissing {
				return nil, ownerError(err)
			}
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "control payload is invalid")
	}
	if len(raw) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "control payload exceeds business limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	result := s.daemon.ExecuteSessionControl(ctx, fields)
	return commandReceipt(s.daemon.DaemonID(), session, meta, result)
}

func commandReceipt(daemonID string, session *pb.SessionRef, meta *pb.CommandMeta, envelope map[string]any) (*pb.CommandReceipt, error) {
	receipt := &pb.CommandReceipt{DaemonId: daemonID, OperationId: meta.OperationId, Session: session, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED}
	if envelope["action"] == "error" {
		if envelope["operation_state"] == "uncertain" {
			receipt.OperationState = pb.OperationState_OPERATION_STATE_UNCERTAIN
			msg := "native command outcome is uncertain; reconcile before retry"
			if detail, ok := envelope["detail"].(string); ok && detail != "" {
				msg = fmt.Sprintf("native command outcome is uncertain: %s", detail)
			}
			receipt.Outcome = &pb.CommandReceipt_Uncertain{Uncertain: businessError(pb.ErrorKind_ERROR_KIND_FAILED_PRECONDITION, msg, false)}
			return receipt, nil
		}
		message, _ := envelope["detail"].(string)
		code := codes.FailedPrecondition
		if strings.Contains(message, "different payload") || strings.Contains(message, "conflict") {
			code = codes.AlreadyExists
		}
		if strings.Contains(message, "storage") || strings.Contains(message, "durable") {
			code = codes.Unavailable
		}
		if message != "" {
			return nil, status.Error(code, fmt.Sprintf("daemon command was rejected before a confirmed receipt: %s", message))
		}
		return nil, status.Error(code, "daemon command was rejected before a confirmed receipt")
	}
	payload, ok := envelope["result"].(map[string]any)
	if !ok {
		return nil, status.Error(codes.Unavailable, "daemon command receipt is invalid")
	}
	if sid, ok := envelope["session_id"].(string); ok && sid != "" {
		if session == nil {
			receipt.Session = &pb.SessionRef{SessionId: sid}
		} else {
			receipt.Session = proto.Clone(session).(*pb.SessionRef)
			receipt.Session.SessionId = sid
		}
	}
	state, _ := payload["status"].(string)
	var valid bool
	receipt.Status, valid = sessionStatus(state)
	if !valid {
		return nil, status.Error(codes.Unavailable, "native command status is invalid")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, status.Error(codes.Internal, "native command receipt is invalid")
	}
	if len(raw) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "command receipt exceeds business limit; query stored operation")
	}
	completed := &pb.CommandResult{Native: &pb.NativeJson{Schema: "astrorder.session.command-result.v1", Utf8Json: raw}}
	completed.Accepted, _ = payload["accepted"].(bool)
	completed.TerminationConfirmed, _ = payload["termination_confirmed"].(bool)
	for name, destination := range map[string]**string{"turn_id": &completed.TurnId, "title": &completed.Title, "model": &completed.Model, "provider": &completed.Provider, "effort": &completed.Effort} {
		if v, ok := payload[name].(string); ok {
			value := v
			*destination = &value
		}
	}
	receipt.Accepted = completed.Accepted
	receipt.TurnId = completed.TurnId
	receipt.Outcome = &pb.CommandReceipt_Completed{Completed: completed}
	return receipt, nil
}

func nativeControlOptions(native *pb.NativeJson, reserved ...string) (map[string]any, error) {
	if native == nil {
		return map[string]any{}, nil
	}
	value, err := decodeQueryJSON(native)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, status.Error(codes.InvalidArgument, "native options must be an object")
	}
	for _, key := range reserved {
		if _, exists := object[key]; exists {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("native options cannot override %s", key))
		}
	}
	return object, nil
}
