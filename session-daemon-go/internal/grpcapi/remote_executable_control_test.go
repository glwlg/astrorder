package grpcapi_test

// remote_executable_control_test.go drives the daemon-side remote control
// consumer path end to end over a real loopback TCP gRPC fixture:
//
//   official AttachSession(RuntimeControl=true, CODEX_SSH/GROK_SSH target with
//   typed SshConnectConfig incl. codex_executable/grok_executable + workspace)
//     -> controlTarget -> nativeQueryConnection (proto string fields into
//        params.ssh_settings)
//     -> runtime.NewMultiplexRegistry with the REAL production
//        remote.NewRemote{Codex,Grok}Factory as the ChildFactory
//     -> real Codex/Grok SSH adapter -> synthetic native stdio transport via
//        the adapter's SSH starter seam (in-process; no real network).
//
// The real factory is exercised for admission (executable/workspace/ssh
// validation and settings capture); the returned adapter is a configured
// native adapter whose transport is the in-process synthetic starter, so the
// SSH transport itself never dials a real host. This file must not claim
// real-remote coverage: the genuine OpenSSH dial is only exercised by the
// opt-in remote package tests.
//
// Boundary notes:
//   - The synthetic starter records the exact ssh.CommandSpec, proving the
//     executable/workspace reached the native adapter's launch path.
//   - The production factories construct the real SSH Transport; no network
//     call happens until Start, which the starter seam replaces here.
//   - A separate factory-only probe (calling remote.NewRemoteCodexFactory /
//     NewRemoteGrokFactory directly) asserts the production builders accept
//     the same settings the daemon path produced, without any adapter.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"astrorder.dev/session-daemon/internal/runtime/grok"
	"astrorder.dev/session-daemon/internal/runtime/remote"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// remoteExecSpec captures one ssh.CommandSpec observed by the synthetic
// starter plus the settings the real factory received.
type remoteExecSpec struct {
	executable string
	dir        string
	args       []string
}

// remoteExecRecorder is the synthetic SSH starter seam. It records every
// launch spec and serves a control-only native transport (initialize-only,
// plus the kind-specific read-only catalog method) so the native adapter
// initializes exactly once without any conversation method.
type remoteExecRecorder struct {
	mu      sync.Mutex
	specs   []remoteExecSpec
	methods []string
	// kind selects which read-only catalog method the transport answers.
	kind string
}

func (f *remoteExecRecorder) recordSpec(spec ssh.CommandSpec) {
	f.mu.Lock()
	f.specs = append(f.specs, remoteExecSpec{executable: spec.Executable, dir: spec.Dir, args: append([]string(nil), spec.Args...)})
	f.mu.Unlock()
}

func (f *remoteExecRecorder) recordMethod(method string) {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	f.mu.Unlock()
}

func (f *remoteExecRecorder) specCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.specs)
}

func (f *remoteExecRecorder) firstSpec() remoteExecSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.specs) == 0 {
		return remoteExecSpec{}
	}
	return f.specs[0]
}

func (f *remoteExecRecorder) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, m := range f.methods {
		if m == method {
			total++
		}
	}
	return total
}

func (f *remoteExecRecorder) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

// remoteExecTransport is a synthetic native stdio transport: stdin JSON-RPC
// in, stdout JSON-RPC out. It answers initialize (with kind-specific catalog
// metadata) and the read-only catalog method; every other method is recorded
// and rejected, so any conversation call fails the test loudly.
type remoteExecTransport struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	done    chan struct{}
	once    sync.Once
}

func newRemoteExecTransport() *remoteExecTransport {
	sinR, sinW := io.Pipe()
	soutR, soutW := io.Pipe()
	return &remoteExecTransport{stdinR: sinR, stdinW: sinW, stdoutR: soutR, stdoutW: soutW, done: make(chan struct{})}
}

func (t *remoteExecTransport) Stdin() io.WriteCloser { return t.stdinW }
func (t *remoteExecTransport) Stdout() io.ReadCloser { return t.stdoutR }
func (t *remoteExecTransport) Stderr() []byte        { return nil }
func (t *remoteExecTransport) Wait() (ssh.ExitStatus, error) {
	<-t.done
	return ssh.ExitStatus{ConfirmedRemoteExit: true, ExitCode: 0}, nil
}
func (t *remoteExecTransport) Close() error {
	_ = t.stdinR.Close()
	_ = t.stdinW.Close()
	_ = t.stdoutR.Close()
	_ = t.stdoutW.Close()
	t.once.Do(func() { close(t.done) })
	return nil
}

