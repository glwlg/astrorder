package grok_test

// runtime_control_test.go exercises the Grok control-only attach path
// (runtime_control=true): initialize the ACP transport exactly once, bind the
// control identity, serve only metadata reads, and never issue session/new,
// session/load, session/prompt, or any other native conversation method.
//
// The fixture is a synthetic ACP stdio child driven through the real adapter;
// it appends every native method it sees to GROK_ACP_CALL_LOG so tests can
// assert the exact method set.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/grok"
)

const controlSessionID = "probe-grok-local"

// TestFakeACP_RuntimeControlInitializeOnly is both the fixture host and the
// RED/GREEN acceptance for the control-only attach path.
func TestFakeACP_RuntimeControlInitializeOnly(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_runtime_control" {
		runFakeACPRuntimeControl(false)
		return
	}

	root := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp-calls.log")
	os.Setenv("GROK_ACP_CALL_LOG", logPath)
	defer os.Unsetenv("GROK_ACP_CALL_LOG")

	var emitMu sync.Mutex
	var ownerPID int
	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_RuntimeControlInitializeOnly"},
		Workspace:  root,
		Allowed:    []string{root},
		AgentID:    controlSessionID,
		AgentName:  "Grok Build",
		Emit: func(sessionID, event string, payload map[string]any, status string) error {
			emitMu.Lock()
			if event == "runtime.owner" {
				if pid, ok := payload["owner_pid"].(int); ok {
					ownerPID = pid
				}
			}
			emitMu.Unlock()
			return nil
		},
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_runtime_control")
	defer os.Unsetenv("TEST_MODE")

	spawned := spawnControl(t, adapter, root)
	if spawned.SessionID != controlSessionID || spawned.Status != "idle" {
		t.Fatalf("unexpected control spawn result: %+v", spawned)
	}
	if spawned.Payload["runtime_control"] != true || spawned.Payload["agent_id"] != controlSessionID || spawned.Payload["control_state"] != "owned" {
		t.Fatalf("control spawn did not report exact identity: %+v", spawned.Payload)
	}

	emitMu.Lock()
	pid := ownerPID
	emitMu.Unlock()
	if pid == 0 || !process.Alive(pid) {
		t.Fatalf("control binding did not publish a live owner pid: %d", pid)
	}

	snap := adapter.Snapshot()
	entry, ok := snap[controlSessionID]
	if !ok || entry.Status != "idle" {
		t.Fatalf("control snapshot missing or not idle: %+v", snap)
	}
	if entry.Payload["runtime_control"] != true || entry.Payload["agent_id"] != controlSessionID {
		t.Fatalf("control snapshot lost identity: %+v", entry.Payload)
	}

	calls := readCalls(t, logPath)
	if countCalls(calls, "initialize") != 1 {
		t.Fatalf("control attach did not initialize exactly once: %v", calls)
	}
	assertNoBusinessCalls(t, calls)

	// Business commands are refused with zero native requests; the control ID
	// can never be used as a native session.
	for _, action := range []string{"session.send", "session.settings", "session.rename", "session.history_page", "session.delete", "session.model.set", "session.interrupt", "session.approve"} {
		if _, err := adapter.Command(context.Background(), core.Request{SessionID: controlSessionID, Action: action, Fields: map[string]any{"prompt": "hello", "command_id": "cmd-1"}}); err == nil {
			t.Fatalf("control session accepted business action %s", action)
		}
	}
	assertNoBusinessCalls(t, readCalls(t, logPath))

	// The model catalog is served from the initialize metadata with zero
	// native requests.
	models, err := adapter.Command(context.Background(), core.Request{SessionID: controlSessionID, Action: "session.models"})
	if err != nil {
		t.Fatalf("control model catalog read failed: %v", err)
	}
	items, _ := models.Payload["items"].([]map[string]any)
	if len(items) != 1 || items[0]["model"] != "grok-4.6" {
		t.Fatalf("control model catalog is wrong: %+v", models.Payload)
	}
	if models.Payload["runtime_control"] != true {
		t.Fatalf("control model catalog lost identity: %+v", models.Payload)
	}
	assertNoBusinessCalls(t, readCalls(t, logPath))

	// A repeated control attach reuses the live transport without starting a
	// second native process.
	repeated := spawnControl(t, adapter, root)
	if repeated.Payload["runtime_control"] != true || repeated.Payload["agent_id"] != controlSessionID {
		t.Fatalf("repeated control attach lost identity: %+v", repeated.Payload)
	}
	for _, result := range []core.Result{spawned, entry, models, repeated} {
		if result.Payload["name"] != "Grok Build" {
			t.Fatalf("control metadata lost configured name: %+v", result.Payload)
		}
	}
	if countCalls(readCalls(t, logPath), "initialize") != 1 {
		t.Fatalf("repeated control attach started another transport: %v", readCalls(t, logPath))
	}

	// An ordinary (non-control) Spawn over the control ID must be refused:
	// the control binding is not a native chat session.
	if _, err := adapter.Spawn(context.Background(), core.Request{
		AgentType: "grok",
		SessionID: controlSessionID,
		Fields:    map[string]any{"cwd": root},
	}); err == nil {
		t.Fatalf("ordinary Spawn over control ID was accepted")
	}
	assertNoBusinessCalls(t, readCalls(t, logPath))
}

