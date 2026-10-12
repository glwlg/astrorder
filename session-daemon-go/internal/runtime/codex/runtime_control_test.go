package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// controlFixture is a synthetic codex app-server stdio transport. It answers
// only the real protocol methods a control-only attach may use (initialize,
// initialized notification, catalog reads) and records every method it saw.
// Any thread/start, thread/resume, session/new, or turn/start is recorded as
// a violation the tests can assert against.
type controlFixture struct {
	mu      sync.Mutex
	methods []string
	starts  atomic.Int32
	threads atomic.Int32
	turns   atomic.Int32
	// ready is closed once the serve loop has recorded the native
	// initialized notification. Spawn returns before that asynchronous
	// notification is necessarily consumed, so tests taking a baseline
	// snapshot must wait for this barrier instead of assuming timing.
	ready     chan struct{}
	readyOnce sync.Once
}

func (f *controlFixture) record(method string) {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	f.mu.Unlock()
	if method == "initialized" {
		f.readyOnce.Do(func() {
			if f.ready != nil {
				close(f.ready)
			}
		})
	}
	switch method {
	case "thread/start", "thread/resume", "session/new", "session/load":
		f.threads.Add(1)
	case "turn/start":
		f.turns.Add(1)
	}
}

func (f *controlFixture) seen(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.methods {
		if m == method {
			return true
		}
	}
	return false
}

func (f *controlFixture) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func (f *controlFixture) count(method string) int {
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

// waitReady blocks until the serve loop has recorded the native initialized
// notification, or fails with the context error. A baseline snapshot taken
// after this barrier can only grow from requests the test itself triggers.
func (f *controlFixture) waitReady(ctx context.Context) error {
	select {
	case <-f.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// serve answers decoded JSON-RPC requests on a synthetic transport until the
// stream ends. Control-only attaches may only reach initialize/initialized
// and read-only catalog methods; anything else is recorded and answered with
// a protocol error so the caller fails fast instead of hanging.
func (f *controlFixture) serve(decoder *json.Decoder, encoder *json.Encoder) {
	for {
		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}
		method, _ := request["method"].(string)
		if method == "" {
			continue
		}
		f.record(method)
		switch method {
		case "initialized":
			continue
		case "initialize":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"protocolVersion": "1.0", "codexHome": "/control-home"}})
		case "model/list":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"data": []any{map[string]any{"id": "gpt-5.2-codex", "displayName": "GPT-5.2 Codex", "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "high"}}}}}})
		case "thread/list", "account/read", "hooks/list", "skills/list":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"data": []any{}}})
		default:
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -32601, "message": "control-only transport rejects " + method}})
		}
	}
}

func newControlFixture() (*controlFixture, SSHStarter) {
	fixture := &controlFixture{ready: make(chan struct{})}
	starter := func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
		fake := newFakeSSHSession()
		go fixture.serve(json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW))
		return fake, nil
	}
	return fixture, starter
}

func controlRequest(sessionID string) core.Request {
	return core.Request{
		AgentType: "codex",
		SessionID: sessionID,
		Fields: map[string]any{
			"params": map[string]any{"runtime_control": true},
		},
	}
}

// TestControlOnlySpawnInitializesWithoutThreads is the RED acceptance for the
// control-only channel: a runtime_control spawn must initialize the app-server
// transport and bind the exact control identity, never issuing thread/start,
// thread/resume, session/new, or turn/start.
func TestControlOnlySpawnInitializesWithoutThreads(t *testing.T) {
	fixture, starter := newControlFixture()
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "codex-control-agent"}, nil, starter)
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := adapter.Spawn(ctx, controlRequest("probe-codex-local"))
	if err != nil {
		t.Fatalf("control-only spawn failed: %v", err)
	}
	if result.SessionID != "probe-codex-local" || result.Status != "idle" {
		t.Fatalf("control-only spawn identity/status: %+v", result)
	}
	if result.Payload["runtime_control"] != true || result.Payload["agent_id"] != "codex-control-agent" {
		t.Fatalf("control-only spawn payload: %#v", result.Payload)
	}
	if fixture.threads.Load() != 0 || fixture.turns.Load() != 0 {
		t.Fatalf("control-only spawn created a native conversation: methods=%v", fixture.snapshot())
	}
	if !fixture.seen("initialize") {
		t.Fatalf("control-only spawn never initialized transport: methods=%v", fixture.snapshot())
	}
	for _, forbidden := range []string{"thread/start", "thread/resume", "session/new", "session/load", "turn/start"} {
		if fixture.seen(forbidden) {
			t.Fatalf("control-only spawn issued %s: methods=%v", forbidden, fixture.snapshot())
		}
	}
	// The control transport is live: a repeated spawn with the same identity
	// must reuse it instead of starting another native process.
	if _, err := adapter.Spawn(ctx, controlRequest("probe-codex-local")); err != nil {
		t.Fatalf("repeat control-only spawn failed: %v", err)
	}
	if fixture.count("initialize") != 1 {
		t.Fatalf("repeat control-only spawn started another transport: methods=%v", fixture.snapshot())
	}
}