// remoteExecStarter builds the codex.SSHStarter / grok.SSHStarter closure the
// configured native adapter uses in this fixture. The same recorder instance
// is shared by both adapter kinds; kind selects the catalog payload.
func (f *remoteExecRecorder) starter() func(ctx context.Context, spec ssh.CommandSpec) (*remoteExecTransport, error) {
	return func(ctx context.Context, spec ssh.CommandSpec) (*remoteExecTransport, error) {
		f.recordSpec(spec)
		transport := newRemoteExecTransport()
		go func() {
			decoder := json.NewDecoder(transport.stdinR)
			encoder := json.NewEncoder(transport.stdoutW)
			for {
				var request map[string]any
				if decoder.Decode(&request) != nil {
					// Input closed: retire the transport so Wait observes a
					// confirmed exit.
					_ = transport.Close()
					return
				}
				method, _ := request["method"].(string)
				if method == "" {
					continue
				}
				f.recordMethod(method)
				switch method {
				case "initialized":
					continue
				case "initialize":
					result := map[string]any{"protocolVersion": "1.0"}
					if f.kind == "grok" {
						result["_meta"] = map[string]any{
							"modelState": map[string]any{
								"currentModelId": "grok-4.6",
								"availableModels": []any{
									map[string]any{"modelId": "grok-4.6", "name": "Grok 4.6"},
								},
							},
						}
					} else {
						result["codexHome"] = "/remote-control-home"
					}
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
				case "model/list":
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"data": []any{map[string]any{"id": "gpt-5.2-codex", "displayName": "GPT-5.2 Codex", "provider": "openai", "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "high"}}}}}})
				default:
					// A control-only binding must never reach a conversation
					// method; record and fail fast if it does.
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -32601, "message": "remote control-only transport rejects " + method}})
				}
			}
		}()
		return transport, nil
	}
}

// remoteExecFactoryRecorder wraps the REAL production ChildFactory: it
// forwards to it (proving the production builders accept the daemon-produced
// settings) and, only for the seam, replaces the returned adapter's transport
// starter with the synthetic one. The captured settings are recorded for
// exactness assertions.
type remoteExecFactoryRecorder struct {
	mu       sync.Mutex
	settings []map[string]any
	connIDs  []string
	recorder *remoteExecRecorder
}

func (f *remoteExecFactoryRecorder) record(connID string, settings map[string]any) {
	copied := map[string]any{}
	for k, v := range settings {
		copied[k] = v
	}
	f.mu.Lock()
	f.connIDs = append(f.connIDs, connID)
	f.settings = append(f.settings, copied)
	f.mu.Unlock()
}

func (f *remoteExecFactoryRecorder) firstSettings() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.settings) == 0 {
		return nil
	}
	return f.settings[0]
}

func (f *remoteExecFactoryRecorder) settingsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.settings)
}

// codexControlChildFactory is the ChildFactory given to the real
// core.NewMultiplexRegistry. It calls the REAL remote.NewRemoteCodexFactory
// for admission/capture and then returns a codex adapter whose transport is
// the synthetic starter (the production factory's own SSH transport is not
// dialed in this fixture).
func (f *remoteExecFactoryRecorder) codexControlChildFactory(t *testing.T) core.ChildFactory {
	realFactory := remote.NewRemoteCodexFactory(func(string, string, map[string]any, string) error { return nil })
	return func(connID string, settings map[string]any) (core.Adapter, error) {
		adapter, err := realFactory(connID, settings)
		if err != nil {
			return nil, err
		}
		if adapter == nil {
			return nil, fmt.Errorf("remote codex factory returned a nil adapter")
		}
		f.record(connID, settings)
		// The production factory returned a real codex adapter wired to the
		// real SSH transport; rebuild the same configuration with the
		// synthetic starter so no network is touched. The executable and
		// workspace come from the exact same settings the factory consumed.
		executable, _ := settings["codex_executable"].(string)
		workspace, _ := settings["workspace"].(string)
		if workspace == "" {
			workspace = "/"
		}
		config := codex.Config{
			Executable: executable,
			Workspace:  workspace,
			Allowed:    []string{"/"},
			AgentID:    "ssh-codex-" + connID,
		}
		return codex.NewSSHAdapter(config, nil, func(ctx context.Context, spec ssh.CommandSpec) (codex.SSHSession, error) {
			transport, err := f.recorder.starter()(ctx, spec)
			if err != nil {
				return nil, err
			}
			return transport, nil
		}), nil
	}
}