// TestFakeACP_RuntimeControlEOFReplace verifies that a control transport that
// fails is retired and replaced by a fresh initialization on the next attach,
// still without any native conversation method.
func TestFakeACP_RuntimeControlEOFReplace(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_runtime_control_eof" {
		runFakeACPRuntimeControl(true)
		return
	}

	root := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp-calls.log")
	os.Setenv("GROK_ACP_CALL_LOG", logPath)
	defer os.Unsetenv("GROK_ACP_CALL_LOG")
	marker := filepath.Join(t.TempDir(), "eof-once")
	os.Setenv("GROK_ACP_EOF_MARKER", marker)
	defer os.Unsetenv("GROK_ACP_EOF_MARKER")

	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_RuntimeControlEOFReplace"},
		Workspace:  root,
		Allowed:    []string{root},
		AgentID:    controlSessionID,
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_runtime_control_eof")
	defer os.Unsetenv("TEST_MODE")

	// First attach: the transport exits right after initialize, so the
	// binding either fails at startup or turns into error shortly after.
	if first, err := adapter.Spawn(context.Background(), controlRequest(root)); err == nil {
		if first.SessionID != controlSessionID {
			t.Fatalf("unexpected first control result: %+v", first)
		}
		waitForStatus(t, adapter, controlSessionID, "error")
	}

	// The failed control transport is retired and replaced by a fresh
	// initialization that stays alive; no native conversation method is
	// ever issued.
	second, err := adapter.Spawn(context.Background(), controlRequest(root))
	if err != nil {
		t.Fatalf("control replacement spawn failed: %v", err)
	}
	if second.Status != "idle" || second.Payload["runtime_control"] != true || second.Payload["agent_id"] != controlSessionID {
		t.Fatalf("replacement control attach lost identity: %+v", second)
	}

	calls := readCalls(t, logPath)
	if countCalls(calls, "initialize") != 2 {
		t.Fatalf("control replacement did not re-initialize: %v", calls)
	}
	assertNoBusinessCalls(t, calls)
}

// TestFakeACP_RuntimeControlCancelledContext verifies a cancelled start never
// registers a control binding and never starts a native transport.
func TestFakeACP_RuntimeControlCancelledContext(t *testing.T) {
	root := t.TempDir()
	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_RuntimeControlInitializeOnly"},
		Workspace:  root,
		Allowed:    []string{root},
		AgentID:    controlSessionID,
	})
	defer adapter.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Spawn(ctx, controlRequest(root)); err == nil {
		t.Fatalf("cancelled control Spawn reported success")
	}
	if snap := adapter.Snapshot(); len(snap) != 0 {
		t.Fatalf("cancelled control Spawn left a registered session: %+v", snap)
	}
}

// TestFakeACP_RuntimeControlShutdownRefusesRunning proves the disconnect guard
// never releases a session with an active turn: an ordinary session with a
// hanging prompt stays "running" and Shutdown must refuse, leaving the
// session and its transport intact.
func TestFakeACP_RuntimeControlShutdownRefusesRunning(t *testing.T) {
	if os.Getenv("TEST_MODE") == "fake_acp_control_hold" {
		runFakeACPControlHold()
		return
	}

	root := t.TempDir()
	adapter := grok.New(grok.Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=TestFakeACP_RuntimeControlShutdownRefusesRunning"},
		Workspace:  root,
		Allowed:    []string{root},
		AgentID:    "local-grok",
	})
	defer adapter.Close()

	os.Setenv("TEST_MODE", "fake_acp_control_hold")
	defer os.Unsetenv("TEST_MODE")

	spawned, err := adapter.Spawn(context.Background(), core.Request{
		AgentType: "grok",
		SessionID: "s-hold-1",
		Fields:    map[string]any{"cwd": root},
	})
	if err != nil {
		t.Fatalf("ordinary Spawn failed: %v", err)
	}
	if spawned.Status != "idle" {
		t.Fatalf("unexpected ordinary spawn: %+v", spawned)
	}

	sent, err := adapter.Command(context.Background(), core.Request{
		SessionID: "s-hold-1",
		Action:    "session.send",
		Fields:    map[string]any{"prompt": "hold", "command_id": "cmd-hold"},
	})
	if err != nil || sent.Status != "running" {
		t.Fatalf("send did not start a running turn: %+v %v", sent, err)
	}
	waitForStatus(t, adapter, "s-hold-1", "running")

	if released, err := adapter.Shutdown(context.Background()); err == nil {
		t.Fatalf("Shutdown released a running session: %v", released)
	}
	if entry, ok := adapter.Snapshot()["s-hold-1"]; !ok || entry.Status != "running" {
		t.Fatalf("refused Shutdown mutated the running session: %+v", adapter.Snapshot())
	}
}