// TestControlOnlySpawnSurfacesTransportEOF ensures a dead native transport is
// reported as error in the snapshot instead of silently remaining idle, and
// that a fresh control-only initialization is possible afterwards.
func TestControlOnlySpawnSurfacesTransportEOF(t *testing.T) {
	var mu sync.Mutex
	transports := []*fakeSSHSession{}
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "codex-control-agent"}, nil,
		func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
			fake := newFakeSSHSession()
			mu.Lock()
			transports = append(transports, fake)
			mu.Unlock()
			go func() {
				decoder := json.NewDecoder(fake.stdinR)
				encoder := json.NewEncoder(fake.stdoutW)
				for {
					var request map[string]any
					if decoder.Decode(&request) != nil {
						return
					}
					if request["method"] == "initialized" {
						continue
					}
					_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"protocolVersion": "1.0"}})
				}
			}()
			return fake, nil
		})
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := adapter.Spawn(ctx, controlRequest("probe-codex-eof")); err != nil {
		t.Fatalf("control-only spawn failed: %v", err)
	}
	mu.Lock()
	first := transports[0]
	mu.Unlock()
	_ = first.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot := adapter.Snapshot()
		if entry, ok := snapshot["probe-codex-eof"]; ok && entry.Status == "error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("control transport EOF was not surfaced: %+v", adapter.Snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Recovery: the dead transport is retired and a fresh control-only
	// initialization is allowed, still without touching a native thread.
	result, err := adapter.Spawn(ctx, controlRequest("probe-codex-eof"))
	if err != nil || result.SessionID != "probe-codex-eof" || result.Payload["runtime_control"] != true {
		t.Fatalf("control-only re-initialization after EOF failed: %+v %v", result, err)
	}
	mu.Lock()
	fresh := len(transports)
	mu.Unlock()
	if fresh != 2 {
		t.Fatalf("recovery did not start exactly one fresh transport: %d", fresh)
	}
}

// TestControlOnlySpawnHonorsContextCancellation ensures a cancelled
// initialization fails loudly and leaves no owned session behind.
func TestControlOnlySpawnHonorsContextCancellation(t *testing.T) {
	drained := make(chan struct{})
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "codex-control-agent"}, nil,
		func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
			fake := newFakeSSHSession()
			go func() {
				decoder := json.NewDecoder(fake.stdinR)
				for {
					var request map[string]any
					if decoder.Decode(&request) != nil {
						close(drained)
						return
					}
					// Deliberately never answer: initialization must fail on
					// context cancellation, not hang or claim success.
				}
			}()
			return fake, nil
		})
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := adapter.Spawn(ctx, controlRequest("probe-codex-cancel")); err == nil {
		t.Fatal("cancelled control initialization reported success")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancelled control initialization did not return promptly")
	}
	if _, ok := adapter.Snapshot()["probe-codex-cancel"]; ok {
		t.Fatalf("cancelled control initialization left an owned session: %+v", adapter.Snapshot())
	}
	<-drained // the failed transport was closed, not leaked
}

