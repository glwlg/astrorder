package grpcapi_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"

	"astrorder.dev/session-daemon/internal/runtime/pty"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type streamTerminal struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	inputs chan []byte
	sizes  chan [2]int
	once   sync.Once
}

func newStreamTerminal() *streamTerminal {
	r, w := io.Pipe()
	return &streamTerminal{reader: r, writer: w, inputs: make(chan []byte, 8), sizes: make(chan [2]int, 8)}
}
func (t *streamTerminal) Read(data []byte) (int, error) { return t.reader.Read(data) }
func (t *streamTerminal) Write(data []byte) (int, error) {
	t.inputs <- append([]byte(nil), data...)
	return len(data), nil
}
func (t *streamTerminal) Resize(cols, rows int) error { t.sizes <- [2]int{cols, rows}; return nil }
func (t *streamTerminal) Close() error {
	t.once.Do(func() { t.reader.Close(); t.writer.Close() })
	return nil
}

func terminalOpen(t *testing.T, f *grpcFixture, id, cwd string, after int64) (grpc.BidiStreamingClient[pb.TerminalClientFrame, pb.TerminalServerFrame], context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	stream, err := pb.NewTerminalStreamClient(f.conn).Open(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err = stream.Send(&pb.TerminalClientFrame{TerminalStreamId: "view-" + id, ReconnectAfterChunkSeq: after, Frame: &pb.TerminalClientFrame_Open{Open: &pb.OpenTerminal{Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_PTY}, Session: &pb.SessionRef{SessionId: id}, Cwd: cwd, Cols: 100, Rows: 40}}}); err != nil {
		cancel()
		t.Fatal(err)
	}
	return stream, cancel
}

