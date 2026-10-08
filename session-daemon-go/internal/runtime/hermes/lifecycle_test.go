package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHermesCircularAliasFailsExplicitly(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Command(ctx, core.Request{SessionID: res.SessionID, Action: "session.send", Fields: map[string]any{"text": "/loop-alias"}})
	if err == nil || !strings.Contains(err.Error(), "alias cycle") {
		t.Fatal("circular alias was not rejected explicitly", err)
	}
}

func TestHermesModelReadReportsCurrentNativeActivity(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.sessions[res.SessionID].Status = "running"
	a.mu.Unlock()
	result, err := a.Command(ctx, core.Request{SessionID: res.SessionID, Action: "session.model.read"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" {
		t.Fatal("read control misreported native activity", result.Status)
	}
}

func TestHermesCloseFailsBoundedlyWithoutProcessExit(t *testing.T) {
	a := New(Config{})
	a.processDone = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unconfirmed process exit reported successful cleanup")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("native close waited indefinitely")
	}
}

func TestHermesForeignApprovalCannotAcquireOwnership(t *testing.T) {
	emitted := 0
	a := New(Config{Emit: func(string, string, map[string]any, string) error { emitted++; return nil }})
	defer a.Close()
	a.handleServerRequest(&rpcMessage{ID: "foreign-approval", Method: "approval", Params: map[string]any{"session_id": "foreign"}})
	if emitted != 0 || len(a.pendingApprovals) != 0 {
		t.Fatal("foreign native request acquired approval ownership")
	}
}

func TestHermesResumeRejectsNativeWorkspaceOutsideAllowlist(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1", "ASTRORDER_HERMES_RESUME_CWD=" + outside}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := a.Spawn(ctx, core.Request{SessionID: "outside-native-session"}); err == nil {
		t.Fatal("native resume escaped workspace allowlist")
	}
}

func TestHermesGatewayChildCleanup(t *testing.T) {
	if os.Getenv("ASTRORDER_HERMES_NATIVE_CHILD") == "1" {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			os.WriteFile(os.Getenv("ASTRORDER_HERMES_CHILD_MARKER"), []byte("running"), 0600)
			time.Sleep(10 * time.Millisecond)
		}
		return
	}
	root := t.TempDir()
	marker := root + "/heartbeat"
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1", "ASTRORDER_HERMES_CHILD_MARKER=" + marker}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.startProcess(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.rpc(ctx, "testing.spawn_child", nil); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("child did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(marker, []byte("stopped"), 0600)
	time.Sleep(100 * time.Millisecond)
	value, err := os.ReadFile(marker)
	if err != nil || string(value) != "stopped" {
		t.Fatal("Hermes close left a native child executing", err)
	}
}

func TestHermesLateCompletionCannotClearTransportFailure(t *testing.T) {
	a := New(Config{})
	defer a.Close()
	a.sessions["owned"] = &SessionState{SessionID: "owned", Status: "idle", Metadata: map[string]any{}}
	a.readStdout(strings.NewReader(""))
	a.handleGatewayEvent(map[string]any{"type": "message.complete", "session_id": "owned"})
	if a.Snapshot()["owned"].Status != "error" {
		t.Fatal("late completion laundered unreconciled EOF into idle")
	}
}

func TestHermesEOFInvalidatesIdleOwnershipAndPendingRPC(t *testing.T) {
	a := New(Config{})
	defer a.Close()
	a.sessions["idle"] = &SessionState{SessionID: "idle", Handle: "live", Status: "idle", Metadata: map[string]any{}}
	a.handleToSession["live"] = "idle"
	reply := make(chan *rpcMessage, 1)
	a.pending["pending"] = pendingRequest{ch: reply}
	a.readStdout(strings.NewReader(""))
	if a.Snapshot()["idle"].Status != "error" {
		t.Fatal("EOF presented stale idle as authoritative inactivity")
	}
	if len(a.handleToSession) != 0 {
		t.Fatal("EOF retained live handles")
	}
	select {
	case _, open := <-reply:
		if open {
			t.Fatal("EOF returned a fabricated response")
		}
	default:
		t.Fatal("EOF did not wake pending RPC")
	}
}
func TestHermesSpawnConfirmsNativeResumeBeforeIdle(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := a.Spawn(ctx, core.Request{SessionID: "missing-native-session"})
	if err == nil {
		t.Fatal("spawn declared idle before native identity was confirmed")
	}
	if _, owned := a.Snapshot()["missing-native-session"]; owned {
		t.Fatal("failed resume left an idle ownership binding")
	}
}