// grokControlChildFactory mirrors codexControlChildFactory for the real
// remote.NewRemoteGrokFactory.
func (f *remoteExecFactoryRecorder) grokControlChildFactory(t *testing.T) core.ChildFactory {
	realFactory := remote.NewRemoteGrokFactory(func(string, string, map[string]any, string) error { return nil })
	return func(connID string, settings map[string]any) (core.Adapter, error) {
		adapter, err := realFactory(connID, settings)
		if err != nil {
			return nil, err
		}
		if adapter == nil {
			return nil, fmt.Errorf("remote grok factory returned a nil adapter")
		}
		f.record(connID, settings)
		executable, _ := settings["grok_executable"].(string)
		workspace, _ := settings["workspace"].(string)
		if workspace == "" {
			workspace = "/"
		}
		config := grok.Config{
			Executable: executable,
			Workspace:  workspace,
			Allowed:    []string{"/"},
			AgentID:    "ssh-grok-" + connID,
		}
		return grok.NewWithSSHStarter(config, func(ctx context.Context, spec ssh.CommandSpec) (grok.SSHSession, error) {
			transport, err := f.recorder.starter()(ctx, spec)
			if err != nil {
				return nil, err
			}
			return transport, nil
		}), nil
	}
}

// remoteControlAttachRequest builds the official typed attach request used by
// both kinds: runtime_control=true, connection identity, and the exact
// SshConnectConfig carrying the resolved remote executable and workspace.
func remoteControlAttachRequest(sessionID, connID string, kind pb.RuntimeKind, sshConfig *pb.SshConnectConfig) *pb.AttachSessionRequest {
	return &pb.AttachSessionRequest{
		Session: &pb.SessionRef{SessionId: sessionID, ConnectionId: &connID},
		Target: &pb.RuntimeTarget{
			Runtime:      kind,
			ConnectionId: &connID,
			Ssh:          sshConfig,
		},
		RuntimeControl: true,
	}
}

// sshConfigWithExecutable is intentionally omitted: the per-kind builders
// below construct the exact typed SshConnectConfig the App sends.

func codexSSHConfig(workspace, executable string) *pb.SshConnectConfig {
	return &pb.SshConnectConfig{
		Host:            "loopback.invalid",
		Port:            2222,
		Workspace:       &workspace,
		CodexExecutable: &executable,
	}
}

func grokSSHConfig(workspace, executable string) *pb.SshConnectConfig {
	return &pb.SshConnectConfig{
		Host:           "loopback.invalid",
		Port:           2222,
		Workspace:      &workspace,
		GrokExecutable: &executable,
	}
}

// assertRemoteControlAttached checks the receipt's identity and status.
func assertRemoteControlAttached(t *testing.T, receipt *pb.SessionReceipt, sessionID string, status pb.SessionStatus) {
	t.Helper()
	if receipt == nil || !receipt.Attached || receipt.Session.GetSessionId() != sessionID || receipt.Status != status {
		t.Fatalf("remote control attach receipt: %+v", receipt)
	}
}

// assertNoConversationMethods fails if any conversation method was issued.
func assertNoConversationMethods(t *testing.T, recorder *remoteExecRecorder, forbidden []string) {
	t.Helper()
	for _, method := range forbidden {
		if recorder.count(method) != 0 {
			t.Fatalf("remote control attach issued %s: %v", method, recorder.snapshot())
		}
	}
}

// assertRemoteSettingsExact checks the settings the real factory consumed.
func assertRemoteSettingsExact(t *testing.T, settings map[string]any, connID, workspace, executableField, executable string) {
	t.Helper()
	if settings == nil {
		t.Fatalf("real remote factory was never called")
	}
	if settings["host"] != "loopback.invalid" || settings["port"] != 2222 {
		t.Fatalf("real factory settings transport fields = %v", settings)
	}
	if settings["workspace"] != workspace {
		t.Fatalf("real factory settings workspace = %v, want %q", settings["workspace"], workspace)
	}
	if settings[executableField] != executable {
		t.Fatalf("real factory settings %s = %v, want %q", executableField, settings[executableField], executable)
	}
}