func TestTerminalStreamJournalReconnectBytesAndExplicitClose(t *testing.T) {
	f := startFixture(t)
	root := t.TempDir()
	term := newStreamTerminal()
	spawns := 0
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, Emit: f.daemon.Emit, TerminalFactory: func(shell, cwd string, env []string, cols, rows int) (pty.Terminal, error) {
		spawns++
		if cols != 100 || rows != 40 || cwd != root {
			t.Errorf("spawn options lost: %s %d %d", cwd, cols, rows)
		}
		return term, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	f.daemon.RegisterRuntime("pty", runtime)
	stream, cancel := terminalOpen(t, f, "terminal-bytes", root, 0)
	opened, err := stream.Recv()
	if err != nil || opened.GetOpened() == nil {
		t.Fatalf("opened: %v %v", opened, err)
	}
	want := []byte{0xff, 0, 'A', 0xe4, 0xb8, 0xad}
	go func() { term.writer.Write(want[:4]); term.writer.Write(want[4:]) }()
	var got []byte
	last := int64(0)
	for len(got) < len(want) {
		frame, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		output := frame.GetOutput()
		if output == nil {
			t.Fatalf("output missing: %v", frame)
		}
		if output.ChunkSeq <= last || output.Replay {
			t.Fatalf("invalid live sequence: %v", output)
		}
		last = output.ChunkSeq
		got = append(got, output.Data...)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("raw terminal bytes changed: %x", got)
	}
	input := []byte{0xff, 0, 'Z'}
	if err = stream.Send(&pb.TerminalClientFrame{TerminalStreamId: "view-terminal-bytes", Frame: &pb.TerminalClientFrame_Input{Input: &pb.TerminalInput{Data: input}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-term.inputs:
		if !bytes.Equal(input, got) {
			t.Fatalf("input bytes changed: %x", got)
		}
	case <-time.After(time.Second):
		t.Fatal("input not dispatched")
	}
	if err = stream.Send(&pb.TerminalClientFrame{TerminalStreamId: "view-terminal-bytes", Frame: &pb.TerminalClientFrame_Resize{Resize: &pb.TerminalResize{Cols: 120, Rows: 50}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-term.sizes:
		if got != [2]int{120, 50} {
			t.Fatalf("resize: %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("resize not dispatched")
	}
	cancel()
	stream.Recv()
	if runtime.Snapshot()["terminal-bytes"].Status != "running" {
		t.Fatal("stream detach stopped native terminal")
	}
	disconnected := []byte("offline-output")
	written := make(chan struct{})
	go func() { term.writer.Write(disconnected); close(written) }()
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("disconnected output blocked")
	}
	deadline := time.Now().Add(time.Second)
	for {
		state, err := f.daemon.AuthorityStatus()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range state.Sessions {
			if s.SessionID == "terminal-bytes" && s.MaxSeqID > last {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("output while detached not committed")
		}
		time.Sleep(time.Millisecond)
	}
	reconnected, cancelReconnect := terminalOpen(t, f, "terminal-bytes", root, last)
	defer cancelReconnect()
	opened, err = reconnected.Recv()
	if err != nil || opened.GetOpened() == nil {
		t.Fatalf("reopen: %v %v", opened, err)
	}
	replayed, err := reconnected.Recv()
	if err != nil || replayed.GetOutput() == nil || !replayed.GetOutput().Replay || !bytes.Equal(replayed.GetOutput().Data, disconnected) {
		t.Fatalf("replay lost: %v %v", replayed, err)
	}
	if spawns != 1 {
		t.Fatalf("reconnect spawned native again: %d", spawns)
	}
	if err = reconnected.Send(&pb.TerminalClientFrame{TerminalStreamId: "view-terminal-bytes", Frame: &pb.TerminalClientFrame_Close{Close: &pb.TerminalClose{Reason: "private fixture complete"}}}); err != nil {
		t.Fatal(err)
	}
	exit, err := reconnected.Recv()
	if err != nil || exit.GetExit() == nil || exit.GetExit().Cause != pb.TerminalExitCause_TERMINAL_EXIT_CAUSE_EXPLICIT_CLOSE || !exit.GetExit().RemoteExitConfirmed {
		t.Fatalf("close: %v %v", exit, err)
	}
	if _, exists := runtime.Snapshot()["terminal-bytes"]; exists {
		t.Fatal("explicit close retained native binding")
	}
}

func TestTerminalStreamOpenFailureNoOpenedReceipt(t *testing.T) {
	f := startFixture(t)
	root := t.TempDir()
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, Emit: f.daemon.Emit, TerminalFactory: func(string, string, []string, int, int) (pty.Terminal, error) {
		return nil, errors.New("synthetic native startup failure")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	f.daemon.RegisterRuntime("pty", runtime)
	stream, cancel := terminalOpen(t, f, "terminal-failed", root, 0)
	defer cancel()
	frame, err := stream.Recv()
	if frame != nil || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("startup failure faked success: %v %v", frame, err)
	}
	if len(runtime.Snapshot()) != 0 {
		t.Fatal("failed startup retained native runtime")
	}
}

func TestTerminalStreamRejectForeignRuntime(t *testing.T) {
	f := startFixture(t)
	f.daemon.RegisterRuntime("hermes", &controlRuntimeFixture{})
	f.daemon.ExecuteSessionControl(f.ctx, map[string]any{"action": "session.spawn", "agent_type": "hermes", "session_id": "foreign-runtime"})
	stream, cancel := terminalOpen(t, f, "foreign-runtime", t.TempDir(), 0)
	defer cancel()
	if frame, err := stream.Recv(); frame != nil || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("foreign runtime accepted: %v %v", frame, err)
	}
}

func TestTerminalStreamNativeExecutableFailure(t *testing.T) {
	f := startFixture(t)
	root := t.TempDir()
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, Emit: f.daemon.Emit})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	f.daemon.RegisterRuntime("pty", runtime)
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	stream, err := pb.NewTerminalStreamClient(f.conn).Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "missing-native-terminal")
	stream.Send(&pb.TerminalClientFrame{TerminalStreamId: "native-failure", Frame: &pb.TerminalClientFrame_Open{Open: &pb.OpenTerminal{Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_PTY}, Session: &pb.SessionRef{SessionId: "missing-native"}, Cwd: root, Executable: &exe, Cols: 80, Rows: 24}}})
	if frame, err := stream.Recv(); frame != nil || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing executable ignored: %v %v", frame, err)
	}
	if len(runtime.Snapshot()) != 0 {
		t.Fatal("failed executable retained binding")
	}
}