// TestControlOnlyShutdownReleasesWithoutClosingAdapter covers safe reconnect:
// a graceful Shutdown releases the confirmed idle control transport but keeps
// the adapter registered for a later control attach, unlike Close.
func TestControlOnlyShutdownReleasesWithoutClosingAdapter(t *testing.T) {
	_, starter := newControlFixture()
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "codex-control-agent"}, nil, starter)
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := adapter.Spawn(ctx, controlRequest("probe-codex-shutdown")); err != nil {
		t.Fatalf("control-only spawn failed: %v", err)
	}
	shutdown, ok := any(adapter).(core.RuntimeDisconnector)
	if !ok {
		t.Fatal("Codex adapter does not implement runtime.disconnect shutdown")
	}
	released, err := shutdown.Shutdown(ctx)
	if err != nil || len(released) != 1 || released[0] != "probe-codex-shutdown" {
		t.Fatalf("shutdown: released=%v err=%v", released, err)
	}
	if _, ok := adapter.Snapshot()["probe-codex-shutdown"]; ok {
		t.Fatalf("shutdown left the control session owned: %+v", adapter.Snapshot())
	}
	result, err := adapter.Spawn(ctx, controlRequest("probe-codex-shutdown"))
	if err != nil || result.SessionID != "probe-codex-shutdown" {
		t.Fatalf("reconnect after shutdown failed: %+v %v", result, err)
	}
}

// TestControlOnlySpawnViaRegistryBindsAndReuses verifies the full
// adapter+Registry path: control-only spawn binds through Registry.Spawn,
// Snapshot reflects live status, and a repeated spawn reuses the transport.
func TestControlOnlySpawnViaRegistryBindsAndReuses(t *testing.T) {
	fixture, starter := newControlFixture()
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "codex-control-agent"}, nil, starter)
	registry := core.NewRegistry()
	registry.Register("codex", adapter)
	defer registry.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := core.Request{AgentType: "codex", SessionID: "probe-codex-registry", Fields: map[string]any{"params": map[string]any{"runtime_control": true}}}
	result, err := registry.Spawn(ctx, request)
	if err != nil || result.SessionID != "probe-codex-registry" {
		t.Fatalf("registry control spawn: %+v %v", result, err)
	}
	states := registry.Sessions()
	if state, ok := states["probe-codex-registry"]; !ok || state.Status != "idle" {
		t.Fatalf("registry snapshot missing control session: %+v", states)
	}
	if _, err := registry.Spawn(ctx, request); err != nil {
		t.Fatalf("registry repeat control spawn: %v", err)
	}
	if fixture.threads.Load() != 0 || fixture.turns.Load() != 0 {
		t.Fatalf("registry control spawn created a native conversation: methods=%v", fixture.snapshot())
	}
	if fixture.count("initialize") != 1 {
		t.Fatalf("registry repeat control spawn started another transport: methods=%v", fixture.snapshot())
	}
}

// TestControlOnlyCommandServesModelCatalog verifies the control binding's
// closed read-only catalog set: session.models runs the real model/list over
// the live control transport, keeps the exact control identity and live
// status, and a repeated read reuses the same transport.
func TestControlOnlyCommandServesModelCatalog(t *testing.T) {
	fixture, starter := newControlFixture()
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "codex-control-agent"}, nil, starter)
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := adapter.Spawn(ctx, controlRequest("probe-codex-catalog")); err != nil {
		t.Fatalf("control-only spawn failed: %v", err)
	}
	read := func() core.Result {
		t.Helper()
		result, err := adapter.Command(ctx, core.Request{AgentType: "codex", SessionID: "probe-codex-catalog", Action: "session.models", Fields: map[string]any{}})
		if err != nil {
			t.Fatalf("control model catalog read failed: %v", err)
		}
		return result
	}
	result := read()
	if result.SessionID != "probe-codex-catalog" || result.Status != "idle" {
		t.Fatalf("control catalog identity/status: %+v", result)
	}
	if result.Payload["runtime_control"] != true || result.Payload["agent_id"] != "codex-control-agent" {
		t.Fatalf("control catalog payload metadata: %#v", result.Payload)
	}
	items, ok := result.Payload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("control catalog items: %#v", result.Payload["items"])
	}
	if row, ok := items[0].(map[string]any); !ok || row["id"] != "gpt-5.2-codex" {
		t.Fatalf("control catalog row: %#v", items[0])
	}
	if fixture.count("model/list") != 1 {
		t.Fatalf("control catalog did not reach native model/list: %v", fixture.snapshot())
	}
	// Reuse: a repeated read stays on the same live transport.
	read()
	if fixture.count("model/list") != 2 || fixture.count("initialize") != 1 {
		t.Fatalf("control catalog reuse started another transport: %v", fixture.snapshot())
	}
	if fixture.threads.Load() != 0 || fixture.turns.Load() != 0 {
		t.Fatalf("control catalog created a native conversation: %v", fixture.snapshot())
	}
}

