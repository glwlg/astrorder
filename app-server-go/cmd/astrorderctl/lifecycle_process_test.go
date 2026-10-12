package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"astrorder.dev/app-server/internal/lifecycle"
)

// This is an opt-in actual product-process test, not a test helper emulating
// the App. The caller supplies a freshly built candidate executable.
func TestAppCLIRealProcessLifecycle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows process ownership")
	}
	candidate := os.Getenv("ASTRORDER_TEST_APP")
	if candidate == "" {
		t.Skip("set ASTRORDER_TEST_APP to freshly built isolated App candidate")
	}
	root := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	data := filepath.Join(state, "data")
	executable := filepath.Join(root, "bin", "astrorder-app.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(candidate)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := os.OpenFile(executable, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	source.Close()
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy isolated candidate: %v %v", copyErr, closeErr)
	}
	static := filepath.Join(root, "frontend", "dist")
	if err := os.MkdirAll(static, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(static, "index.html"), []byte("isolated CLI frontend"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	addressListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := addressListener.Addr().String()
	addressListener.Close()
	_, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	var daemonRequests atomic.Int32
	sentinel, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sentinelAddress := sentinel.Addr().String()
	sentinelServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { daemonRequests.Add(1); w.WriteHeader(http.StatusTeapot) })}
	go sentinelServer.Serve(sentinel)
	defer sentinelServer.Close()
	envelope := map[string]any{"environment": map[string]string{"ASTRORDER_HOST": "127.0.0.1", "ASTRORDER_PORT": rawPort}}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "production.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	runCLI := func(args ...string) map[string]json.RawMessage {
		t.Helper()
		var output bytes.Buffer
		if code := run(args, &output); code != 0 {
			t.Fatalf("%s failed: %s", strings.Join(args, " "), output.String())
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	provision := runCLI("provision", "--root", root, "--state-dir", state, "--data-dir", data, "--app-executable", executable, "--grpc-target", sentinelAddress)
	if string(provision["ok"]) != "true" {
		t.Fatalf("provision failed: %s", provision["ok"])
	}
	appCommand := func(operation string) lifecycle.AppStatusResult {
		t.Helper()
		result := runCLI("app", operation, "--root", root, "--state-dir", state)
		var status lifecycle.AppStatusResult
		if err := json.Unmarshal(result["app"], &status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	if status := appCommand("status"); status.State != lifecycle.StateStopped {
		t.Fatalf("initial App state=%s", status.State)
	}
	started := appCommand("start")
	// Register cleanup as soon as an owned process is created.
	defer func() {
		var output bytes.Buffer
		run([]string{"app", "stop", "--root", root, "--state-dir", state}, &output)
	}()
	if started.State != lifecycle.StateRunning || !started.IdentityVerified || !started.PortOwned || !started.Healthy {
		t.Fatalf("invalid started status: %+v", started)
	}
	repeated := appCommand("start")
	if repeated.Process != started.Process {
		t.Fatal("duplicate start changed process identity")
	}
	response, err := (&http.Client{}).Get("http://" + address + "/")
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || string(content) != "isolated CLI frontend" {
		t.Fatalf("static response=%d readErr=%v", response.StatusCode, readErr)
	}
	// Status must use only public config; even a corrupted encrypted file must
	// not prevent it from checking the exact owned process and HTTP health.
	credentialsPath := filepath.Join(state, "production.credentials.dpapi")
	encrypted, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	defer func() {
		if !restored {
			_ = os.WriteFile(credentialsPath, encrypted, 0600)
		}
	}()
	if err := os.WriteFile(credentialsPath, []byte("invalid isolated ciphertext"), 0600); err != nil {
		t.Fatal(err)
	}
	observed := appCommand("status")
	if observed.Process != started.Process || observed.State != lifecycle.StateRunning {
		t.Fatal("status depends on credential decryption")
	}
	if err := os.WriteFile(credentialsPath, encrypted, 0600); err != nil {
		t.Fatal(err)
	}
	restored = true
	restarted := appCommand("restart")
	if restarted.State != lifecycle.StateRunning || restarted.Process == started.Process {
		t.Fatal("restart did not replace only the App process")
	}
	stopped := appCommand("stop")
	if stopped.State != lifecycle.StateStopped {
		t.Fatalf("stop state=%s", stopped.State)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	owners, err := lifecycle.ListeningPIDs(port)
	if err != nil || len(owners) != 0 {
		t.Fatalf("App listener remained: owners=%v err=%v", owners, err)
	}
	if daemonRequests.Load() != 0 {
		t.Fatalf("App lifecycle contacted daemon sentinel %d times", daemonRequests.Load())
	}
	response, err = (&http.Client{}).Get("http://" + sentinelAddress + "/")
	if err != nil {
		t.Fatalf("independent sentinel did not survive: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusTeapot {
		t.Fatal("independent sentinel changed")
	}
	t.Log("real App provision/start/duplicate/status/restart/stop passed; encrypted credentials preserved; App listener released; independent sentinel survived without lifecycle requests")
}
