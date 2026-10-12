package grpcapi_test

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/grpcapi"
	"astrorder.dev/session-daemon/internal/modelconfig"
	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type modelConfigFixture struct {
	t      *testing.T
	home   string
	daemon *protocol.Daemon
	server *grpc.Server
	client sessiondv1.ModelConfigClient
	conn   *grpc.ClientConn
	ctx    context.Context
}

type dummyReloadAdapter struct {
	status  string
	reloads int
}

func (d *dummyReloadAdapter) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "s1", Status: d.status}, nil
}
func (d *dummyReloadAdapter) Spawn(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "s1", Status: d.status}, nil
}
func (d *dummyReloadAdapter) Command(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "s1", Status: d.status}, nil
}
func (d *dummyReloadAdapter) Destroy(context.Context, core.Request) error { return nil }
func (d *dummyReloadAdapter) Sessions() map[string]core.Result {
	return map[string]core.Result{"s1": {SessionID: "s1", Status: d.status}}
}
func (d *dummyReloadAdapter) ReloadConfig(context.Context) error {
	d.reloads++
	return nil
}

func startModelConfigFixture(t *testing.T, adapter *dummyReloadAdapter, runners ...func(context.Context, string, ...string) *exec.Cmd) *modelConfigFixture {
	return startModelConfigFixtureWithAssets(t, adapter, "", runners...)
}

func startModelConfigFixtureWithAssets(t *testing.T, adapter *dummyReloadAdapter, assets string, runners ...func(context.Context, string, ...string) *exec.Cmd) *modelConfigFixture {
	t.Helper()
	home := t.TempDir()
	daemon, err := protocol.Open("legacy-ws-secret", filepath.Join(t.TempDir(), "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	if adapter != nil {
		daemon.RegisterRuntime("codex", adapter)
		daemon.ExecuteSessionControl(context.Background(), map[string]any{"action": "session.create", "agent_type": "codex"})
	}

	cfg := modelconfig.Config{
		AllowedRoots:    []string{home},
		HelperAssetsDir: assets,
	}
	if len(runners) > 0 {
		cfg.RemoteCommandRunner = runners[0]
	}
	if err := daemon.ConfigureModelConfig(cfg); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := grpcapi.NewServer(grpcapi.Config{Credential: testSecret}, daemon)
	if err != nil {
		t.Fatal(err)
	}

	go func() { _ = server.Serve(listener) }()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(grpcapi.AuthorizationMetadataKey, "Bearer "+testSecret))
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(
		dialCtx,
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(grpcapi.MaxWireBytes),
			grpc.MaxCallSendMsgSize(grpcapi.MaxWireBytes),
		),
	)
	if err != nil {
		server.Stop()
		listener.Close()
		daemon.Close()
		t.Fatal(err)
	}

	client := sessiondv1.NewModelConfigClient(conn)
	f := &modelConfigFixture{
		t:      t,
		home:   home,
		daemon: daemon,
		server: server,
		client: client,
		conn:   conn,
		ctx:    ctx,
	}
	t.Cleanup(func() {
		conn.Close()
		server.Stop()
		listener.Close()
		daemon.Close()
	})
	return f
}

