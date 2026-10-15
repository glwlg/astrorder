package lifecycle

import (
	"context"

	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

// A real disposable process releases its RPC listener immediately, then takes
// longer than the former ten-second cutoff to clean up owned runtimes.
type slowShutdownService struct {
	pb.UnimplementedDaemonControlServer
	shutdown chan struct{}
}

func (s *slowShutdownService) GetCapabilities(context.Context, *pb.GetCapabilitiesRequest) (*pb.GetCapabilitiesResponse, error) {
	return &pb.GetCapabilitiesResponse{
		ProtocolMajor: 1, DaemonId: "isolated-slow-shutdown",
		Services:                 []string{"astrorder.sessiond.v1.ConnectorIngress", "astrorder.sessiond.v1.DaemonControl", "astrorder.sessiond.v1.EventStream", "astrorder.sessiond.v1.ModelConfig", "astrorder.sessiond.v1.OperationQuery", "astrorder.sessiond.v1.RuntimeQuery", "astrorder.sessiond.v1.SessionControl", "astrorder.sessiond.v1.TerminalStream"},
		CanonicalizationVersions: []pb.CanonicalizationVersion{pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1},
		MaxBusinessBytes:         pb.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		MaxWireBytes:             pb.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		EventStreamVersion:       pb.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:          true,
	}, nil
}
func (s *slowShutdownService) GetStatus(context.Context, *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	pending := uint64(0)
	return &pb.GetStatusResponse{DaemonId: "isolated-slow-shutdown", PendingOperations: &pending}, nil
}
func (s *slowShutdownService) Shutdown(context.Context, *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	select {
	case s.shutdown <- struct{}{}:
	default:
	}
	return &pb.ShutdownResponse{Accepted: true, GracefulDrainStarted: true}, nil
}
func TestSlowShutdownHelperProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_SLOW_SHUTDOWN_HELPER") != "1" {
		return
	}
	listener, err := net.Listen("tcp4", os.Getenv("ASTRORDER_SLOW_SHUTDOWN_TARGET"))
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	service := &slowShutdownService{shutdown: make(chan struct{}, 1)}
	pb.RegisterDaemonControlServer(server, service)
	go server.Serve(listener)
	<-service.shutdown
	server.GracefulStop()
	listener.Close()
	time.Sleep(12 * time.Second)
}

func TestDaemonUpdateWaitsForSlowProcessExit(t *testing.T) {
	candidate := os.Getenv("ASTRORDER_TEST_DAEMON")
	if candidate == "" {
		t.Skip("set ASTRORDER_TEST_DAEMON for real replacement startup")
	}
	root := t.TempDir()
	state := t.TempDir()
	executable := filepath.Join(root, "astrorder-sessiond.exe")
	copyBinary := func(source, target string) {
		t.Helper()
		input, err := os.Open(source)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		output, err := os.Create(target)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("copy: %v %v", copyErr, closeErr)
		}
	}
	copyBinary(os.Args[0], executable)
	copyBinary(candidate, executable+".new")
	address := unusedLoopbackAddress(t)
	config := filepath.Join(state, "runtimes.json")
	if err := os.WriteFile(config, []byte(`{"runtimes":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestSlowShutdownHelperProcess$")
	command.Env = append(os.Environ(), "ASTRORDER_SLOW_SHUTDOWN_HELPER=1", "ASTRORDER_SLOW_SHUTDOWN_TARGET="+address)
	log, err := os.Create(filepath.Join(state, "helper.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})
	identity, err := InspectProcess(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDaemonProcessMetadata(state, identity); err != nil {
		t.Fatal(err)
	}
	options := DaemonOptions{ExecutablePath: executable, StateDir: state, WorkingDir: root, GRPCTarget: address, Port: mustPort(t, address), ConfigPath: config, DatabasePath: filepath.Join(state, "daemon.sqlite3"), Secret: "isolated-secret", GRPCCredential: "isolated-credential"}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for {
		status, err := DaemonStatus(ctx, options)
		if err == nil && status.State == StateRunning {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("helper exited before ready: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	started := time.Now()
	updated, err := UpdateDaemonWithLifecycle(ctx, options, false)
	if err != nil {
		t.Fatalf("one update must wait for delayed old process and start replacement: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := StopDaemonWithLifecycle(cleanupCtx, options, false); err != nil {
			t.Errorf("replacement cleanup: %v", err)
		}
	})
	if !updated.Updated || updated.State != StateRunning || !updated.IdentityVerified || updated.Process == identity {
		t.Fatalf("replacement not running after a single update: %+v", updated)
	}
	if time.Since(started) < 12*time.Second {
		t.Fatal("update did not wait for old process cleanup")
	}
	if _, err := InspectProcess(identity.PID); !os.IsNotExist(err) {
		t.Fatalf("old process still exists: %v", err)
	}
	if _, err := os.Stat(executable + ".new"); !os.IsNotExist(err) {
		t.Fatalf("candidate not consumed: %v", err)
	}
	if _, err := StopDaemonWithLifecycle(ctx, options, false); err != nil {
		t.Fatal(fmt.Errorf("replacement cleanup: %w", err))
	}
}
