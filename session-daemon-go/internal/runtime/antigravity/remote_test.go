package antigravity

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
)

type fakeAgySSHSession struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	done    chan struct{}

	mu         sync.Mutex
	closed     bool
	exitCode   int
	remoteExit bool
}

func newFakeAgySSHSession() *fakeAgySSHSession {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	return &fakeAgySSHSession{
		stdinR:  inR,
		stdinW:  inW,
		stdoutR: outR,
		stdoutW: outW,
		done:    make(chan struct{}),
	}
}

func (f *fakeAgySSHSession) Stdin() io.WriteCloser { return f.stdinW }
func (f *fakeAgySSHSession) Stdout() io.ReadCloser { return f.stdoutR }
func (f *fakeAgySSHSession) Stderr() []byte        { return nil }

func (f *fakeAgySSHSession) finishRemote(code int) {
	f.mu.Lock()
	if !f.closed {
		f.exitCode = code
		f.remoteExit = true
	}
	f.mu.Unlock()
	_ = f.stdoutW.Close()
	select {
	case <-f.done:
	default:
		close(f.done)
	}
}

func (f *fakeAgySSHSession) Wait() (sshtrans.ExitStatus, error) {
	<-f.done
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return sshtrans.ExitStatus{LocalKilled: true, ExitCode: -1, Err: sshtrans.ErrSessionClosed}, sshtrans.ErrSessionClosed
	}
	return sshtrans.ExitStatus{ConfirmedRemoteExit: f.remoteExit, ExitCode: f.exitCode}, nil
}

func (f *fakeAgySSHSession) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	_ = f.stdinW.Close()
	_ = f.stdinR.Close()
	_ = f.stdoutW.Close()
	_ = f.stdoutR.Close()
	select {
	case <-f.done:
	default:
		close(f.done)
	}
	return nil
}