// TestControlOnlyCommandRejectsNativeSessionActions ensures a control binding
// refuses every native-session action explicitly with zero native requests
// and zero new threads, and stays healthy afterwards.
func TestControlOnlyCommandRejectsNativeSessionActions(t *testing.T) {
	fixture, starter := newControlFixture()
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "codex-control-agent"}, nil, starter)
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := adapter.Spawn(ctx, controlRequest("probe-codex-reject")); err != nil {
		t.Fatalf("control-only spawn failed: %v", err)
	}
	// Spawn returns after initialize, but the native initialized notification
	// is consumed asynchronously by the serve loop. Wait for the explicit
	// barrier before taking the baseline so the legitimate notification can
	// never be misread as a request triggered by the rejected actions.
	if err := fixture.waitReady(ctx); err != nil {
		t.Fatalf("control fixture never recorded the initialized notification: %v (methods=%v)", err, fixture.snapshot())
	}
	before := fixture.snapshot()
	for _, action := range []string{"session.send", "session.steer", "session.interrupt", "session.compact", "session.review", "session.approve", "session.settings", "session.rename", "session.history_page", "session.delete"} {
		if _, err := adapter.Command(ctx, core.Request{AgentType: "codex", SessionID: "probe-codex-reject", Action: action, Fields: map[string]any{}}); err == nil {
			t.Fatalf("control binding accepted native-session action %s", action)
		}
	}
	after := fixture.snapshot()
	if len(after) != len(before) {
		t.Fatalf("rejected control actions issued native requests: %v", after)
	}
	if entry, ok := adapter.Snapshot()["probe-codex-reject"]; !ok || entry.Status != "idle" {
		t.Fatalf("control binding status after rejections: %+v", adapter.Snapshot())
	}
	if fixture.threads.Load() != 0 || fixture.turns.Load() != 0 {
		t.Fatalf("rejected control actions created a native conversation: %v", after)
	}
}

// TestControlOnlyCommandHonorsPreCancelledContext ensures pre-cancelled
// control reads and spawns fail explicitly without native requests and
// without leaving an owned session.
func TestControlOnlyCommandHonorsPreCancelledContext(t *testing.T) {
	fixture, starter := newControlFixture()
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}, AgentID: "codex-control-agent"}, nil, starter)
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := adapter.Spawn(ctx, controlRequest("probe-codex-precancel")); err != nil {
		t.Fatalf("control-only spawn failed: %v", err)
	}
	cancelled, cancelRead := context.WithCancel(context.Background())
	cancelRead()
	if _, err := adapter.Spawn(cancelled, controlRequest("probe-codex-precancel-new")); err == nil {
		t.Fatal("pre-cancelled control spawn reported success")
	}
	if _, ok := adapter.Snapshot()["probe-codex-precancel-new"]; ok {
		t.Fatalf("pre-cancelled control spawn left an owned session: %+v", adapter.Snapshot())
	}
	if _, err := adapter.Spawn(cancelled, controlRequest("probe-codex-precancel")); err == nil {
		t.Fatal("pre-cancelled control reuse reported success")
	}
	if _, err := adapter.Command(cancelled, core.Request{AgentType: "codex", SessionID: "probe-codex-precancel", Action: "session.models", Fields: map[string]any{}}); err == nil {
		t.Fatal("pre-cancelled control read reported success")
	}
	if fixture.count("model/list") != 0 {
		t.Fatalf("pre-cancelled control read reached the native transport: %v", fixture.snapshot())
	}
	// The live transport still serves a real read afterwards.
	result, err := adapter.Command(ctx, core.Request{AgentType: "codex", SessionID: "probe-codex-precancel", Action: "session.models", Fields: map[string]any{}})
	if err != nil || result.Status != "idle" {
		t.Fatalf("live control read after cancelled one: %+v %v", result, err)
	}
	if fixture.count("model/list") != 1 {
		t.Fatalf("live control read did not reach native model/list: %v", fixture.snapshot())
	}
}
