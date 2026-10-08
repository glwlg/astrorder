package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHermesConcurrentResumeHasOneNativeHandle(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.startProcess(ctx); err != nil {
		t.Fatal(err)
	}
	session := &SessionState{SessionID: "stored-concurrent", Workspace: root, Status: "idle", Metadata: map[string]any{}}
	a.sessions["stored-concurrent"] = session
	results := make(chan string, 8)
	for range 8 {
		go func() {
			handle, err := a.ensureHandle(ctx, session)
			if err != nil {
				handle = err.Error()
			}
			results <- handle
		}()
	}
	first := <-results
	for range 7 {
		if next := <-results; next != first {
			t.Fatalf("duplicate native resumes: %q != %q", first, next)
		}
	}
}

func TestHermesDeleteCannotCloseRunningNativeHandle(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: session.SessionID, Action: "session.send", Fields: map[string]any{"text": "slow:sleep-long"}}); err != nil {
		t.Fatal(err)
	}
	_, err = a.Command(ctx, core.Request{SessionID: session.SessionID, Action: "session.delete"})
	if err == nil {
		t.Fatal("active native handle was closed and deleted")
	}
	if a.Snapshot()[session.SessionID].Status != "running" {
		t.Fatal("refused delete changed active state")
	}
}

func TestHermesHistoryRejectsInvalidLimitWithoutRPCFallback(t *testing.T) {
	a := New(Config{})
	s := &SessionState{SessionID: "a", Status: "idle", Metadata: map[string]any{}}
	for _, limit := range []any{0, 201, 1.5, true, "2"} {
		_, err := a.handleHistoryPage(context.Background(), s, core.Request{Fields: map[string]any{"limit": limit}})
		if err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("invalid history limit reached native IO: %v", err)
		}
	}
}

func TestHermesApprovalSetRequiresNativeReadback(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1", "ASTRORDER_HERMES_REFUSE_CONFIG=1"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Command(ctx, core.Request{SessionID: session.SessionID, Action: "session.approval.set", Fields: map[string]any{"mode": "manual"}})
	if err == nil {
		t.Fatal("unconfirmed native approval change was reported successful")
	}
}

func TestHermesCompletionBeforePromptReplyDoesNotRevertIdle(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Command(ctx, core.Request{SessionID: session.SessionID, Action: "session.send", Fields: map[string]any{"text": "trigger:complete-before-reply"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Snapshot()[session.SessionID].Status != "idle" {
		t.Fatal("late prompt acknowledgement overwrote native completion")
	}
}

func TestHermesConcurrentStartupWaitsForGatewayReadiness(t *testing.T) {
	if os.Getenv("ASTRORDER_HERMES_NO_READY") == "1" {
		var frame any
		decoder := json.NewDecoder(os.Stdin)
		for decoder.Decode(&frame) == nil {
		}
		os.Exit(0)
	}
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesConcurrentStartupWaitsForGatewayReadiness"}, Environment: []string{"ASTRORDER_HERMES_NO_READY=1"}, Workspace: root})
	defer a.Close()
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- a.startProcess(first) }()
	deadline := time.Now().Add(time.Second)
	for {
		a.mu.RLock()
		started := a.cmd != nil
		a.mu.RUnlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native helper did not start")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, stop := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer stop()
	if err := a.startProcess(ctx); err == nil {
		t.Fatal("another caller skipped gateway readiness")
	}
	cancel()
	<-finished
}

func TestHermesCancelledStartDoesNotCreateNativeProcess(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"}, Environment: []string{"ASTRORDER_HERMES_HELPER=1"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.startProcess(ctx); err == nil {
		t.Fatal("cancelled startup succeeded")
	}
	if a.cmd != nil {
		t.Fatal("cancelled request created a native process")
	}
}
func TestHermesSlowSubscriberCannotBlockRPCResponses(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a := New(Config{Emit: func(string, string, map[string]any, string) error { close(entered); <-release; return nil }})
	a.sessions["a"] = &SessionState{SessionID: "a", Status: "running", Metadata: map[string]any{}}
	reply := make(chan *rpcMessage, 1)
	a.pending["reply"] = pendingRequest{ch: reply}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	defer a.Close()
	defer close(release)
	go a.readStdout(reader)
	go func() {
		encoder := json.NewEncoder(writer)
		encoder.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "message.complete", "session_id": "a"}})
		encoder.Encode(map[string]any{"id": "reply", "result": map[string]any{"ok": true}})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback not reached")
	}
	select {
	case message := <-reply:
		if message == nil {
			t.Fatal("response lost")
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("native RPC reader blocked behind slow subscriber")
	}
}

