package grpcapi

import (
	"context"
	"errors"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

func TestShutdownCompletionOnlySignalsAcceptedSuccessfulRPC(t *testing.T) {
	for _, test := range []struct {
		name            string
		accepted        bool
		completionError error
		client          bool
		method          string
		wantSignal      bool
	}{
		{name: "accepted", accepted: true, method: sessiondv1.DaemonControl_Shutdown_FullMethodName, wantSignal: true},
		{name: "refused", method: sessiondv1.DaemonControl_Shutdown_FullMethodName},
		{name: "transport failed", accepted: true, completionError: errors.New("response transport failed"), method: sessiondv1.DaemonControl_Shutdown_FullMethodName},
		{name: "client end", accepted: true, client: true, method: sessiondv1.DaemonControl_Shutdown_FullMethodName},
		{name: "another method", accepted: true, method: sessiondv1.DaemonControl_GetStatus_FullMethodName},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemon := protocol.New("fixture-secret")
			defer daemon.Close()
			completion := shutdownStats{daemon: daemon}
			if test.accepted && test.method == sessiondv1.DaemonControl_Shutdown_FullMethodName {
				if _, err := daemon.AuthorizeShutdown(nil, false); err != nil {
					t.Fatal(err)
				}
			}
			ctx := completion.TagRPC(context.Background(), &stats.RPCTagInfo{FullMethodName: test.method})
			_, err := shutdownAfterResponseInterceptor(daemon)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: test.method}, func(context.Context, any) (any, error) {
				return &sessiondv1.ShutdownResponse{Accepted: test.accepted}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			completion.HandleRPC(ctx, &stats.OutPayload{})
			select {
			case <-daemon.ShutdownRequested():
				t.Fatal("shutdown preceded RPC completion")
			default:
			}
			completion.HandleRPC(ctx, &stats.End{Client: test.client, Error: test.completionError})
			select {
			case <-daemon.ShutdownRequested():
				if !test.wantSignal {
					t.Fatal("non-successful shutdown signaled")
				}
			default:
				if test.wantSignal {
					t.Fatal("successful accepted shutdown not signaled")
				}
			}
			if test.completionError != nil {
				if _, err := daemon.AppendEvent(protocol.EventInput{SessionID: "resumed", Event: "fixture", Status: "idle"}); err != nil {
					t.Fatal("failed response did not release admission")
				}
			}
		})
	}
}
