package grpcapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/configuration"
	"astrorder.dev/session-daemon/internal/grpcapi"
	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type grpcReloadLiveAdapter struct{}

func (grpcReloadLiveAdapter) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "grpc-live-1", Status: "running", Payload: map[string]any{"status": "running", "agent_id": "local-codex"}}, nil
}
func (grpcReloadLiveAdapter) Command(_ context.Context, req core.Request) (core.Result, error) {
	return core.Result{SessionID: req.SessionID, Status: "running", Payload: map[string]any{"accepted": true}}, nil
}
func (grpcReloadLiveAdapter) Snapshot() map[string]core.Result {
	return map[string]core.Result{"grpc-live-1": {SessionID: "grpc-live-1", Status: "running"}}
}

func TestGRPCReloadConfigEndToEndAuthValidationAndHotRegister(t *testing.T) {
	f := startFixture(t)
	stateDir := t.TempDir()
	workspace := t.TempDir()
	cfgPath := filepath.Join(stateDir, "session-daemon.json")

	writeCfg := func(cfg configuration.Config) {
		t.Helper()
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	writeCfg(configuration.Config{
		Runtimes: []configuration.Runtime{
			{Type: "pty", Allowed: []string{workspace}},
		},
	})

	// Before attaching reloader, RPC returns FailedPrecondition (not Unimplemented).
	req := &sessiondv1.NativeJson{Schema: grpcapi.ReloadConfigRequestSchema, Utf8Json: []byte(`{}`)}
	resp := new(sessiondv1.NativeJson)
	if err := f.conn.Invoke(f.ctx, grpcapi.ReloadConfigFullMethodName, req, resp); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unconfigured reloader code = %v, want FailedPrecondition (%v)", status.Code(err), err)
	}

	// Unauthenticated request is rejected by the gRPC Bearer interceptor.
	if err := f.conn.Invoke(context.Background(), grpcapi.ReloadConfigFullMethodName, req, resp); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated reload code = %v, want Unauthenticated (%v)", status.Code(err), err)
	}

	// Attach configuration manager and perform initial load.
	mgr := configuration.NewManager(f.daemon, configuration.ManagerOptions{
		ConfigPath:  cfgPath,
		StateDir:    stateDir,
		RefreshPath: func(...string) string { return "" },
		LookPath:    func(string) (string, error) { return "", errors.New("not found") },
		UserHomeDir: func() (string, error) { return workspace, nil },
	})
	if _, err := mgr.ReloadInitial(); err != nil {
		t.Fatal(err)
	}
	f.daemon.SetRuntimeConfigReloader(mgr.Reload)

	// Invalid request payloads are rejected cleanly.
	for _, bad := range [][]byte{
		[]byte(`{"unknown_field":true}`),
		[]byte(`{"extra_path_dirs":["bad\u0000path"]}`),
		[]byte(`not-json`),
	} {
		err := f.conn.Invoke(f.ctx, grpcapi.ReloadConfigFullMethodName, &sessiondv1.NativeJson{Utf8Json: bad}, new(sessiondv1.NativeJson))
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("bad payload %q gave %v, want InvalidArgument", string(bad), err)
		}
	}

	// Confirm GetStatus does not report codex registered yet.
	controlClient := sessiondv1.NewDaemonControlClient(f.conn)
	beforeStatus, err := controlClient.GetStatus(f.ctx, &sessiondv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range beforeStatus.Runtimes {
		if rt.Runtime == sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX && rt.Registered {
			t.Fatal("codex unexpectedly registered before hot reload")
		}
	}

	// Attach a live running session on grok before hot-reloading codex + updated grok.
	if _, err := f.daemon.SwapRuntime("grok", grpcReloadLiveAdapter{}, nil); err != nil {
		t.Fatal(err)
	}
	created := f.daemon.ExecuteSessionControl(context.Background(), map[string]any{
		"action":     "session.create",
		"agent_type": "grok",
		"params":     map[string]any{},
	})
	if created["action"] != "session.create.result" {
		t.Fatalf("create live session failed: %+v", created)
	}

	// Add codex and grok to session-daemon.json and invoke ReloadConfig over gRPC.
	writeCfg(configuration.Config{
		Runtimes: []configuration.Runtime{
			{Type: "pty", Allowed: []string{workspace}},
			{Type: "codex", Executable: "/usr/local/bin/codex", Workspace: workspace, Allowed: []string{workspace}, AgentID: "local-codex"},
			{Type: "grok", Executable: "/usr/local/bin/grok", Workspace: workspace, Allowed: []string{workspace}, AgentID: "local-grok"},
		},
	})

	resp = new(sessiondv1.NativeJson)
	callReq := &sessiondv1.NativeJson{
		Schema:   grpcapi.ReloadConfigRequestSchema,
		Utf8Json: []byte(`{"probe_path":false}`),
	}
	if err := f.conn.Invoke(f.ctx, grpcapi.ReloadConfigFullMethodName, callReq, resp); err != nil {
		t.Fatalf("ReloadConfig RPC failed: %v", err)
	}
	if resp.Schema != grpcapi.ReloadConfigResponseSchema {
		t.Fatalf("unexpected response schema: %q", resp.Schema)
	}
	var decoded protocol.RuntimeReloadResult
	if err := json.Unmarshal(resp.Utf8Json, &decoded); err != nil {
		t.Fatalf("decode response JSON: %v", err)
	}
	if !decoded.Supported || decoded.DaemonID != f.daemon.DaemonID() || decoded.PreservedSessions != 1 {
		t.Fatalf("unexpected reload result: %+v", decoded)
	}
	if !reflect.DeepEqual(decoded.AddedRuntimes, []string{"codex", "grok"}) {
		t.Fatalf("added_runtimes = %v, want [codex grok]", decoded.AddedRuntimes)
	}

	// Verify GetStatus immediately reflects newly registered codex AND preserved running grok session.
	afterStatus, err := controlClient.GetStatus(f.ctx, &sessiondv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	codexRegistered := false
	for _, rt := range afterStatus.Runtimes {
		if rt.Runtime == sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX && rt.Registered {
			codexRegistered = true
		}
	}
	if !codexRegistered {
		t.Fatalf("GetStatus did not report codex registered after hot reload: %+v", afterStatus.Runtimes)
	}
	livePreserved := false
	for _, s := range afterStatus.Sessions {
		if s.GetSession().GetSessionId() == "grpc-live-1" && s.GetStatus() == sessiondv1.SessionStatus_SESSION_STATUS_RUNNING && s.GetRuntimeOwned() {
			livePreserved = true
		}
	}
	if !livePreserved {
		t.Fatalf("running session was not preserved in GetStatus after hot reload: %+v", afterStatus.Sessions)
	}
}
