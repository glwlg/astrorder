package grpcapi

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"astrorder.dev/contracts/agycredential"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var agyAccountID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (s *runtimeQueryService) ManageAgyAccount(ctx context.Context, req *pb.AgyAccountRequest) (*pb.AgyAccountResponse, error) {
	invalid := status.Error(codes.InvalidArgument, "AGY 账号请求无效")
	if req == nil || req.Target == nil || len(req.Credential) > agycredential.MaxBytes || proto.Size(req)-len(req.Credential) > 16*1024 {
		return nil, invalid
	}
	if err := rejectNativeQueryUnknown(req.ProtoReflect()); err != nil {
		return nil, err
	}
	kind := "antigravity"
	switch req.Target.Runtime {
	case pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY:
	case pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH:
		kind = "antigravity-ssh"
	default:
		return nil, invalid
	}
	params, err := nativeQueryConnection(req.Target, kind == "antigravity-ssh")
	if err != nil {
		return nil, err
	}
	id := req.AccountId
	apply := req.Action == pb.AgyAccountAction_AGY_ACCOUNT_ACTION_APPLY_CREDENTIAL
	if req.ConfirmActive && !apply {
		return nil, invalid
	}
	method := ""
	switch req.Action {
	case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_STATE, pb.AgyAccountAction_AGY_ACCOUNT_ACTION_CAPTURE_CREDENTIAL:
		if id != "" || len(req.Credential) != 0 {
			return nil, invalid
		}
		method = "account.vault.state"
		if req.Action == pb.AgyAccountAction_AGY_ACCOUNT_ACTION_CAPTURE_CREDENTIAL {
			method = "account.vault.capture"
		}
	case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_QUOTA, pb.AgyAccountAction_AGY_ACCOUNT_ACTION_APPLY_CREDENTIAL:
		if id == "current" && !apply {
			if len(req.Credential) != 0 {
				return nil, invalid
			}
		} else if _, valid := agycredential.Identity(req.Credential); !valid || !agyAccountID.MatchString(id) {
			return nil, invalid
		}
		method = "account.vault.quota"
	default:
		return nil, invalid
	}
	request := core.Request{AgentType: kind, Action: "agy.account", Fields: map[string]any{
		"method": method, "params": params, "account_id": id, "credential": req.Credential,
	}}
	var result map[string]any
	if apply {
		result, err = s.daemon.SwitchAgyAccount(ctx, request, req.Target.GetConnectionId(), req.ConfirmActive)
	} else {
		result, err = s.daemon.QueryNative(ctx, request)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if method == "account.vault.capture" {
		credential, _ := result["credential"].([]byte)
		if _, valid := agycredential.Identity(credential); !valid {
			return nil, status.Error(codes.FailedPrecondition, "目标 AGY 凭证格式无效")
		}
		return &pb.AgyAccountResponse{Credential: credential}, nil
	}
	delete(result, "credential")
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > MaxBusinessBytes {
		return nil, status.Error(codes.Internal, "AGY 账号响应无效")
	}
	return &pb.AgyAccountResponse{Result: &pb.NativeJson{Schema: "astrorder.agy-account-vault-result.v1", Utf8Json: raw}}, nil
}
