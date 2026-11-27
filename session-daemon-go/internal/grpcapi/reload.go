package grpcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	RuntimeReloadServiceName     = "astrorder.sessiond.v1.RuntimeReload"
	ReloadConfigMethodName       = "ReloadConfig"
	ReloadConfigFullMethodName   = "/astrorder.sessiond.v1.RuntimeReload/ReloadConfig"
	ReloadConfigRequestSchema    = "astrorder.daemon.reload-config-request.v1"
	ReloadConfigResponseSchema   = "astrorder.daemon.reload-config-response.v1"
	maxReloadExtraPathDirEntries = 64
	maxReloadExtraPathDirLength  = 1024
)

type runtimeReloadServer interface {
	ReloadConfig(context.Context, *sessiondv1.NativeJson) (*sessiondv1.NativeJson, error)
}

type runtimeReloadService struct {
	daemon *protocol.Daemon
}

// RegisterRuntimeReload registers the dynamic Agent runtime hot-reload RPC
// service on the provided gRPC server using the standard protobuf NativeJson
// envelope so wire compatibility and Bearer auth interceptors are preserved.
func RegisterRuntimeReload(server *grpc.Server, daemon *protocol.Daemon) {
	server.RegisterService(&runtimeReloadServiceDesc, &runtimeReloadService{daemon: daemon})
}

func (s *runtimeReloadService) ReloadConfig(ctx context.Context, req *sessiondv1.NativeJson) (*sessiondv1.NativeJson, error) {
	if req != nil {
		if err := rejectNativeQueryUnknown(req.ProtoReflect()); err != nil {
			return nil, err
		}
		if len(req.Utf8Json) > MaxBusinessBytes {
			return nil, status.Error(codes.ResourceExhausted, "reload request exceeds business limit")
		}
		if len(req.Utf8Json) > 0 && !utf8.Valid(req.Utf8Json) {
			return nil, status.Error(codes.InvalidArgument, "reload request JSON must be UTF-8")
		}
	}

	var opts protocol.RuntimeReloadOptions
	if req != nil && len(bytes.TrimSpace(req.Utf8Json)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(req.Utf8Json))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&opts); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid reload config request payload")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, status.Error(codes.InvalidArgument, "reload config request must contain a single JSON object")
		}
	}
	if len(opts.ExtraPathDirs) > maxReloadExtraPathDirEntries {
		return nil, status.Error(codes.InvalidArgument, "too many extra_path_dirs entries")
	}
	for _, dir := range opts.ExtraPathDirs {
		if len(dir) > maxReloadExtraPathDirLength || strings.ContainsRune(dir, 0) {
			return nil, status.Error(codes.InvalidArgument, "invalid extra_path_dirs entry")
		}
	}

	result, err := s.daemon.ReloadRuntimeConfig(ctx, opts)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil, status.FromContextError(err).Err()
		case errors.Is(err, core.ErrRuntimeBusy):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case errors.Is(err, protocol.ErrRuntimeReloadNotConfigured):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Unavailable, err.Error())
		}
	}

	raw, err := json.Marshal(result)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to encode reload config response")
	}
	if len(raw) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "reload config response exceeds business limit")
	}
	return &sessiondv1.NativeJson{
		Schema:   ReloadConfigResponseSchema,
		Utf8Json: raw,
	}, nil
}

func _RuntimeReload_ReloadConfig_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(sessiondv1.NativeJson)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(runtimeReloadServer).ReloadConfig(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ReloadConfigFullMethodName,
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(runtimeReloadServer).ReloadConfig(ctx, req.(*sessiondv1.NativeJson))
	}
	return interceptor(ctx, in, info, handler)
}

var runtimeReloadServiceDesc = grpc.ServiceDesc{
	ServiceName: RuntimeReloadServiceName,
	HandlerType: (*runtimeReloadServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: ReloadConfigMethodName,
			Handler:    _RuntimeReload_ReloadConfig_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "astrorder/sessiond/v1/runtime_reload.proto",
}
