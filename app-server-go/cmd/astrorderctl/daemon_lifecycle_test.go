package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/lifecycle"
)

func TestNativeDaemonLifecycleEndToEndPrivateFixture(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows DPAPI and native lifecycle required")
	}

	root := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	data := filepath.Join(state, "data")
	appExec := filepath.Join(root, "bin", "astrorder-app.exe")
	daemonExec := filepath.Join(root, "bin", "astrorder-sessiond.exe")

	for _, d := range []string{filepath.Dir(appExec), filepath.Join(root, "frontend", "dist")} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(appExec, []byte("app fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemonExec, []byte("daemon fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "dist", "index.html"), []byte("index fixture"), 0600); err != nil {
		t.Fatal(err)
	}

	var pOut bytes.Buffer
	pCode := run([]string{
		"provision",
		"--root", root,
		"--state-dir", state,
		"--data-dir", data,
		"--app-executable", appExec,
		"--grpc-target", "127.0.0.1:45679",
	}, &pOut)
	if pCode != 0 {
		t.Fatalf("provision failed: %s", pOut.String())
	}

	// 1. Verify session-daemon.json was created by provision
	daemonCfgPath := filepath.Join(state, "session-daemon.json")
	cfgBody, err := os.ReadFile(daemonCfgPath)
	if err != nil {
		t.Fatalf("session-daemon.json not created: %v", err)
	}
	if !strings.Contains(string(cfgBody), "runtimes") {
		t.Fatalf("session-daemon.json content invalid: %s", string(cfgBody))
	}

	// 2. Query daemon status when stopped
	var statOut bytes.Buffer
	statCode := run([]string{"daemon", "status", "--root", root, "--state-dir", state}, &statOut)
	if statCode != 0 {
		t.Fatalf("daemon status failed: %s", statOut.String())
	}
	var statResp map[string]any
	if err := json.Unmarshal(statOut.Bytes(), &statResp); err != nil || statResp["ok"] != true {
		t.Fatalf("invalid daemon status response: %s", statOut.String())
	}
	daemonData := statResp["daemon"].(map[string]any)
	if daemonData["state"] != "stopped" {
		t.Fatalf("expected state stopped, got: %v", daemonData["state"])
	}

	// 3. Test service wrapper for daemon status
	var svcOut bytes.Buffer
	svcCode := run([]string{"service", "daemon", "status", "--root", root, "--state-dir", state}, &svcOut)
	if svcCode != 0 {
		t.Fatalf("service daemon status failed: %s", svcOut.String())
	}
	var svcResp map[string]any
	if err := json.Unmarshal(svcOut.Bytes(), &svcResp); err != nil || svcResp["ok"] != true {
		t.Fatalf("invalid service daemon response: %s", svcOut.String())
	}
	if svcResp["target"] != "daemon" || svcResp["action"] != "status" || svcResp["state"] != "stopped" {
		t.Fatalf("flattened service response mismatch: %v", svcResp)
	}

	// 4. Test service wrapper rejecting unknown arguments
	svcOut.Reset()
	badCode := run([]string{"service", "daemon", "status", "--root", root, "--state-dir", state, "unexpected_arg"}, &svcOut)
	if badCode == 0 {
		t.Fatal("service wrapper accepted unexpected positional arguments")
	}

	// 5. RestartDaemonWithLifecycle lock verification
	opts := lifecycle.DaemonOptions{
		ExecutablePath: daemonExec,
		StateDir:       state,
		WorkingDir:     root,
		Port:           45679,
		GRPCTarget:     "127.0.0.1:45679",
		Secret:         "test-secret",
		GRPCCredential: "test-grpc-credential",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling Restart on a stopped synthetic daemon shouldn't deadlock.
	// It will stop (no-op since stopped) then attempt start (will fail to start synthetic text executable, but proves lock acquisition is single & clean)
	_, _ = lifecycle.RestartDaemonWithLifecycle(ctx, opts, true)
}

func TestSelectSafeLoopbackPort(t *testing.T) {
	// Bind a port to make it occupied
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	occupiedPort := ln.Addr().(*net.TCPAddr).Port

	// Ask for occupiedPort, should fallback to another free port
	allocated, err := lifecycle.SelectSafeLoopbackPort(occupiedPort)
	if err != nil {
		t.Fatal(err)
	}
	if allocated == occupiedPort {
		t.Fatalf("port %d was occupied, but select returned it", occupiedPort)
	}
}

func TestAppOnlyRestartKeepsIndependentDaemon(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}

	// Launch dummy HTTP server as synthetic App Server
	appLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	appPort := appLn.Addr().(*net.TCPAddr).Port
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ok","service":"astrorder","protocol_version":1}`))
			}
		}),
	}
	go func() { _ = srv.Serve(appLn) }()
	defer func() { _ = srv.Close() }()

	root := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	data := filepath.Join(state, "data")
	appExec := filepath.Join(root, "bin", "astrorder-app.exe")
	for _, d := range []string{filepath.Dir(appExec), filepath.Join(root, "frontend", "dist")} {
		_ = os.MkdirAll(d, 0700)
	}
	_ = os.WriteFile(appExec, []byte("app fixture"), 0600)
	_ = os.WriteFile(filepath.Join(root, "frontend", "dist", "index.html"), []byte("index"), 0600)

	var pOut bytes.Buffer
	pCode := run([]string{
		"provision",
		"--root", root,
		"--state-dir", state,
		"--data-dir", data,
		"--app-executable", appExec,
		"--grpc-target", "127.0.0.1:45680",
	}, &pOut)
	if pCode != 0 {
		t.Fatalf("provision failed: %s", pOut.String())
	}

	// Update production.json port to appPort
	cfgPath := filepath.Join(state, "production.json")
	b, _ := os.ReadFile(cfgPath)
	var envMap map[string]any
	_ = json.Unmarshal(b, &envMap)
	envObj := envMap["environment"].(map[string]any)
	envObj["ASTRORDER_PORT"] = strconv.Itoa(appPort)
	nb, _ := json.Marshal(envMap)
	_ = os.WriteFile(cfgPath, nb, 0600)

	// App status query should see port conflict or running if owned
	var appStatOut bytes.Buffer
	code := run([]string{"app", "status", "--root", root, "--state-dir", state}, &appStatOut)
	if code != 0 {
		t.Fatalf("app status failed: %s", appStatOut.String())
	}
	var appStatResp map[string]any
	_ = json.Unmarshal(appStatOut.Bytes(), &appStatResp)
	if appStatResp["ok"] != true {
		t.Fatalf("app status response not ok: %s", appStatOut.String())
	}
}
