package daemonclient_test

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeDaemonServer struct {
	sessiondv1.UnimplementedDaemonControlServer
	sessiondv1.UnimplementedSessionControlServer
	sessiondv1.UnimplementedRuntimeQueryServer
	sessiondv1.UnimplementedOperationQueryServer
	sessiondv1.UnimplementedModelConfigServer
	sessiondv1.UnimplementedEventStreamServer
	sessiondv1.UnimplementedTerminalStreamServer
	sessiondv1.UnimplementedConnectorIngressServer

	expectedAuth string
	caps         *sessiondv1.GetCapabilitiesResponse
	capsErr      error
	authHits     atomic.Int32
	streamHits   atomic.Int32
}

func (s *fakeDaemonServer) checkAuth(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no metadata")
	}
	vals := md.Get(daemonclient.AuthorizationMetadataKey)
	if len(vals) == 0 || vals[0] != "Bearer "+s.expectedAuth {
		return status.Error(codes.Unauthenticated, "unauthorized")
	}
	s.authHits.Add(1)
	return nil
}

func (s *fakeDaemonServer) GetCapabilities(ctx context.Context, req *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if s.capsErr != nil {
		return nil, s.capsErr
	}
	return s.caps, nil
}

func (s *fakeDaemonServer) Subscribe(req *sessiondv1.SubscribeRequest, stream grpc.ServerStreamingServer[sessiondv1.EventStreamFrame]) error {
	if err := s.checkAuth(stream.Context()); err != nil {
		return err
	}
	s.streamHits.Add(1)
	<-stream.Context().Done()
	return stream.Context().Err()
}

func defaultCaps() *sessiondv1.GetCapabilitiesResponse {
	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor: 1,
		ProtocolMinor: 0,
		DaemonId:      "daemon-test-123",
		Services: []string{
			"astrorder.sessiond.v1.ConnectorIngress",
			"astrorder.sessiond.v1.DaemonControl",
			"astrorder.sessiond.v1.EventStream",
			"astrorder.sessiond.v1.ModelConfig",
			"astrorder.sessiond.v1.OperationQuery",
			"astrorder.sessiond.v1.RuntimeQuery",
			"astrorder.sessiond.v1.SessionControl",
			"astrorder.sessiond.v1.TerminalStream",
		},
		CanonicalizationVersions: []sessiondv1.CanonicalizationVersion{
			sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
		},
		MaxWireBytes:       sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		MaxBusinessBytes:   sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		EventStreamVersion: sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:    true,
	}
}

func startFakeDaemonServer(t *testing.T, srv *fakeDaemonServer) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	g := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(g, srv)
	sessiondv1.RegisterSessionControlServer(g, srv)
	sessiondv1.RegisterRuntimeQueryServer(g, srv)
	sessiondv1.RegisterOperationQueryServer(g, srv)
	sessiondv1.RegisterModelConfigServer(g, srv)
	sessiondv1.RegisterEventStreamServer(g, srv)
	sessiondv1.RegisterTerminalStreamServer(g, srv)
	sessiondv1.RegisterConnectorIngressServer(g, srv)

	go func() {
		_ = g.Serve(lis)
	}()

	cleanup := func() {
		g.Stop()
		_ = lis.Close()
	}
	return lis.Addr().String(), cleanup
}

func TestClientDialAndTargetValidation(t *testing.T) {
	ctx := context.Background()

	// Rejects empty or WS
	for _, invalid := range []string{"", "ws://127.0.0.1:8080", "wss://127.0.0.1:8080", "192.168.1.1:9090", "example.com:9090"} {
		_, err := daemonclient.Dial(ctx, daemonclient.Config{
			Target:     invalid,
			Credential: "secret",
		})
		if err == nil {
			t.Fatalf("expected target %q to be rejected", invalid)
		}
	}

	// Rejects empty credential
	_, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:     "127.0.0.1:9999",
		Credential: "",
	})
	if err == nil {
		t.Fatal("expected empty credential to be rejected")
	}
}

func TestClientAuthenticationAndCapabilitiesValidation(t *testing.T) {
	ctx := context.Background()
	fake := &fakeDaemonServer{
		expectedAuth: "test-secret",
		caps:         defaultCaps(),
	}
	addr, stop := startFakeDaemonServer(t, fake)
	defer stop()

	// Wrong secret rejected
	_, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         addr,
		Credential:     "wrong-secret",
		RequestTimeout: 2 * time.Second,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unauthenticated") {
		t.Fatalf("expected unauthenticated error, got: %v", err)
	}

	// Valid dial succeeds
	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         addr,
		Credential:     "test-secret",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer client.Close()

	if client.DaemonID() != "daemon-test-123" {
		t.Fatalf("unexpected daemon ID: %s", client.DaemonID())
	}
	caps := client.Capabilities()
	if caps == nil || len(caps.Services) != 8 {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}

	// Test stream call auth
	streamCtx, cancel := context.WithCancel(ctx)
	regCancel := client.RegisterSubscription(cancel)
	defer regCancel()

	stream, err := client.Events.Subscribe(streamCtx, &sessiondv1.SubscribeRequest{})
	if err != nil {
		t.Fatalf("stream call failed: %v", err)
	}
	go func() {
		_, _ = stream.Recv()
	}()

	time.Sleep(50 * time.Millisecond)
	if fake.streamHits.Load() == 0 {
		t.Fatal("stream call did not reach server with proper auth")
	}

	// Close releases connection without daemon stop
	if err := client.Close(); err != nil {
		t.Fatalf("client.Close failed: %v", err)
	}
}

func TestClientCapabilitiesRejections(t *testing.T) {
	ctx := context.Background()

	// Wrong protocol major
	{
		fake := &fakeDaemonServer{
			expectedAuth: "sec",
			caps:         defaultCaps(),
		}
		fake.caps.ProtocolMajor = 2
		addr, stop := startFakeDaemonServer(t, fake)
		_, err := daemonclient.Dial(ctx, daemonclient.Config{Target: addr, Credential: "sec", RequestTimeout: time.Second})
		stop()
		if err == nil || !strings.Contains(err.Error(), "unsupported protocol major") {
			t.Fatalf("expected major version error, got: %v", err)
		}
	}

	// Missing required service
	{
		fake := &fakeDaemonServer{
			expectedAuth: "sec",
			caps:         defaultCaps(),
		}
		fake.caps.Services = []string{"ConnectorIngress"}
		addr, stop := startFakeDaemonServer(t, fake)
		_, err := daemonclient.Dial(ctx, daemonclient.Config{Target: addr, Credential: "sec", RequestTimeout: time.Second})
		stop()
		if err == nil || !strings.Contains(err.Error(), "missing required service") {
			t.Fatalf("expected missing service error, got: %v", err)
		}
	}

	// Invalid wire limit
	{
		fake := &fakeDaemonServer{
			expectedAuth: "sec",
			caps:         defaultCaps(),
		}
		fake.caps.MaxWireBytes = 1000
		addr, stop := startFakeDaemonServer(t, fake)
		_, err := daemonclient.Dial(ctx, daemonclient.Config{Target: addr, Credential: "sec", RequestTimeout: time.Second})
		stop()
		if err == nil || !strings.Contains(err.Error(), "unsupported wire limit") {
			t.Fatalf("expected wire limit error, got: %v", err)
		}
	}
}
