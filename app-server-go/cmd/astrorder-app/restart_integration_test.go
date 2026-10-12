package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/lifecycle"
)

// Opt in with a freshly built native daemon; all state and listeners are private.
func TestDaemonRestartWithRealIsolatedLifecycle(t *testing.T) {
	executable := os.Getenv("ASTRORDER_TEST_DAEMON_EXECUTABLE")
	if executable == "" || runtime.GOOS != "windows" {
		t.Skip("requires explicit native daemon executable and Windows DPAPI")
	}
	executable, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	app := filepath.Join(root, "bin", "astrorder-app.exe")
	if err := os.MkdirAll(filepath.Dir(app), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app, []byte("unused app fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "frontend", "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "dist", "index.html"), []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	result, err := lifecycle.Provision(lifecycle.Options{RootDir: root, StateDir: state, DataDir: filepath.Join(state, "data"), AppExecutable: app, GRPCTarget: target, PublicEnvironment: map[string]string{"ASTRORDER_SESSION_DAEMON_EXECUTABLE": executable}})
	if err != nil {
		t.Fatal(err)
	}
	values, err := lifecycle.OpenEnvironment(state)
	if err != nil {
		t.Fatal(err)
	}
	options := lifecycle.DaemonOptions{ExecutablePath: executable, ConfigPath: filepath.Join(state, "session-daemon.json"), DatabasePath: filepath.Join(state, "data", "session-daemon.sqlite3"), WorkingDir: root, StateDir: state, Port: port, GRPCTarget: target, Secret: values["ASTRORDER_SESSION_DAEMON_SECRET"], GRPCCredential: values["ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL"], StartupTimeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if _, err := lifecycle.StopDaemonWithLifecycle(cleanup, options, false); err != nil {
			t.Errorf("isolated cleanup: %v", err)
		}
	}()
	before, err := lifecycle.StartDaemon(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if before.State != "running" || !before.IdentityVerified || before.DaemonID == "" || before.SessionCount != 0 {
		t.Fatalf("unexpected initial authority: %+v", before)
	}
	if err := daemonRestart(result.ConfigPath)(ctx, false); err != nil {
		t.Fatal(err)
	}
	after, err := lifecycle.DaemonStatus(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "running" || !after.IdentityVerified || after.DaemonID != before.DaemonID || after.Process.CreationTime == before.Process.CreationTime || after.SessionCount != 0 {
		t.Fatalf("restart authority mismatch: before=%+v after=%+v", before, after)
	}
	t.Log("isolated native restart replaced the process and preserved persistent daemon identity with zero sessions")
}
