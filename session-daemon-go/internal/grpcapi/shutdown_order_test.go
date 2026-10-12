package grpcapi

import (
	"context"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type shutdownTestTransport struct{}

func (shutdownTestTransport) Method() string               { return sessiondv1.DaemonControl_Shutdown_FullMethodName }
func (shutdownTestTransport) SetHeader(metadata.MD) error  { return nil }
func (shutdownTestTransport) SendHeader(metadata.MD) error { return nil }
func (shutdownTestTransport) SetTrailer(metadata.MD) error { return nil }

func TestShutdownInterceptorDoesNotSignalBeforeResponseSerialization(t *testing.T) {
	daemon := protocol.New("fixture-secret")
	defer daemon.Close()
	ctx := grpc.NewContextWithServerTransportStream(context.Background(), shutdownTestTransport{})
	interceptor := shutdownAfterResponseInterceptor(daemon)
	response, err := interceptor(ctx, &sessiondv1.ShutdownRequest{}, &grpc.UnaryServerInfo{FullMethod: sessiondv1.DaemonControl_Shutdown_FullMethodName}, func(ctx context.Context, _ any) (any, error) {
		return (&service{daemon: daemon}).Shutdown(ctx, &sessiondv1.ShutdownRequest{})
	})
	if err != nil || !response.(*sessiondv1.ShutdownResponse).Accepted {
		t.Fatalf("shutdown handler rejected: %v, %v", response, err)
	}
	select {
	case <-daemon.ShutdownRequested():
		t.Fatal("shutdown signaled before gRPC serialized or wrote the response")
	default:
	}
}