// TestRemoteCodexExecutableControlOverOfficialGRPC proves the daemon-side
// remote Codex control consumer path: official TCP gRPC AttachSession with a
// typed CODEX_SSH target carrying codex_executable + workspace flows through
// nativeQueryConnection into a real NewMultiplexRegistry built on the REAL
// remote.NewRemoteCodexFactory, initializes the configured native adapter
// exactly once with the exact executable/workspace captured from the SSH
// CommandSpec, never issues a conversation method, serves the read-only
// model catalog, reports runtime_owned typed status, and releases the exact
// connection through DisconnectRuntime.
func TestRemoteCodexExecutableControlOverOfficialGRPC(t *testing.T) {
	recorder := &remoteExecRecorder{kind: "codex"}
	factory := &remoteExecFactoryRecorder{recorder: recorder}
	registry := core.NewMultiplexRegistry(factory.codexControlChildFactory(t))
	f := startFixture(t)
	f.daemon.RegisterRuntime("codex-ssh", registry)

	const sessionID = "probe-codex-remote-control"
	connID := "saved2"
	const workspace = "/workspace"
	const executable = "/opt/native/codex"
	attach := func() *pb.SessionReceipt {
		t.Helper()
		receipt, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx,
			remoteControlAttachRequest(sessionID, connID, pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, codexSSHConfig(workspace, executable)))
		if err != nil {
			t.Fatalf("AttachSession: %v", err)
		}
		return receipt
	}

	receipt := attach()
	assertRemoteControlAttached(t, receipt, sessionID, pb.SessionStatus_SESSION_STATUS_IDLE)
	if receipt.DaemonId != f.daemon.DaemonID() {
		t.Fatalf("remote control attach daemon epoch: %+v", receipt)
	}

	// The real factory accepted the daemon-produced settings and the native
	// adapter launched the configured executable/workspace exactly once.
	if factory.settingsCount() != 1 {
		t.Fatalf("real remote factory call count = %d, want 1", factory.settingsCount())
	}
	assertRemoteSettingsExact(t, factory.firstSettings(), connID, workspace, "codex_executable", executable)
	if recorder.specCount() != 1 {
		t.Fatalf("native launch count = %d, want 1", recorder.specCount())
	}
	spec := recorder.firstSpec()
	if spec.executable != executable || spec.dir != workspace {
		t.Fatalf("native CommandSpec = %+v, want executable %q dir %q", spec, executable, workspace)
	}
	if recorder.count("initialize") != 1 {
		t.Fatalf("control attach did not initialize exactly once: %v", recorder.snapshot())
	}
	assertNoConversationMethods(t, recorder, []string{"thread/start", "thread/resume", "session/new", "session/load", "turn/start"})

	// The read-only model catalog is served over the live control transport
	// without any mutation.
	models, err := pb.NewRuntimeQueryClient(f.conn).ListModels(f.ctx, &pb.ListModelsRequest{Session: &pb.SessionRef{SessionId: sessionID, ConnectionId: &connID}})
	if err != nil || len(models.Items) != 1 || models.Items[0].Model != "gpt-5.2-codex" {
		t.Fatalf("control model catalog: %+v %v", models, err)
	}

	// The authoritative status reports the live binding with the typed kind,
	// connection identity and runtime_owned verdict.
	statusResponse, err := pb.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &pb.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	session := sessionByID(statusResponse, sessionID)
	if session == nil || session.RuntimeOwned == nil || !session.GetRuntimeOwned() {
		t.Fatalf("control session not live-owned: %+v", statusResponse.Sessions)
	}
	if session.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH {
		t.Fatalf("control session runtime kind = %v, want CODEX_SSH", session.GetRuntime())
	}
	if session.GetSession().GetConnectionId() != connID || session.GetSession().GetAgentId() != "ssh-codex-"+connID {
		t.Fatalf("control session identity = %+v", session.GetSession())
	}

	// A repeated attach over the live transport must reuse it: no second
	// native process, no second initialize.
	if repeated := attach(); repeated.Session.GetSessionId() != sessionID {
		t.Fatalf("repeat remote control attach: %+v", repeated)
	}
	if recorder.specCount() != 1 || recorder.count("initialize") != 1 {
		t.Fatalf("repeat attach started another native process: specs=%d methods=%v", recorder.specCount(), recorder.snapshot())
	}

	// Official runtime disconnect releases exactly this connection's binding.
	disconnect, err := pb.NewRuntimeQueryClient(f.conn).DisconnectRuntime(f.ctx, &pb.DisconnectRuntimeRequest{
		Meta: controlMeta("disconnect-remote-codex-control"),
		Target: &pb.RuntimeTarget{
			Runtime:      pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH,
			ConnectionId: ptr(connID),
			Ssh:          codexSSHConfig(workspace, executable),
		},
	})
	if err != nil || !disconnect.Disconnected {
		t.Fatalf("remote control disconnect: %+v %v", disconnect, err)
	}
	released := false
	for _, id := range disconnect.ReleasedSessionIds {
		if id == sessionID {
			released = true
		}
	}
	if !released {
		t.Fatalf("remote control disconnect did not release the binding: %+v", disconnect)
	}

	// A fresh attach after disconnect re-initializes cleanly.
	deadline := time.Now().Add(5 * time.Second)
	for {
		reconnected := attach()
		if reconnected.Session.GetSessionId() == sessionID && reconnected.Status == pb.SessionStatus_SESSION_STATUS_IDLE {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconnect after disconnect failed: %+v", reconnected)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if recorder.count("initialize") != 2 {
		t.Fatalf("reconnect did not initialize a fresh transport: %v", recorder.snapshot())
	}
	assertNoConversationMethods(t, recorder, []string{"thread/start", "thread/resume", "session/new", "session/load", "turn/start"})
}