func TestHermesValidatesWorkspaceBeforeStartingGateway(t *testing.T) {
	root := t.TempDir()
	a := New(Config{Workspace: root, Allowed: []string{root}})
	defer a.Close()
	_, err := a.Create(context.Background(), core.Request{Fields: map[string]any{"cwd": t.TempDir()}})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("workspace validation was bypassed: %v", err)
	}
	if a.cmd != nil {
		t.Fatal("invalid input started native gateway")
	}
}
func TestHermesForeignGatewayEventCannotPublish(t *testing.T) {
	calls := 0
	a := New(Config{Emit: func(string, string, map[string]any, string) error { calls++; return nil }})
	a.handleGatewayEvent(map[string]any{"type": "message.complete", "session_id": "foreign", "payload": map[string]any{"text": "not ours"}})
	if calls != 0 {
		t.Fatal("foreign event crossed native ownership boundary")
	}
}
func TestHermesForwardsStructuredStreamWithoutChangingOpaqueIdentity(t *testing.T) {
	calls := 0
	a := New(Config{Emit: func(id, event string, payload map[string]any, status string) error {
		calls++
		if id != "durable" || event != "hermes.stream" || status != "running" {
			t.Fatalf("incorrect stream envelope %q %q %q", id, event, status)
		}
		return nil
	}})
	a.sessions["durable"] = &SessionState{SessionID: "durable", Handle: "live", Status: "running", Metadata: map[string]any{}}
	a.handleToSession["live"] = "durable"
	a.handleGatewayEvent(map[string]any{"type": "hermes.stream", "session_id": "live", "payload": map[string]any{"delta": "native"}})
	if calls != 1 {
		t.Fatal("structured stream silently dropped")
	}
}
func TestHermesUnknownApprovalCannotBeSentAsNativeDecision(t *testing.T) {
	a := New(Config{})
	a.sessions["a"] = &SessionState{SessionID: "a", Handle: "live", Status: "waiting_approval", Metadata: map[string]any{}}
	_, err := a.Command(context.Background(), core.Request{SessionID: "a", Action: "session.approve", Fields: map[string]any{"approval_id": "invented", "choice": "once"}})
	if err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("unknown approval was not rejected before IO: %v", err)
	}
}
func TestHermesFailedApprovalWriteRetainsPendingDecision(t *testing.T) {
	a := New(Config{})
	s := &SessionState{SessionID: "a", Status: "waiting_approval", Metadata: map[string]any{}}
	a.sessions["a"] = s
	a.pendingApprovals["approval"] = &approvalRecord{NativeID: "srq-a", SessionID: "a"}
	_, err := a.handleApprove(context.Background(), s, core.Request{Fields: map[string]any{"approval_id": "approval", "choice": "once"}})
	if err == nil {
		t.Fatal("closed transport accepted approval")
	}
	if a.pendingApprovals["approval"] == nil {
		t.Fatal("failed decision discarded pending approval")
	}
}
func TestHermesReadOnlyOperationPreservesRunningState(t *testing.T) {
	a := New(Config{})
	s := &SessionState{SessionID: "a", Status: "running", Metadata: map[string]any{}}
	a.sessions["a"] = s
	a.pendingApprovals["other"] = &approvalRecord{NativeID: "srq-other", SessionID: "different"}
	_, err := a.handleApprove(context.Background(), s, core.Request{Fields: map[string]any{"approval_id": "other", "choice": "once"}})
	if err == nil || !strings.Contains(err.Error(), "owned") {
		t.Fatalf("foreign approval was not rejected: %v", err)
	}
	if a.pendingApprovals["other"] == nil {
		t.Fatal("foreign decision mutated approval state")
	}
}
func TestHermesEmitFailureStopsAcceptingNativeWork(t *testing.T) {
	a := New(Config{Emit: func(string, string, map[string]any, string) error { return errors.New("commit failed") }})
	a.sessions["a"] = &SessionState{SessionID: "a", Status: "running", Metadata: map[string]any{}}
	a.handleGatewayEvent(map[string]any{"type": "message.complete", "session_id": "a"})
	if a.Snapshot()["a"].Status != "error" {
		t.Fatal("journal failure masqueraded as completion")
	}
}

func TestHermesCreatePublishesLocalOwnerPID(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var sessionID, status string
	var pid int
	adapter := New(Config{
		Executable:  os.Args[0],
		Arguments:   []string{"-test.run=TestHermesAdapterOwnsMultipleSessions"},
		Environment: []string{"ASTRORDER_HERMES_HELPER=1"},
		Workspace:   root,
		Allowed:     []string{root},
		Emit: func(id, event string, payload map[string]any, frameStatus string) error {
			if event != "runtime.owner" {
				return nil
			}
			mu.Lock()
			sessionID = id
			status = frameStatus
			if event == "runtime.owner" {
				status = "recorded"
			}
			pid, _ = payload["owner_pid"].(int)
			mu.Unlock()
			return nil
		},
	})
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	created, err := adapter.Create(ctx, core.Request{Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sessionID != created.SessionID || status != "recorded" || pid != adapter.cmd.Process.Pid || pid <= 0 {
		t.Fatalf("owner frame session=%q status=%q pid=%d process=%d", sessionID, status, pid, adapter.cmd.Process.Pid)
	}
}
