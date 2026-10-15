package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// Uses a real private daemon and formal App startup; only the native stdio peer
// is synthetic. No production services, user histories, or model calls are used.
func TestAutoConnectAfterPrivateDaemonRestart(t *testing.T) {
	executable := os.Getenv("ASTRORDER_TEST_SESSIOND")
	if executable == "" {
		t.Skip("requires a private daemon candidate")
	}
	root, err := os.MkdirTemp("C:/Users/luwei/AppData/Local/hermes/cache/scratch", "auto-connect-native-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := lis.Addr().String()
	lis.Close()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "daemon.json")
	body, _ := json.Marshal(map[string]any{"runtimes": []any{map[string]any{"type": "codex", "executable": helper, "arguments": []string{"-test.run=^TestGroupNativeProcessHelper$", "astrorder-group-helper"}, "workspace": root, "allowed": []string{root}, "agent_id": "private-codex"}}})
	if err := os.WriteFile(cfgPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	var command *exec.Cmd
	var done chan error
	stop := func() {
		if command == nil {
			return
		}
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("private daemon did not exit")
		}
		command = nil
	}
	defer stop()
	start := func() {
		command = exec.Command(executable)
		for _, key := range []string{"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR"} {
			if value, ok := os.LookupEnv(key); ok {
				command.Env = append(command.Env, key+"="+value)
			}
		}
		command.Env = append(command.Env, "USERPROFILE="+root, "HOME="+root, "ASTRORDER_SESSION_DAEMON_SECRET=fixture", "ASTRORDER_SESSION_DAEMON_DB="+filepath.Join(root, "daemon.db"), "ASTRORDER_SESSION_DAEMON_CONFIG="+cfgPath, "ASTRORDER_SESSION_DAEMON_GRPC_TARGET="+address, "ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL=fixture", "ASTRORDER_SESSION_DAEMON_WS_DISABLED=1")
		command.Stdout, command.Stderr = io.Discard, io.Discard
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done = make(chan error, 1)
		current := command
		go func() { done <- current.Wait() }()
	}
	start()
	var client *daemonclient.Client
	for ctx.Err() == nil {
		probe, end := context.WithTimeout(ctx, 200*time.Millisecond)
		client, err = daemonclient.Dial(probe, daemonclient.Config{Target: address, Credential: "fixture"})
		end()
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if client == nil {
		t.Fatalf("private daemon readiness: %v", err)
	}
	defer client.Close()
	epoch := client.DaemonID()
	appCtx, stopApp := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	appDone := make(chan error, 1)
	go func() {
		err := runServer(appCtx, config.Config{Listen: "127.0.0.1:0", DatabasePath: filepath.Join(root, "app.db"), DefaultWorkspace: root, BrowserSecret: "fixture", ConnectorSecret: "fixture", Daemon: config.DaemonConfig{Target: address, Credential: "fixture", RequestTimeout: time.Second}}, writer, slog.New(slog.NewTextHandler(io.Discard, nil)))
		writer.CloseWithError(err)
		appDone <- err
	}()
	defer func() {
		stopApp()
		select {
		case err := <-appDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("private App did not exit")
		}
	}()
	var ready readyMessage
	if err := json.NewDecoder(reader).Decode(&ready); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	waitOwned := func() {
		t.Helper()
		for ctx.Err() == nil {
			probe, end := context.WithTimeout(ctx, 500*time.Millisecond)
			snapshot, err := client.Daemon.GetStatus(probe, &pb.GetStatusRequest{})
			end()
			if err == nil {
				for _, s := range snapshot.Sessions {
					if s.GetSession().GetSessionId() == "probe-codex-local" && s.GetRuntimeOwned() {
						return
					}
				}
			}
			select {
			case err := <-appDone:
				t.Fatalf("App exited during reconnect: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
		}
		t.Fatal("private control channel did not reconnect")
	}
	waitOwned()
	firstPID := command.Process.Pid
	stop()
	start()
	waitOwned()
	if command.Process.Pid == firstPID {
		t.Fatal("private daemon process did not change")
	}
	if client.DaemonID() != epoch {
		t.Fatal("persistent daemon identity changed")
	}
	t.Log("formal App stayed running; real private daemon restarted and control ownership returned automatically")
}