// TestRemoteGrokExecutableControlOverOfficialGRPC mirrors the Codex test for
// GROK_SSH: the typed grok_executable + workspace reach the real
// remote.NewRemoteGrokFactory, the configured adapter initializes once over
// the synthetic transport, the read-only catalog is served, the typed
// live-ownership status is exact, and disconnect releases only this binding.
func TestRemoteGrokExecutableControlOverOfficialGRPC(t *testing.T) {
	recorder := &remoteExecRecorder{kind: "grok"}
	factory := &remoteExecFactoryRecorder{recorder: recorder}
	registry := core.NewMultiplexRegistry(factory.grokControlChildFactory(t))
	f := startFixture(t)
	f.daemon.RegisterRuntime("grok-ssh", registry)

	const sessionID = "probe-grok-remote-control"
	connID := "saved2"
	const workspace = "/srv/private-control"
	const executable = "/opt/native/grok"
	attach := func() *pb.SessionReceipt {
		t.Helper()
		receipt, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx,
			remoteControlAttachRequest(sessionID, connID, pb.RuntimeKind_RUNTIME_KIND_GROK_SSH, grokSSHConfig(workspace, executable)))
		if err != nil {
			t.Fatalf("AttachSession: %v", err)
		}
		return receipt
	}

	receipt := attach()
	assertRemoteControlAttached(t, receipt, sessionID, pb.SessionStatus_SESSION_STATUS_IDLE)
	if factory.settingsCount() != 1 {
		t.Fatalf("real remote factory call count = %d, want 1", factory.settingsCount())
	}
	assertRemoteSettingsExact(t, factory.firstSettings(), connID, workspace, "grok_executable", executable)
	if recorder.specCount() != 1 {
		t.Fatalf("native launch count = %d, want 1", recorder.specCount())
	}
	spec := recorder.firstSpec()
	if spec.executable != executable || spec.dir != workspace {
		t.Fatalf("native CommandSpec = %+v, want executable %q dir %q", spec, executable, workspace)
	}
	if recorder.count("initialize") != 1 {
		t.Fatalf("control attach did not initialize exactly once: %v", recorder.snapshot())
	}
	assertNoConversationMethods(t, recorder, []string{"session/new", "session/load", "session/prompt"})

	models, err := pb.NewRuntimeQueryClient(f.conn).ListModels(f.ctx, &pb.ListModelsRequest{Session: &pb.SessionRef{SessionId: sessionID, ConnectionId: &connID}})
	if err != nil || len(models.Items) != 1 || models.Items[0].Model != "grok-4.6" {
		t.Fatalf("control model catalog: %+v %v", models, err)
	}

	statusResponse, err := pb.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &pb.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	session := sessionByID(statusResponse, sessionID)
	if session == nil || session.RuntimeOwned == nil || !session.GetRuntimeOwned() {
		t.Fatalf("control session not live-owned: %+v", statusResponse.Sessions)
	}
	if session.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_GROK_SSH {
		t.Fatalf("control session runtime kind = %v, want GROK_SSH", session.GetRuntime())
	}
	if session.GetSession().GetConnectionId() != connID || session.GetSession().GetAgentId() != "ssh-grok-"+connID {
		t.Fatalf("control session identity = %+v", session.GetSession())
	}

	// A repeated attach over the live transport must reuse it.
	if repeated := attach(); repeated.Session.GetSessionId() != sessionID {
		t.Fatalf("repeat remote control attach: %+v", repeated)
	}
	if recorder.specCount() != 1 || recorder.count("initialize") != 1 {
		t.Fatalf("repeat attach started another native process: specs=%d methods=%v", recorder.specCount(), recorder.snapshot())
	}

	disconnect, err := pb.NewRuntimeQueryClient(f.conn).DisconnectRuntime(f.ctx, &pb.DisconnectRuntimeRequest{
		Meta: controlMeta("disconnect-remote-grok-control"),
		Target: &pb.RuntimeTarget{
			Runtime:      pb.RuntimeKind_RUNTIME_KIND_GROK_SSH,
			ConnectionId: ptr(connID),
			Ssh:          grokSSHConfig(workspace, executable),
		},
	})
	if err != nil || !disconnect.Disconnected {
		t.Fatalf("remote control disconnect: %+v %v", disconnect, err)
	}
	released := false
	for _, id := range disconnect.ReleasedSessionIds {
		if id == sessionID {
			released = true
		}
	}
	if !released {
		t.Fatalf("remote control disconnect did not release the binding: %+v", disconnect)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		reconnected := attach()
		if reconnected.Session.GetSessionId() == sessionID && reconnected.Status == pb.SessionStatus_SESSION_STATUS_IDLE {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconnect after disconnect failed: %+v", reconnected)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if recorder.count("initialize") != 2 {
		t.Fatalf("reconnect did not initialize a fresh transport: %v", recorder.snapshot())
	}
	assertNoConversationMethods(t, recorder, []string{"session/new", "session/load", "session/prompt"})
}

