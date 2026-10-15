package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/lifecycle"
)

func TestDaemonUpdateWithoutCandidateDoesNotRequireCredentialsOrStop(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "astrorder-sessiond.exe"), []byte("existing daemon"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"update-check", "update"} {
		var output bytes.Buffer
		code := run([]string{"service", "daemon", action, "--root", root, "--state-dir", state}, &output)
		var result map[string]any
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if code != 0 || result["ok"] != true || result["state"] != "no_update" || result["update_available"] != false {
			t.Fatalf("%s without candidate: code=%d result=%v", action, code, result)
		}
		if _, err := os.Stat(filepath.Join(state, "daemon-process.json")); !os.IsNotExist(err) {
			t.Fatalf("no-update check touched lifecycle state: %v", err)
		}
	}
}

// This exercises product daemon binaries with empty runtimes, an isolated port
// and a new database. It never connects to installed or native agent sessions.
func TestDaemonUpdateRealProcess(t *testing.T) {
	candidate := os.Getenv("ASTRORDER_TEST_DAEMON")
	if candidate == "" {
		t.Skip("set ASTRORDER_TEST_DAEMON to a freshly built isolated daemon")
	}
	root := t.TempDir()
	state := t.TempDir()
	executable := filepath.Join(root, "bin", "astrorder-sessiond.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	copyFile := func(from, to string) {
		t.Helper()
		input, err := os.Open(from)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		output, err := os.Create(to)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("copy isolated executable: %v %v", copyErr, closeErr)
		}
	}
	copyFile(candidate, executable)
	// These are only provision path fixtures; no App process is launched.
	appExecutable := filepath.Join(root, "bin", "astrorder-app.exe")
	if err := os.WriteFile(appExecutable, []byte("provision path fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "frontend", "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "dist", "index.html"), []byte("isolated"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := listener.Addr().String()
	listener.Close()
	call := func(args ...string) (map[string]any, int) {
		t.Helper()
		var output bytes.Buffer
		code := run(args, &output)
		var result map[string]any
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result, code
	}
	if result, code := call("provision", "--root", root, "--state-dir", state, "--data-dir", filepath.Join(state, "data"), "--app-executable", appExecutable, "--grpc-target", target); code != 0 {
		t.Fatalf("isolated provision: %v", result)
	}
	config := filepath.Join(state, "production.json")
	body, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Environment map[string]any `json:"environment"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := filepath.Join(state, "empty-runtimes.json")
	if err := os.WriteFile(runtimeConfig, []byte(`{"runtimes":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	envelope.Environment["ASTRORDER_SESSION_DAEMON_CONFIG"] = runtimeConfig
	envelope.Environment["ASTRORDER_SESSION_DAEMON_EXECUTABLE"] = executable
	envelope.Environment["ASTRORDER_SESSION_DAEMON_DB"] = filepath.Join(state, "data", "session-daemon.sqlite3")
	body, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, body, 0600); err != nil {
		t.Fatal(err)
	}
	service := func(action string) (map[string]any, int) {
		return call("service", "daemon", action, "--root", root, "--state-dir", state)
	}
	started, code := service("start")
	if code != 0 || started["state"] != "running" {
		t.Fatalf("start isolated daemon: %v", started)
	}
	t.Cleanup(func() {
		if result, code := service("stop"); code != 0 {
			t.Errorf("isolated daemon cleanup: %v", result)
		}
	})
	identity := func(result map[string]any) lifecycle.ProcessIdentity {
		body, _ := json.Marshal(result["process"])
		var process lifecycle.ProcessIdentity
		if err := json.Unmarshal(body, &process); err != nil {
			t.Fatal(err)
		}
		return process
	}
	before := identity(started)
	if result, code := service("update"); code != 0 || result["state"] != "no_update" {
		t.Fatalf("missing update disturbed running daemon: %v", result)
	}
	if status, _ := service("status"); identity(status) != before {
		t.Fatal("no-update changed daemon process")
	}
	copyFile(candidate, executable+".new")
	updated, code := service("update")
	if code != 0 || updated["state"] != "running" || updated["updated"] != true || identity(updated) == before || updated["identity_verified"] != true {
		t.Fatalf("update did not start a new verified process: %v", updated)
	}
	if _, err := lifecycle.InspectProcess(before.PID); !os.IsNotExist(err) {
		t.Fatalf("old daemon did not exit: %v", err)
	}
	if _, err := os.Stat(executable + ".new"); !os.IsNotExist(err) {
		t.Fatalf("applied candidate was not consumed: %v", err)
	}
	backup, _ := updated["backup_path"].(string)
	originalBytes, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{executable, backup} {
		if content, err := os.ReadFile(path); err != nil || !bytes.Equal(content, originalBytes) {
			t.Fatalf("updated executable or backup bytes differ: %s err=%v", path, err)
		}
	}
	// A valid Windows program that exits immediately exercises startup rollback.
	copyFile(os.Getenv("ASTRORDER_TEST_CTL"), executable+".new")
	rollback, code := service("update")
	t.Logf("failed-candidate update response: %v", rollback)
	message, _ := rollback["message"].(string)
	if code == 0 || !strings.Contains(message, "previous executable restored") {
		t.Fatalf("startup rollback was not exercised: %v", rollback)
	}
	status, code := service("status")
	if code != 0 || status["state"] != "running" || status["identity_verified"] != true || identity(status) == identity(updated) {
		t.Fatalf("previous daemon did not recover: %v", status)
	}
	if restored, err := os.ReadFile(executable); err != nil || !bytes.Equal(restored, originalBytes) {
		t.Fatalf("rollback did not restore executable bytes: %v", err)
	}
	t.Log("real daemon update, missing candidate, process exit, verified replacement and startup rollback passed")
}
