package pty_test

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/pty"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"testing"
	"time"
)

func TestPTYExplicitCloseReleasesIdentityAfterOutputDrain(t *testing.T) {
	root := t.TempDir()
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, TerminalFactory: func(string, string, []string, int, int) (pty.Terminal, error) { return newTestTerminal(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	request := core.Request{SessionID: "reusable", Fields: map[string]any{"cwd": root}}
	if _, err = runtime.Spawn(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Command(context.Background(), core.Request{SessionID: "reusable", Action: "session.close"}); err != nil {
		t.Fatal(err)
	}
	if _, exists := runtime.Snapshot()["reusable"]; exists {
		t.Fatal("closed session retained its runtime binding")
	}
	if _, err = runtime.Spawn(context.Background(), request); err != nil {
		t.Fatal("closed identity could not be reused", err)
	}
}

func TestPTYSplitUTF8SurvivesJSONProjection(t *testing.T) {
	root := t.TempDir()
	term := newTestTerminal()
	output := make(chan string, 8)
	finished := make(chan struct{})
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, TerminalFactory: func(string, string, []string, int, int) (pty.Terminal, error) { return term, nil }, Emit: func(_ string, event string, payload map[string]any, _ string) error {
		if event == "pty.output" {
			raw, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			var decoded map[string]any
			if err = json.Unmarshal(raw, &decoded); err != nil {
				return err
			}
			output <- decoded["data"].(string)
		} else if event == "pty.closed" {
			close(finished)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err = runtime.Spawn(context.Background(), core.Request{SessionID: "unicode", Fields: map[string]any{"cwd": root}}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for _, b := range []byte("中") {
			term.writer.Write([]byte{b})
		}
		term.writer.Close()
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("no EOF")
	}
	close(output)
	joined := ""
	for chunk := range output {
		joined += chunk
	}
	if joined != "中" {
		t.Fatalf("split UTF-8 corrupted by JSON: %q", joined)
	}
}

type cleanupFailureTerminal struct{ *testTerminal }

func (t *cleanupFailureTerminal) Close() error {
	t.testTerminal.Close()
	return errors.New("native cleanup failed")
}
func TestPTYCleanupFailureIsReturnedAndRecorded(t *testing.T) {
	root := t.TempDir()
	term := &cleanupFailureTerminal{newTestTerminal()}
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, TerminalFactory: func(string, string, []string, int, int) (pty.Terminal, error) { return term, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Spawn(context.Background(), core.Request{SessionID: "cleanup", Fields: map[string]any{"cwd": root}}); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Close(); err == nil {
		t.Fatal("native cleanup failure was silently ignored")
	}
	if runtime.Snapshot()["cleanup"].Status != "error" {
		t.Fatal("failed cleanup reported as idle")
	}
}

type testTerminal struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	once   sync.Once
	closed chan struct{}
}

func newTestTerminal() *testTerminal {
	r, w := io.Pipe()
	return &testTerminal{reader: r, writer: w, closed: make(chan struct{})}
}
func (t *testTerminal) Read(b []byte) (int, error) { return t.reader.Read(b) }
func (t *testTerminal) Write(b []byte) (int, error) {
	select {
	case <-t.closed:
		return 0, io.ErrClosedPipe
	default:
		return len(b), nil
	}
}
func (t *testTerminal) Resize(int, int) error { return nil }
func (t *testTerminal) Close() error {
	t.once.Do(func() { close(t.closed); t.reader.Close(); t.writer.Close() })
	return nil
}
func TestPTYBlockedStartupDoesNotBlockMetadata(t *testing.T) {
	root := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	term := newTestTerminal()
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, TerminalFactory: func(string, string, []string, int, int) (pty.Terminal, error) {
		close(started)
		<-release
		return term, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	var unblock sync.Once
	defer func() { unblock.Do(func() { close(release) }); runtime.Close() }()
	returned := make(chan error, 1)
	go func() {
		_, err := runtime.Spawn(context.Background(), core.Request{SessionID: "slow", Fields: map[string]any{"cwd": root}})
		returned <- err
	}()
	<-started
	snapshot := make(chan struct{})
	go func() { runtime.Snapshot(); close(snapshot) }()
	select {
	case <-snapshot:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("startup IO held global metadata lock")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	unblock.Do(func() { close(release) })
	select {
	case err := <-returned:
		if err == nil {
			t.Fatal("startup published ownership after runtime close")
		}
	case <-time.After(time.Second):
		t.Fatal("startup failed to finish")
	}
	select {
	case <-term.closed:
	default:
		t.Fatal("late-created terminal leaked")
	}
}
func TestPTYResizeRejectsFractionalAndNonFiniteValues(t *testing.T) {
	root := t.TempDir()
	term := newTestTerminal()
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, TerminalFactory: func(string, string, []string, int, int) (pty.Terminal, error) { return term, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err = runtime.Spawn(context.Background(), core.Request{SessionID: "dims", Fields: map[string]any{"cwd": root}}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{80.5, math.NaN(), math.Inf(1), math.Inf(-1), float32(80.5)} {
		if _, err = runtime.Command(context.Background(), core.Request{SessionID: "dims", Action: "session.resize", Fields: map[string]any{"cols": value, "rows": 24}}); err == nil {
			t.Fatalf("invalid dimension %v accepted", value)
		}
	}
}
func TestPTYDurableEmissionFailureClosesOnlyAffectedTerminal(t *testing.T) {
	root := t.TempDir()
	terms := make(chan *testTerminal, 2)
	attempted := make(chan struct{}, 1)
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, TerminalFactory: func(string, string, []string, int, int) (pty.Terminal, error) {
		term := newTestTerminal()
		terms <- term
		return term, nil
	}, Emit: func(id, event string, _ map[string]any, _ string) error {
		if id == "broken" {
			select {
			case attempted <- struct{}{}:
			default:
			}
			return errors.New("durable commit failed")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	for _, id := range []string{"broken", "healthy"} {
		if _, err = runtime.Spawn(context.Background(), core.Request{SessionID: id, Fields: map[string]any{"cwd": root}}); err != nil {
			t.Fatal(err)
		}
	}
	broken, healthy := <-terms, <-terms
	go broken.writer.Write([]byte("output"))
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("no emission attempt")
	}
	select {
	case <-broken.closed:
	case <-time.After(time.Second):
		t.Fatal("failed emission silently ignored")
	}
	if runtime.Snapshot()["broken"].Status != "error" {
		t.Fatal("storage failure reported as idle")
	}
	select {
	case <-healthy.closed:
		t.Fatal("unrelated terminal closed")
	default:
	}
}

type pidTerminal struct {
	*testTerminal
	pid int
}

func (t *pidTerminal) PID() int { return t.pid }

func TestPTYSpawnPublishesOwnerPID(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var emitted []string
	runtime, err := pty.New(pty.Config{
		Allowed: []string{root},
		TerminalFactory: func(string, string, []string, int, int) (pty.Terminal, error) {
			return &pidTerminal{testTerminal: newTestTerminal(), pid: 4242}, nil
		},
		Emit: func(_, event string, payload map[string]any, status string) error {
			if event == "runtime.owner" {
				mu.Lock()
				emitted = append(emitted, fmt.Sprintf("%v:%v", payload["owner_pid"], status))
				mu.Unlock()
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	result, err := runtime.Spawn(context.Background(), core.Request{SessionID: "owned-pty", Fields: map[string]any{"cwd": root}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Payload["owner_pid"] != 4242 {
		t.Fatalf("spawn did not report owner pid: %#v", result.Payload)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(emitted) != 1 || emitted[0] != "4242:running" {
		t.Fatalf("owner event missing: %v", emitted)
	}
}