// TestRemoteExecutableControlPreservesOtherConnectionBinding proves a scoped
// DisconnectRuntime for one connection does not release a different
// connection's live binding on the same multiplex registry.
func TestRemoteExecutableControlPreservesOtherConnectionBinding(t *testing.T) {
	recorder := &remoteExecRecorder{kind: "codex"}
	factory := &remoteExecFactoryRecorder{recorder: recorder}
	registry := core.NewMultiplexRegistry(factory.codexControlChildFactory(t))
	f := startFixture(t)
	f.daemon.RegisterRuntime("codex-ssh", registry)

	attachFor := func(sessionID, connID string) {
		t.Helper()
		receipt, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx,
			remoteControlAttachRequest(sessionID, connID, pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, codexSSHConfig("/workspace", "/opt/native/codex")))
		if err != nil {
			t.Fatalf("AttachSession %s: %v", connID, err)
		}
		if !receipt.Attached {
			t.Fatalf("attach %s not confirmed: %+v", connID, receipt)
		}
	}
	attachFor("probe-codex-saved2", "saved2")
	attachFor("probe-codex-other", "other")
	if recorder.specCount() != 2 {
		t.Fatalf("expected one native launch per connection, got %d", recorder.specCount())
	}

	disconnect, err := pb.NewRuntimeQueryClient(f.conn).DisconnectRuntime(f.ctx, &pb.DisconnectRuntimeRequest{
		Meta: controlMeta("disconnect-remote-codex-scoped"),
		Target: &pb.RuntimeTarget{
			Runtime:      pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH,
			ConnectionId: ptr("saved2"),
			Ssh:          codexSSHConfig("/workspace", "/opt/native/codex"),
		},
	})
	if err != nil || !disconnect.Disconnected {
		t.Fatalf("scoped disconnect: %+v %v", disconnect, err)
	}
	statusResponse, err := pb.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &pb.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	released := sessionByID(statusResponse, "probe-codex-saved2")
	if released == nil || released.RuntimeOwned == nil || released.GetRuntimeOwned() {
		t.Fatalf("released session still claims live ownership: %+v", released)
	}
	live := sessionByID(statusResponse, "probe-codex-other")
	if live == nil || live.RuntimeOwned == nil || !live.GetRuntimeOwned() {
		t.Fatalf("other connection's binding was lost: %+v", statusResponse.Sessions)
	}
	if live.GetSession().GetConnectionId() != "other" {
		t.Fatalf("other connection's identity changed: %+v", live.GetSession())
	}
}