func TestModelConfigEndToEndPlanApplyPlanReadback(t *testing.T) {
	f := startModelConfigFixture(t, nil)

	// Step 1: Initial Plan on empty home
	planReq := &sessiondv1.ModelConfigPlanRequest{
		Target: &sessiondv1.ModelConfigTarget{
			Target: &sessiondv1.ModelConfigTarget_Local{Local: &sessiondv1.LocalTarget{}},
		},
	}
	planResp, err := f.client.Plan(f.ctx, planReq)
	if err != nil {
		t.Fatalf("initial Plan failed: %v", err)
	}
	if planResp.TargetSupport != sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED {
		t.Fatalf("expected target support IMPLEMENTED, got %v", planResp.TargetSupport)
	}
	if len(planResp.CurrentFiles) == 0 {
		t.Fatalf("expected current files reported")
	}

	// Step 2: Plan with Proposed Codex config
	newContent := []byte("model = \"gpt-4o\"\n")
	planWithChange, err := f.client.Plan(f.ctx, &sessiondv1.ModelConfigPlanRequest{
		Target: &sessiondv1.ModelConfigTarget{
			Target: &sessiondv1.ModelConfigTarget_Local{Local: &sessiondv1.LocalTarget{}},
		},
		ProposedFiles: []*sessiondv1.ModelConfigFileInput{
			{
				File:    sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG,
				Content: newContent,
			},
		},
	})
	if err != nil {
		t.Fatalf("plan with proposed files failed: %v", err)
	}
	if len(planWithChange.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(planWithChange.Changes))
	}
	chg := planWithChange.Changes[0]
	if chg.File != sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG || !chg.Changed {
		t.Fatalf("expected codex_config changed, got %+v", chg)
	}

	// Step 3: Apply the file with valid meta
	meta := &sessiondv1.CommandMeta{
		OperationId:      "op-apply-1",
		Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
	}
	applyResp, err := f.client.Apply(f.ctx, &sessiondv1.ModelConfigApplyRequest{
		Meta: meta,
		Target: &sessiondv1.ModelConfigTarget{
			Target: &sessiondv1.ModelConfigTarget_Local{Local: &sessiondv1.LocalTarget{}},
		},
		Files: []*sessiondv1.ModelConfigFileInput{
			{
				File:    sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG,
				Content: newContent,
			},
		},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if applyResp.OperationId != "op-apply-1" {
		t.Fatalf("expected op-apply-1, got %q", applyResp.OperationId)
	}
	if len(applyResp.Changed) != 1 || applyResp.Changed[0] != sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG {
		t.Fatalf("expected codex_config changed, got %v", applyResp.Changed)
	}

	// Step 4: Plan Readback verifying content and sha256
	planReadback, err := f.client.Plan(f.ctx, planReq)
	if err != nil {
		t.Fatalf("readback plan failed: %v", err)
	}
	var foundCodex *sessiondv1.ModelConfigFileState
	for _, cf := range planReadback.CurrentFiles {
		if cf.File == sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG {
			foundCodex = cf
			break
		}
	}
	if foundCodex == nil {
		t.Fatalf("codex_config not found in current files")
	}
	if !foundCodex.Exists || string(foundCodex.Content) != string(newContent) {
		t.Fatalf("expected content %q, got exists=%v content=%q", string(newContent), foundCodex.Exists, string(foundCodex.Content))
	}

	// Step 5: Completed retry idempotency via Apply with same OperationID
	retryResp, err := f.client.Apply(f.ctx, &sessiondv1.ModelConfigApplyRequest{
		Meta: meta,
		Target: &sessiondv1.ModelConfigTarget{
			Target: &sessiondv1.ModelConfigTarget_Local{Local: &sessiondv1.LocalTarget{}},
		},
		Files: []*sessiondv1.ModelConfigFileInput{
			{
				File:    sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG,
				Content: newContent,
			},
		},
	})
	if err != nil {
		t.Fatalf("retry apply failed: %v", err)
	}
	if retryResp.OperationId != "op-apply-1" || len(retryResp.Changed) != 1 {
		t.Fatalf("expected idempotent response, got %+v", retryResp)
	}
}

func TestModelConfigBadInputAndHashMismatch(t *testing.T) {
	f := startModelConfigFixture(t, nil)

	// Apply with wrong expected hash and valid Meta
	badExpectedSHA := "1111222233334444555566667777888899990000111122223333444455556666"
	_, err := f.client.Apply(f.ctx, &sessiondv1.ModelConfigApplyRequest{
		Meta: &sessiondv1.CommandMeta{
			OperationId:      "op-bad-hash",
			Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
		},
		Files: []*sessiondv1.ModelConfigFileInput{
			{
				File:           sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG,
				Content:        []byte("model = \"m\"\n"),
				ExpectedSha256: &badExpectedSHA,
			},
		},
	})
	if err == nil {
		t.Fatalf("expected hash mismatch error, got nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v (%v)", st.Code(), err)
	}

	// Bad input: invalid TOML with valid Meta
	_, err = f.client.Apply(f.ctx, &sessiondv1.ModelConfigApplyRequest{
		Meta: &sessiondv1.CommandMeta{
			OperationId:      "op-bad-toml",
			Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
		},
		Files: []*sessiondv1.ModelConfigFileInput{
			{
				File:    sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG,
				Content: []byte("this is not [ valid toml"),
			},
		},
	})
	if err == nil {
		t.Fatalf("expected invalid TOML error, got nil")
	}
}

func TestModelConfigReloadDeferred(t *testing.T) {
	// Dummy adapter with running session -> should defer
	adapter := &dummyReloadAdapter{status: "running"}
	f := startModelConfigFixture(t, adapter)

	resp, err := f.client.Reload(f.ctx, &sessiondv1.ModelConfigReloadRequest{
		Meta: &sessiondv1.CommandMeta{
			OperationId:      "op-reload-1",
			Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
		},
		Agents: []sessiondv1.ReloadAgent{sessiondv1.ReloadAgent_RELOAD_AGENT_CODEX},
	})
	if err != nil {
		t.Fatalf("reload call failed: %v", err)
	}
	if !resp.Deferred {
		t.Fatalf("expected reload to be deferred, got false")
	}
	if len(resp.Pending) != 1 || resp.Pending[0] != sessiondv1.ReloadAgent_RELOAD_AGENT_CODEX {
		t.Fatalf("expected codex in pending reloads, got %v", resp.Pending)
	}
	if adapter.reloads != 0 {
		t.Fatalf("adapter should not have reloaded yet, got %d", adapter.reloads)
	}
}

func TestModelConfigNoSecretsLeakedInRPCError(t *testing.T) {
	f := startModelConfigFixture(t, nil)

	secretVal := "super-secret-api-token-value-12345"
	// Provide secret token with malformed config to cause an error
	_, err := f.client.Apply(f.ctx, &sessiondv1.ModelConfigApplyRequest{
		Meta: &sessiondv1.CommandMeta{
			OperationId:      "op-secret-err",
			Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
		},
		Files: []*sessiondv1.ModelConfigFileInput{
			{
				File:    sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG,
				Content: []byte("invalid toml syntax [[[[\n"),
			},
		},
		EnvironmentValues: []*sessiondv1.SensitiveEnvironmentValue{
			{
				Name:  modelconfig.EnvTokenKey,
				Value: []byte(secretVal),
			},
		},
	})
	if err == nil {
		t.Fatalf("expected failure with malformed content")
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, secretVal) {
		t.Fatalf("secret leaked in error message: %s", errMsg)
	}
}

func TestModelConfigRemoteTargetExecution(t *testing.T) {
	helper := buildRemoteModelHelper(t)
	calls := 0
	runner := realModelHelperRunner(t, helper, t.TempDir(), &calls)
	var invokedCommand string
	var invokedArgs []string
	f := startModelConfigFixture(t, nil, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		invokedCommand = name
		invokedArgs = args
		return runner(ctx, name, args...)
	})

	// 1. WSL target
	planResp, err := f.client.Plan(f.ctx, &sessiondv1.ModelConfigPlanRequest{
		Target: &sessiondv1.ModelConfigTarget{
			Target: &sessiondv1.ModelConfigTarget_Wsl{
				Wsl: &sessiondv1.WslTarget{Distro: "Ubuntu"},
			},
		},
	})
	if err != nil {
		t.Fatalf("remote WSL plan failed: %v", err)
	}
	if planResp.TargetSupport != sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED {
		t.Fatalf("expected IMPLEMENTED, got %v", planResp.TargetSupport)
	}
	if invokedCommand != "wsl.exe" {
		t.Fatalf("expected wsl.exe, got %q", invokedCommand)
	}

	// 2. SSH target
	planRespSSH, err := f.client.Plan(f.ctx, &sessiondv1.ModelConfigPlanRequest{
		Target: &sessiondv1.ModelConfigTarget{
			Target: &sessiondv1.ModelConfigTarget_Ssh{
				Ssh: &sessiondv1.SshTarget{
					Config: &sessiondv1.SshConnectConfig{
						Host: "remote-host",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("remote SSH plan failed: %v", err)
	}
	if planRespSSH.TargetSupport != sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED {
		t.Fatalf("expected IMPLEMENTED, got %v", planRespSSH.TargetSupport)
	}
	if invokedCommand != "ssh" {
		t.Fatalf("expected ssh, got %q", invokedCommand)
	}
	if len(invokedArgs) == 0 || invokedArgs[len(invokedArgs)-1] != "exec 'astrorder-model-helper'" {
		t.Fatalf("expected remote command exec 'astrorder-model-helper', got %v", invokedArgs)
	}
}