// runFakeACPControlHold answers initialize and session/load, then holds every
// session/prompt unanswered so the session stays running until the transport
// is closed.
func runFakeACPControlHold() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		id := req["id"]
		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": 1,
					"_meta": map[string]any{
						"modelState": map[string]any{
							"currentModelId": "grok-4.6",
							"availableModels": []any{
								map[string]any{"modelId": "grok-4.6", "name": "Grok 4.6"},
							},
						},
					},
				},
			})
		case "session/load":
			params, _ := req["params"].(map[string]any)
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"sessionId": params["sessionId"]},
			})
		case "session/prompt":
			// Hold the prompt open: the turn stays running.
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		}
	}
}

func controlRequest(root string) core.Request {
	return core.Request{
		AgentType: "grok",
		SessionID: controlSessionID,
		Fields: map[string]any{
			"params": map[string]any{"runtime_control": true},
			"cwd":    root,
		},
	}
}

func spawnControl(t *testing.T, adapter *grok.Adapter, root string) core.Result {
	t.Helper()
	result, err := adapter.Spawn(context.Background(), controlRequest(root))
	if err != nil {
		t.Fatalf("control Spawn failed: %v", err)
	}
	return result
}

func waitForStatus(t *testing.T, adapter *grok.Adapter, sessionID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if entry, ok := adapter.Snapshot()[sessionID]; ok && entry.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("control snapshot for %s never became %q: %+v", sessionID, want, adapter.Snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readCalls(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ACP call log: %v", err)
	}
	var calls []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			calls = append(calls, line)
		}
	}
	return calls
}

func countCalls(calls []string, method string) int {
	total := 0
	for _, call := range calls {
		if call == method {
			total++
		}
	}
	return total
}

func assertNoBusinessCalls(t *testing.T, calls []string) {
	t.Helper()
	for _, forbidden := range []string{"session/new", "session/load", "session/prompt", "session/set_model"} {
		if countCalls(calls, forbidden) != 0 {
			t.Fatalf("control attach issued %s: %v", forbidden, calls)
		}
	}
}

// runFakeACPRuntimeControl is the synthetic ACP transport. It answers
// initialize with model metadata and rejects conversation methods so a
// control attach can never silently create or load a native session. When
// eof is true it exits right after initialize to simulate transport EOF.
func runFakeACPRuntimeControl(eof bool) {
	log := acpCallLog()
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		id := req["id"]
		method, _ := req["method"].(string)
		if log != nil {
			_, _ = log.WriteString(method + "\n")
		}
		switch method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": 1,
					"_meta": map[string]any{
						"modelState": map[string]any{
							"currentModelId": "grok-4.6",
							"availableModels": []any{
								map[string]any{"modelId": "grok-4.6", "name": "Grok 4.6"},
							},
						},
					},
				},
			})
			if eof && markFirstRun() {
				return
			}
		case "session/new", "session/load", "session/prompt":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error":   map[string]any{"code": -32601, "message": "control-only transport rejects " + method},
			})
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		}
	}
}

func acpCallLog() *os.File {
	path := os.Getenv("GROK_ACP_CALL_LOG")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return file
}

// markFirstRun reports true only for the first fixture process to reach this
// point; later processes keep serving so a replacement transport can stay
// alive after the first one simulates EOF.
func markFirstRun() bool {
	path := os.Getenv("GROK_ACP_EOF_MARKER")
	if path == "" {
		return true
	}
	if _, err := os.Stat(path); err == nil {
		return false
	}
	_ = os.WriteFile(path, []byte("1"), 0o644)
	return true
}