// TestRemoteExecutableControlTransportOptionsRemainUnimplemented pins the
// explicit Unimplemented boundary: the daemon consumer path must keep
// refusing ssh_config_alias / options / ssh_executable with codes.Unimplemented
// instead of silently producing a partial mapping.
func TestRemoteExecutableControlTransportOptionsRemainUnimplemented(t *testing.T) {
	recorder := &remoteExecRecorder{kind: "codex"}
	factory := &remoteExecFactoryRecorder{recorder: recorder}
	registry := core.NewMultiplexRegistry(factory.codexControlChildFactory(t))
	f := startFixture(t)
	f.daemon.RegisterRuntime("codex-ssh", registry)

	alias := "build-prod"
	sshExecutable := "/usr/local/bin/ssh"
	options := &pb.SshOptions{BatchMode: ptr(true)}
	cases := []struct {
		name   string
		config *pb.SshConnectConfig
	}{
		{
			name: "ssh_config_alias",
			config: &pb.SshConnectConfig{
				Host:            "loopback.invalid",
				Workspace:       ptr("/workspace"),
				CodexExecutable: ptr("/opt/native/codex"),
				SshConfigAlias:  &alias,
			},
		},
		{
			name: "ssh_executable",
			config: &pb.SshConnectConfig{
				Host:            "loopback.invalid",
				Workspace:       ptr("/workspace"),
				CodexExecutable: ptr("/opt/native/codex"),
				SshExecutable:   &sshExecutable,
			},
		},
		{
			name: "options",
			config: &pb.SshConnectConfig{
				Host:            "loopback.invalid",
				Workspace:       ptr("/workspace"),
				CodexExecutable: ptr("/opt/native/codex"),
				Options:         options,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			connID := "unsupported"
			_, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx,
				remoteControlAttachRequest("probe-codex-unsupported", connID, pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH, tc.config))
			if status.Code(err) != codes.Unimplemented {
				t.Fatalf("unsupported transport option %s = %v, want Unimplemented: %v", tc.name, status.Code(err), err)
			}
		})
	}
	if factory.settingsCount() != 0 || recorder.specCount() != 0 {
		t.Fatalf("unsupported transport options reached the factory: settings=%d specs=%d", factory.settingsCount(), recorder.specCount())
	}
}

// TestRemoteFactoriesAcceptDaemonSettingsShape proves the REAL production
// factories accept exactly the settings shape the daemon consumer path
// produces (host/port/workspace/executable strings), independent of the gRPC
// fixture. This is a factory admission test only: no transport is started.
func TestRemoteFactoriesAcceptDaemonSettingsShape(t *testing.T) {
	codexSettings := map[string]any{
		"host":             "loopback.invalid",
		"port":             2222,
		"workspace":        "/workspace",
		"codex_executable": "/opt/native/codex",
	}
	adapter, err := remote.NewRemoteCodexFactory(func(string, string, map[string]any, string) error { return nil })("saved2", codexSettings)
	if err != nil || adapter == nil {
		t.Fatalf("remote codex factory rejected daemon settings shape: %v", err)
	}
	grokSettings := map[string]any{
		"host":            "loopback.invalid",
		"port":            2222,
		"workspace":       "/srv/private-control",
		"grok_executable": "/opt/native/grok",
	}
	adapter, err = remote.NewRemoteGrokFactory(func(string, string, map[string]any, string) error { return nil })("saved2", grokSettings)
	if err != nil || adapter == nil {
		t.Fatalf("remote grok factory rejected daemon settings shape: %v", err)
	}
}
