package grpcapi

import (
	"context"
	"encoding/json"
	"errors"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// RuntimeQuery exposes native discovery and session reads. The generated base
// explicitly returns Unimplemented for the remaining mutation methods.
type runtimeQueryService struct {
	sessiondv1.UnimplementedRuntimeQueryServer
	daemon *protocol.Daemon
}

func RegisterRuntimeQuery(server *grpc.Server, daemon *protocol.Daemon) {
	sessiondv1.RegisterRuntimeQueryServer(server, &runtimeQueryService{daemon: daemon})
}

func (s *runtimeQueryService) QueryNative(ctx context.Context, request *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
	native, err := translateNativeQuery(request)
	if err != nil {
		return nil, err
	}
	result, err := s.daemon.QueryNative(ctx, native)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil, status.FromContextError(err).Err()
		case errors.Is(err, core.ErrRuntimeBusy):
			return nil, status.Error(codes.FailedPrecondition, "native runtime admission is closed")
		default:
			return nil, status.Error(codes.Unavailable, "native runtime query failed")
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, status.Error(codes.Internal, "native query response is not valid JSON")
	}
	if len(raw) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "native query response exceeds business limit")
	}
	return &sessiondv1.NativeQueryResponse{Target: proto.Clone(request.Target).(*sessiondv1.RuntimeTarget), Result: &sessiondv1.NativeJson{Schema: "astrorder.runtime.native-query-result.v1", Utf8Json: raw}}, nil
}
