package grpcapi

import (
	"context"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (s *runtimeQueryService) SetModel(ctx context.Context, r *pb.SetModelRequest) (*pb.CommandReceipt, error) {
	if r == nil || r.Model == "" {
		return nil, status.Error(codes.InvalidArgument, "model is required")
	}
	fields := controlFields("session.model.set", r.Session)
	fields["provider"] = r.Provider
	fields["model"] = r.Model
	return (&sessionControlService{daemon: s.daemon}).execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *runtimeQueryService) SetReasoning(ctx context.Context, r *pb.SetReasoningRequest) (*pb.CommandReceipt, error) {
	if r == nil || r.Effort == "" {
		return nil, status.Error(codes.InvalidArgument, "reasoning effort is required")
	}
	fields := controlFields("session.reasoning.set", r.Session)
	fields["effort"] = r.Effort
	return (&sessionControlService{daemon: s.daemon}).execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *runtimeQueryService) SetApprovalMode(ctx context.Context, r *pb.SetApprovalModeRequest) (*pb.CommandReceipt, error) {
	if r == nil || r.Mode != "manual" && r.Mode != "auto" && r.Mode != "full_access" {
		return nil, status.Error(codes.InvalidArgument, "approval mode is invalid")
	}
	fields := controlFields("session.approval.set", r.Session)
	fields["mode"] = r.Mode
	return (&sessionControlService{daemon: s.daemon}).execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *runtimeQueryService) ReadApprovalMode(ctx context.Context, r *pb.ReadApprovalModeRequest) (*pb.ApprovalModeState, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "approval read request is required")
	}
	payload, _, err := s.readSession(ctx, r, r.Session, "session.approval.read", nil)
	if err != nil {
		return nil, err
	}
	mode, err := readString(payload, "mode")
	if err != nil {
		return nil, err
	}
	if mode == nil {
		return nil, status.Error(codes.Unavailable, "native approval mode is absent")
	}
	raw, err := readString(payload, "raw_unknown")
	if err != nil {
		return nil, err
	}
	return &pb.ApprovalModeState{Session: proto.Clone(r.Session).(*pb.SessionRef), Mode: *mode, RawUnknown: raw}, nil
}
func (s *runtimeQueryService) DisconnectRuntime(ctx context.Context, r *pb.DisconnectRuntimeRequest) (*pb.DisconnectRuntimeResponse, error) {
	if err := validateControlRequest(r); err != nil {
		return nil, err
	}
	kind, _, err := controlTarget(r.Target)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{"action": "runtime.disconnect", "agent_type": kind}
	if r.Target.ConnectionId != nil {
		fields["connection_id"] = *r.Target.ConnectionId
	}
	if err := addCommandMeta(fields, r.Meta); err != nil {
		return nil, err
	}
	// Registry disconnect always requires authoritative idle. The confirmation
	// bit cannot silently relax the existing runtime safety guard.
	result := s.daemon.ExecuteSessionControl(ctx, fields)
	response := &pb.DisconnectRuntimeResponse{DaemonId: s.daemon.DaemonID(), OperationState: pb.OperationState_OPERATION_STATE_COMPLETED}
	if result["action"] == "error" {
		if result["operation_state"] == "uncertain" {
			response.OperationState = pb.OperationState_OPERATION_STATE_UNCERTAIN
			response.Error = businessError(pb.ErrorKind_ERROR_KIND_FAILED_PRECONDITION, "runtime disconnect outcome uncertain", false)
			return response, nil
		}
		return nil, status.Error(codes.FailedPrecondition, "runtime disconnect refused")
	}
	payload, ok := result["result"].(map[string]any)
	if !ok || payload["disconnected"] != true {
		return nil, status.Error(codes.Unavailable, "runtime disconnect is unconfirmed")
	}
	response.Disconnected = true
	switch values := payload["released_sessions"].(type) {
	case []string:
		response.ReleasedSessionIds = values
	case []any:
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, status.Error(codes.Unavailable, "runtime disconnect receipt is invalid")
			}
			response.ReleasedSessionIds = append(response.ReleasedSessionIds, text)
		}
	}
	return response, nil
}
