package grpcapi

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

const (
	AuthorizationMetadataKey = "authorization"

	MaxBusinessBytes = protocol.MaxBusinessBytes
	MaxWireBytes     = 33_554_432

	defaultSubscriptionQueueCapacity = 256
	defaultReplayPageSize            = 256
	defaultSendTimeout               = 30 * time.Second
)

type Config struct {
	Credential string
}

type Option func(*options)

type options struct {
	subscriptionQueueCapacity int
	replayPageHook            func(string, int)
	sendTimeout               time.Duration
	sendHook                  func(string)
}

func WithSubscriptionQueueCapacity(capacity int) Option {
	return func(options *options) { options.subscriptionQueueCapacity = capacity }
}

func WithReplayPageHook(hook func(sessionID string, page int)) Option {
	return func(options *options) { options.replayPageHook = hook }
}

func WithSendTimeout(timeout time.Duration) Option {
	return func(options *options) { options.sendTimeout = timeout }
}

func WithSendHook(hook func(kind string)) Option {
	return func(options *options) { options.sendHook = hook }
}

type shutdownRPCKey struct{}
type shutdownRPCState struct{ accepted atomic.Bool }

type shutdownStats struct{ daemon *protocol.Daemon }

func (handler shutdownStats) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	if info.FullMethodName == sessiondv1.DaemonControl_Shutdown_FullMethodName {
		return context.WithValue(ctx, shutdownRPCKey{}, &shutdownRPCState{})
	}
	return ctx
}

func (handler shutdownStats) HandleRPC(ctx context.Context, event stats.RPCStats) {
	state, _ := ctx.Value(shutdownRPCKey{}).(*shutdownRPCState)
	end, finished := event.(*stats.End)
	if finished && !end.Client && state != nil && state.accepted.Load() {
		// End runs after response serialization and status submission. The
		// process owner must still drain the server before closing the daemon.
		if end.Error == nil {
			handler.daemon.SignalShutdown()
		} else {
			handler.daemon.CancelShutdown()
		}
	}
}

func (shutdownStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (shutdownStats) HandleConn(context.Context, stats.ConnStats)                       {}

func shutdownAfterResponseInterceptor(_ *protocol.Daemon) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, err error) {
		response, err = handler(ctx, request)
		if info.FullMethod == sessiondv1.DaemonControl_Shutdown_FullMethodName && err == nil {
			if reply, ok := response.(*sessiondv1.ShutdownResponse); ok && reply.Accepted {
				if state, ok := ctx.Value(shutdownRPCKey{}).(*shutdownRPCState); ok {
					state.accepted.Store(true)
				}
			}
		}
		return response, err
	}
}

func NewServer(config Config, daemon *protocol.Daemon, supplied ...Option) (*grpc.Server, error) {
	if daemon == nil {
		return nil, fmt.Errorf("daemon authority is required")
	}
	if config.Credential == "" {
		return nil, fmt.Errorf("gRPC daemon credential is required")
	}
	settings := options{subscriptionQueueCapacity: defaultSubscriptionQueueCapacity, sendTimeout: defaultSendTimeout}
	for _, apply := range supplied {
		if apply != nil {
			apply(&settings)
		}
	}
	if settings.subscriptionQueueCapacity < 1 || settings.subscriptionQueueCapacity > 65536 {
		return nil, fmt.Errorf("invalid gRPC subscription queue capacity")
	}
	if settings.sendTimeout <= 0 {
		return nil, fmt.Errorf("invalid gRPC stream send timeout")
	}
	authenticate := authenticator(config.Credential)
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(MaxWireBytes),
		grpc.MaxSendMsgSize(MaxWireBytes),
		grpc.StatsHandler(shutdownStats{daemon: daemon}),
		grpc.ChainUnaryInterceptor(authenticate.unary, shutdownAfterResponseInterceptor(daemon)),
		grpc.ChainStreamInterceptor(authenticate.stream),
	)
	service := &service{daemon: daemon, options: settings}
	sessiondv1.RegisterDaemonControlServer(server, service)
	sessiondv1.RegisterEventStreamServer(server, service)
	RegisterOperationQuery(server, daemon)
	RegisterRuntimeQuery(server, daemon)
	RegisterSessionControl(server, daemon)
	RegisterModelConfig(server, daemon)
	registerTerminalStream(server, daemon, settings)
	RegisterConnectorIngress(server, daemon)
	return server, nil
}

type authenticator string

func (expected authenticator) verify(ctx context.Context) error {
	incoming, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "daemon authentication failed")
	}
	values := incoming.Get(AuthorizationMetadataKey)
	if len(values) != 1 {
		return status.Error(codes.Unauthenticated, "daemon authentication failed")
	}
	const prefix = "Bearer "
	supplied := values[0]
	if !strings.HasPrefix(supplied, prefix) {
		return status.Error(codes.Unauthenticated, "daemon authentication failed")
	}
	credential := supplied[len(prefix):]
	if credential == "" || subtle.ConstantTimeCompare([]byte(credential), []byte(expected)) != 1 {
		return status.Error(codes.Unauthenticated, "daemon authentication failed")
	}
	return nil
}

func (expected authenticator) unary(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := expected.verify(ctx); err != nil {
		return nil, err
	}
	return handler(ctx, request)
}

func (expected authenticator) stream(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := expected.verify(stream.Context()); err != nil {
		return err
	}
	return handler(server, stream)
}
