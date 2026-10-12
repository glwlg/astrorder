package grpcapi_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/pty"
)

func init() {
	if os.Getenv("ASTRORDER_GRPC_TERMINAL_HELPER") == "1" {
		fmt.Printf("NATIVE_ARGS=%s|%s\n", os.Args[1], os.Args[2])
		os.Exit(0)
	}
}

func TestTerminalStreamNativeCommandArgumentsAndExit(t *testing.T) {
	t.Setenv("ASTRORDER_GRPC_TERMINAL_HELPER", "1")
	f := startFixture(t)
	root := t.TempDir()
	executable := filepath.Join(root, "native helper with spaces.exe")
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(executable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(target, source); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err = target.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(executable, 0700); err != nil {
		t.Fatal(err)
	}
	runtime, err := pty.New(pty.Config{Allowed: []string{root}, Emit: f.daemon.Emit})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	f.daemon.RegisterRuntime("pty", runtime)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	stream, err := pb.NewTerminalStreamClient(f.conn).Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.Send(&pb.TerminalClientFrame{TerminalStreamId: "native-command", Frame: &pb.TerminalClientFrame_Open{Open: &pb.OpenTerminal{Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_PTY}, Session: &pb.SessionRef{SessionId: "native-command"}, Cwd: root, Executable: &executable, Args: []string{"space argument", "引号\"与尾斜杠\\"}, Cols: 100, Rows: 40}}}); err != nil {
		t.Fatal(err)
	}
	opened, err := stream.Recv()
	if err != nil || opened.GetOpened() == nil {
		t.Fatalf("native opened: %v %v", opened, err)
	}
	var output []byte
	for {
		frame, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if chunk := frame.GetOutput(); chunk != nil {
			output = append(output, chunk.Data...)
		}
		if exit := frame.GetExit(); exit != nil {
			if !exit.RemoteExitConfirmed {
				t.Fatal("native exit unconfirmed")
			}
			break
		}
	}
	if !strings.Contains(string(output), "NATIVE_ARGS=space argument|引号\"与尾斜杠\\") {
		t.Fatalf("native arguments lost: %q", output)
	}
}
