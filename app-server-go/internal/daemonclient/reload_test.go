package daemonclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeReloadService struct {
	authCheck func(context.Context) error
	handler   func(context.Context, *sessiondv1.NativeJson) (*sessiondv1.NativeJson, error)
}

type fakeReloadServerIface interface {
	ReloadConfig(context.Context, *sessiondv1.NativeJson) (*sessiondv1.NativeJson, error)
}

func (f *fakeReloadService) ReloadConfig(ctx context.Context, req *sessiondv1.NativeJson) (*sessiondv1.NativeJson, error) {
	if f.authCheck != nil {
		if err := f.authCheck(ctx); err != nil {
			return nil, err
		}
	}
	return f.handler(ctx, req)
}

func registerFakeReloadService(s *grpc.Server, impl *fakeReloadService) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: daemonclient.RuntimeReloadServiceName,
		HandlerType: (*fakeReloadServerIface)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "ReloadConfig",
				Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
					in := new(sessiondv1.NativeJson)
					if err := dec(in); err != nil {
						return nil, err
					}
					return srv.(fakeReloadServerIface).ReloadConfig(ctx, in)
				},
			},
		},
	}, impl)
}

func TestClientReloadConfigGracefulFallbackOnOldDaemon(t *testing.T) {
	ctx := context.Background()
	// Old daemon only registers the original 8 services (no RuntimeReload).
	fake := &fakeDaemonServer{
		expectedAuth: "legacy-secret",
		caps:         defaultCaps(),
	}
	addr, stop := startFakeDaemonServer(t, fake)
	defer stop()

	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         addr,
		Credential:     "legacy-secret",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial against older daemon should succeed: %v", err)
	}
	defer client.Close()

	if client.SupportsReloadConfig() {
		t.Fatal("SupportsReloadConfig should be false for older daemon capabilities")
	}

	res, err := client.ReloadConfig(ctx)
	if !errors.Is(err, daemonclient.ErrReloadUnsupported) {
		t.Fatalf("expected ErrReloadUnsupported on old daemon, got err=%v", err)
	}
	if res == nil || res.Supported || res.DaemonID != "daemon-test-123" {
		t.Fatalf("expected Supported=false result with daemon ID, got: %+v", res)
	}
}

func TestClientReloadConfigSuccessAndOptions(t *testing.T) {
	ctx := context.Background()
	caps := defaultCaps()
	caps.ProtocolMinor = 3
	caps.Services = append(caps.Services, daemonclient.RuntimeReloadServiceName)
	fake := &fakeDaemonServer{
		expectedAuth: "reload-secret",
		caps:         caps,
	}

	var receivedOpts daemonclient.ReloadConfigOptions
	reloadImpl := &fakeReloadService{
		authCheck: fake.checkAuth,
		handler: func(_ context.Context, req *sessiondv1.NativeJson) (*sessiondv1.NativeJson, error) {
			if err := json.Unmarshal(req.GetUtf8Json(), &receivedOpts); err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			payload, err := json.Marshal(daemonclient.ReloadConfigResult{
				Supported:         true,
				DaemonID:          "daemon-test-123",
				ConfigPath:        "/tmp/session-daemon.json",
				ReloadedAt:        "2026-11-27T09:00:00Z",
				AddedRuntimes:     []string{"codex"},
				UpdatedRuntimes:   []string{"hermes"},
				UnchangedRuntimes: []string{"pty"},
				Registered:        []string{"codex", "codex-ssh", "hermes", "pty"},
				PreservedSessions: 2,
				Warnings:          []string{"test warning"},
			})
			if err != nil {
				return nil, err
			}
			return &sessiondv1.NativeJson{
				Schema:   "astrorder.daemon.reload-config-response.v1",
				Utf8Json: payload,
			}, nil
		},
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(g, fake)
	sessiondv1.RegisterSessionControlServer(g, fake)
	sessiondv1.RegisterRuntimeQueryServer(g, fake)
	sessiondv1.RegisterOperationQueryServer(g, fake)
	sessiondv1.RegisterModelConfigServer(g, fake)
	sessiondv1.RegisterEventStreamServer(g, fake)
	sessiondv1.RegisterTerminalStreamServer(g, fake)
	sessiondv1.RegisterConnectorIngressServer(g, fake)
	registerFakeReloadService(g, reloadImpl)
	go func() { _ = g.Serve(lis) }()
	defer func() {
		g.Stop()
		_ = lis.Close()
	}()

	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         lis.Addr().String(),
		Credential:     "reload-secret",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer client.Close()

	if !client.SupportsReloadConfig() {
		t.Fatal("SupportsReloadConfig should be true when RuntimeReload service is advertised")
	}

	probe := true
	res, err := client.ReloadConfigWithOptions(ctx, daemonclient.ReloadConfigOptions{
		ProbePath:     &probe,
		ExtraPathDirs: []string{"/opt/astrorder/runtimes/node", "/opt/astrorder/runtimes/uv"},
	})
	if err != nil {
		t.Fatalf("ReloadConfigWithOptions failed: %v", err)
	}
	if receivedOpts.ProbePath == nil || !*receivedOpts.ProbePath {
		t.Fatalf("server did not receive probe_path=true: %+v", receivedOpts)
	}
	if !reflect.DeepEqual(receivedOpts.ExtraPathDirs, []string{"/opt/astrorder/runtimes/node", "/opt/astrorder/runtimes/uv"}) {
		t.Fatalf("server received unexpected extra_path_dirs: %v", receivedOpts.ExtraPathDirs)
	}
	if !res.Supported || res.PreservedSessions != 2 || !reflect.DeepEqual(res.AddedRuntimes, []string{"codex"}) || !reflect.DeepEqual(res.UpdatedRuntimes, []string{"hermes"}) {
		t.Fatalf("unexpected ReloadConfigResult: %+v", res)
	}
}

func TestClientReloadConfigNilReceiverSafety(t *testing.T) {
	var c *daemonclient.Client
	if c.SupportsReloadConfig() {
		t.Fatal("nil client should not report reload support")
	}
	if _, err := c.ReloadConfig(context.Background()); err == nil {
		t.Fatal("nil client ReloadConfig should return an error")
	}
}