func TestRemoteWorkspaceAllowlistAndFallback(t *testing.T) {
	noSSH := func(context.Context, sshtrans.CommandSpec) (SSHSession, error) {
		t.Fatal("workspace validation must not start SSH")
		return nil, nil
	}
	for _, tc := range []struct {
		name, root, ws, raw, want string
		ok                        bool
	}{
		{"root subtree", "/", "", "/tmp/project", "/tmp/project", true},
		{"root itself", "/", "", "/", "/", true},
		{"scoped subtree", "/work", "/work", "/work/app", "/work/app", true},
		{"scoped exact", "/work", "/work", "/work", "/work", true},
		{"sibling prefix rejected", "/work", "/work", "/workspace", "", false},
		{"parent escape rejected", "/work", "/work", "/work/../etc", "", false},
		{"relative path rejected", "/", "/", "relative/dir", "", false},
		{"nul byte rejected", "/", "/", "/work/\x00bad", "", false},
		{"windows drive falls back to configured posix", "/home/luwei", "/home/luwei", `P:\DevApp\Astrorder\server`, "/home/luwei", true},
		{"windows drive falls back to slash", "/", "", `C:\Users\luwei`, "/", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewWithSSH(Config{Workspace: tc.ws, Allowed: []string{tc.root}, ConnectionID: "wsl"}, noSSH, nil)
			got, err := a.workspace(tc.raw)
			if (err == nil) != tc.ok {
				t.Fatalf("workspace(%q) err=%v wantOK=%v", tc.raw, err, tc.ok)
			}
			if tc.ok && got != tc.want {
				t.Fatalf("workspace(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestRemoteAntigravityACPLifecycleWithHelper(t *testing.T) {
	remoteHome := t.TempDir()
	cliDir := filepath.Join(remoteHome, ".gemini", "antigravity-cli")
	convDir := filepath.Join(cliDir, "conversations")
	desktopConvDir := filepath.Join(remoteHome, ".gemini", "antigravity", "conversations")
	if err := os.MkdirAll(convDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(desktopConvDir, 0o700); err != nil {
		t.Fatal(err)
	}

	parentID := "11111111-1111-4111-8111-111111111111"
	resumableID := "44444444-4444-4444-8444-444444444444"
	busyExternalID := "55555555-5555-4555-8555-555555555555"
	desktopID := "66666666-6666-4666-8666-666666666666"

	sumDBPath := filepath.Join(cliDir, "conversation_summaries.db")
	sumDB, err := sql.Open("sqlite", sumDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sumDB.Close()
	if _, err := sumDB.Exec(`CREATE TABLE conversation_summaries (
		conversation_id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT "",
		last_modified_time TEXT NOT NULL,
		workspace_uris TEXT NOT NULL,
		not_fully_idle NUMERIC NOT NULL DEFAULT 0,
		killed NUMERIC NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	insertSummary := func(id, title, uri string, active, killed int) {
		t.Helper()
		if _, err := sumDB.Exec(`INSERT OR REPLACE INTO conversation_summaries VALUES(?,?,?,?,?,?)`,
			id, title, "2026-11-17 15:00:00Z", fmt.Sprintf("[%q]", uri), active, killed); err != nil {
			t.Fatal(err)
		}
	}
	insertSummary(parentID, "Remote Parent", "file:///home/luwei/project", 0, 0)
	insertSummary(resumableID, "Idle Remote", "file:///home/luwei/project", 0, 0)
	insertSummary(busyExternalID, "Busy Remote", "file:///home/luwei/project", 1, 0)

	seedStepsDB := func(dir, id string, texts ...string) {
		t.Helper()
		db, err := sql.Open("sqlite", filepath.Join(dir, id+".db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_format INTEGER,step_payload BLOB,metadata BLOB)`); err != nil {
			t.Fatal(err)
		}
		for idx, txt := range texts {
			kind := 14
			if idx%2 == 1 {
				kind = 15
			}
			if _, err := db.Exec(`INSERT INTO steps VALUES(?,?,3,0,?,NULL)`, idx, kind, nativeText(kind, txt)); err != nil {
				t.Fatal(err)
			}
		}
	}
	seedStepsDB(convDir, parentID, "remote user prompt", "remote assistant reply", "second prompt")
	seedStepsDB(convDir, resumableID, "saved user", "saved reply")
	seedStepsDB(convDir, busyExternalID, "busy user")
	seedStepsDB(desktopConvDir, desktopID, "desktop user", "desktop reply")

	var eventsMu sync.Mutex
	var emitted []map[string]any
	emit := func(streamID, event string, payload map[string]any, status string) error {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		emitted = append(emitted, map[string]any{"stream_id": streamID, "event": event, "payload": payload, "status": status})
		return nil
	}

	starter := func(_ context.Context, spec sshtrans.CommandSpec) (SSHSession, error) {
		if _, err := sshtrans.BuildRemoteCommand(spec); err != nil {
			return nil, err
		}
		fake := newFakeAgySSHSession()
		if len(spec.Args) > 0 && spec.Args[len(spec.Args)-1] == "models" {
			go func() {
				_, _ = fmt.Fprintln(fake.stdoutW, "gemini-test-medium\tTest Gemini (Medium)")
				_, _ = fmt.Fprintln(fake.stdoutW, "claude-test\tTest Claude")
				fake.finishRemote(0)
			}()
			return fake, nil
		}
		go func() {
			serveMockACP(fake.stdinR, fake.stdoutW, parentID, false)
			fake.finishRemote(0)
		}()
		return fake, nil
	}

	helper := func(ctx context.Context, payload map[string]any) (map[string]any, error) {
		payload["home"] = remoteHome
		return ExecuteHelper(ctx, payload)
	}

	adapter := NewWithSSH(Config{
		Executable:   "/home/luwei/.local/bin/agy",
		Workspace:    "/home/luwei/project",
		Allowed:      []string{"/home/luwei"},
		ConnectionID: "wsl",
		AgentName:    "Antigravity · WSL",
		Emit:         emit,
	}, starter, helper)
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Runtime control probe + session.list
	if _, err := adapter.Spawn(ctx, core.Request{SessionID: "probe-antigravity-wsl", Fields: map[string]any{"runtime_control": true}}); err != nil {
		t.Fatalf("runtime_control spawn: %v", err)
	}
	listRes, err := adapter.Query(ctx, core.Request{Fields: map[string]any{"method": "session.list"}})
	if err != nil || len(listRes["items"].([]map[string]any)) != 3 {
		t.Fatalf("session.list: %+v %v", listRes, err)
	}

	// 2. Busy external conversation rejected; desktop conversation attached readonly; idle CLI conversation resumed!
	if _, err := adapter.Spawn(ctx, core.Request{SessionID: busyExternalID}); err == nil {
		t.Fatal("busy external conversation should not resume")
	}
	deskAttach, err := adapter.Spawn(ctx, core.Request{SessionID: desktopID})
	if err != nil || deskAttach.Payload["control_state"] != "readonly" {
		t.Fatalf("desktop readonly attach: %+v %v", deskAttach, err)
	}
	deskHist, err := adapter.Command(ctx, core.Request{SessionID: desktopID, Action: "session.history_page", Fields: map[string]any{"limit": 10}})
	if err != nil || len(deskHist.Payload["items"].([]map[string]any)) != 2 {
		t.Fatalf("desktop history_page: %+v %v", deskHist, err)
	}

	resumed, err := adapter.Spawn(ctx, core.Request{SessionID: parentID})
	if err != nil || resumed.SessionID != parentID {
		t.Fatalf("resume parent: %+v %v", resumed, err)
	}
	histRes, err := adapter.Command(ctx, core.Request{SessionID: parentID, Action: "session.history_page", Fields: map[string]any{"limit": 2}})
	if err != nil || len(histRes.Payload["items"].([]map[string]any)) != 2 {
		t.Fatalf("history_page 1: %+v %v", histRes, err)
	}
	histRes2, err := adapter.Command(ctx, core.Request{SessionID: parentID, Action: "session.history_page", Fields: map[string]any{"limit": 2, "before": histRes.Payload["next_cursor"]}})
	if err != nil || len(histRes2.Payload["items"].([]map[string]any)) != 1 {
		t.Fatalf("history_page 2: %+v %v", histRes2, err)
	}

	// 3. Send, interrupt, model/approval switch, and delete over SSH ACP
	if _, err := adapter.Command(ctx, core.Request{SessionID: parentID, Action: "session.send", Fields: map[string]any{"text": "WAIT_CANCEL", "command_id": "remote-cancel"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := adapter.Command(ctx, core.Request{SessionID: parentID, Action: "session.interrupt"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for adapter.Snapshot()[parentID].Status == "running" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if adapter.Snapshot()[parentID].Status != "idle" {
		t.Fatalf("remote cancelled turn did not settle: %+v", adapter.Snapshot()[parentID])
	}

	if _, err := adapter.Spawn(ctx, core.Request{SessionID: resumableID}); err != nil {
		t.Fatal(err)
	}
	delRes, err := adapter.Command(ctx, core.Request{SessionID: resumableID, Action: "session.delete"})
	if err != nil || delRes.Payload["deleted"] != resumableID {
		t.Fatalf("remote delete failed: %+v %v", delRes, err)
	}

	if _, err := adapter.Command(ctx, core.Request{SessionID: parentID, Action: "session.close"}); err != nil {
		t.Fatalf("remote close failed: %v", err)
	}
	released, err := adapter.Shutdown(ctx)
	if err != nil || len(released) == 0 {
		t.Fatalf("remote Shutdown failed: released=%v err=%v", released, err)
	}
}
