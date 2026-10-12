package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/grpcapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func isolatedDaemonEnv(values ...string) []string {
	blocked := []string{
		"ASTRORDER_SESSION_DAEMON_SECRET=",
		"ASTRORDER_SESSION_DAEMON_PORT=",
		"ASTRORDER_SESSION_DAEMON_DB=",
		"ASTRORDER_SESSION_DAEMON_CONFIG=",
		"ASTRORDER_SESSION_DAEMON_GRPC_TARGET=",
		"ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL=",
		"ASTRORDER_SESSION_DAEMON_WS_DISABLED=",
	}
	environment := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		skip := false
		for _, prefix := range blocked {
			if strings.HasPrefix(entry, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			environment = append(environment, entry)
		}
	}
	return append(environment, values...)
}

func TestDaemonRequiresGRPCConfig(t *testing.T) {
	if err := validateGRPCConfig("", ""); err == nil {
		t.Fatal("daemon accepted missing gRPC configuration")
	}
}

func TestDaemonConfiguredGRPCShutdownExitsProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_GRPC_DAEMON_CHILD") == "1" {
		main()
		return
	}
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	reserved.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonConfiguredGRPCShutdownExitsProcess$")
	cmd.Env = isolatedDaemonEnv(
		"ASTRORDER_TEST_GRPC_DAEMON_CHILD=1",
		"ASTRORDER_SESSION_DAEMON_SECRET=legacy-test-secret",
		"ASTRORDER_SESSION_DAEMON_DB="+filepath.Join(t.TempDir(), "daemon.db"),
		"ASTRORDER_SESSION_DAEMON_CONFIG=",
		"ASTRORDER_SESSION_DAEMON_GRPC_TARGET="+address,
		"ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL=grpc-daemon-test-secret",
		"ASTRORDER_SESSION_DAEMON_WS_DISABLED=1",
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var conn *grpc.ClientConn
	for {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, 100*time.Millisecond)
		conn, err = grpc.DialContext(
			attemptCtx,
			address,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(grpcapi.MaxWireBytes), grpc.MaxCallSendMsgSize(grpcapi.MaxWireBytes)),
		)
		attemptCancel()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(err)
		}
	}
	defer conn.Close()
	callCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(grpcapi.AuthorizationMetadataKey, "Bearer grpc-daemon-test-secret"))
	client := sessiondv1.NewDaemonControlClient(conn)
	capabilities, err := client.GetCapabilities(callCtx, &sessiondv1.GetCapabilitiesRequest{})
	if err != nil || capabilities.DaemonId == "" {
		t.Fatalf("configured gRPC was not ready: %+v, %v", capabilities, err)
	}
	if !capabilities.OperationLookup {
		t.Fatal("configured daemon does not advertise operation lookup")
	}
	receipt, err := sessiondv1.NewOperationQueryClient(conn).GetOperation(callCtx, &sessiondv1.GetOperationRequest{Key: &sessiondv1.OperationKey{
		OperationId: "not-executed", LegacyAction: "session.create",
		Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
	}})
	if err != nil || receipt.State != sessiondv1.OperationState_OPERATION_STATE_MISSING || receipt.DaemonId != capabilities.DaemonId {
		t.Fatalf("configured daemon operation lookup failed: %+v, %v", receipt, err)
	}
	response, err := client.Shutdown(callCtx, &sessiondv1.ShutdownRequest{ExpectedDaemonId: &capabilities.DaemonId})
	if err != nil || !response.Accepted {
		t.Fatalf("gRPC shutdown was not accepted: %+v, %v", response, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon acknowledged gRPC shutdown but process did not exit")
	}
	if connection, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		connection.Close()
		t.Fatal("gRPC listener survived shutdown")
	}
}

func TestDaemonRejectsIncompleteOrNonLoopbackGRPCConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		secret string
	}{
		{name: "missing credential", target: "127.0.0.1:30019"},
		{name: "missing target", secret: "secret"},
		{name: "non-loopback", target: "0.0.0.0:30019", secret: "secret"},
		{name: "unspecified port", target: "127.0.0.1:0", secret: "secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateGRPCConfig(tc.target, tc.secret); err == nil {
				t.Fatalf("invalid config was accepted: target=%q credential=%t", tc.target, tc.secret != "")
			}
		})
	}
	if err := validateGRPCConfig("127.0.0.1:30019", "secret"); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}
