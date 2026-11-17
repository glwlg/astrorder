//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "lifecycle-broker" {
		os.Exit(runLifecycleBroker(os.Args[2:], os.Stdout))
	}
	if strings.EqualFold(filepath.Base(os.Args[0]), "astrorder-app.exe") {
		os.Exit(runBrokerTestApp())
	}
	os.Exit(m.Run())
}

func runBrokerTestApp() int {
	server := &http.Server{Addr: net.JoinHostPort("127.0.0.1", os.Getenv("ASTRORDER_PORT"))}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok","service":"astrorder","protocol_version":1}`)
	})
	mux.HandleFunc("/_desktop/shutdown", func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+os.Getenv("ASTRORDER_BROWSER_SECRET") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `{"stopping":true}`)
		go func() {
			time.Sleep(25 * time.Millisecond)
			_ = server.Shutdown(context.Background())
		}()
	})
	server.Handler = mux
	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return 1
	}
	return 0
}

func TestRestrictedJobLifecycleCaller(t *testing.T) {
	if os.Getenv("ASTRORDER_BROKER_TEST_CALLER") != "1" {
		return
	}
	gate := os.Getenv("ASTRORDER_BROKER_TEST_GATE")
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(1)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var result bytes.Buffer
	code := run([]string{
		"service", "app", "start",
		"--root", os.Getenv("ASTRORDER_BROKER_TEST_ROOT"),
		"--state-dir", os.Getenv("ASTRORDER_BROKER_TEST_STATE"),
	}, &result)
	if err := os.WriteFile(os.Getenv("ASTRORDER_BROKER_TEST_RESULT"), result.Bytes(), 0600); err != nil || code != 0 {
		os.Exit(1)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestRestrictedJobStartsIndependentApp(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "安装 with spaces")
	state := filepath.Join(base, "状态 with spaces")
	executable := filepath.Join(root, "bin", "astrorder-app.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	destination, err := os.Create(executable)
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	source.Close()
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy isolated App helper: %v %v", copyErr, closeErr)
	}
	static := filepath.Join(root, "frontend", "dist")
	if err := os.MkdirAll(static, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(static, "index.html"), []byte("isolated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	listener.Close()
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{"environment": map[string]string{
		"ASTRORDER_HOST": "127.0.0.1", "ASTRORDER_PORT": port,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "production.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	var provision bytes.Buffer
	if code := run([]string{
		"provision", "--root", root, "--state-dir", state,
		"--data-dir", filepath.Join(state, "data"), "--app-executable", executable,
		"--grpc-target", "127.0.0.1:45679",
	}, &provision); code != 0 {
		t.Fatalf("provision isolated fixture: %s", provision.String())
	}
	t.Cleanup(func() {
		var output bytes.Buffer
		if code := run([]string{"app", "stop", "--root", root, "--state-dir", state}, &output); code != 0 {
			t.Errorf("stop isolated App fixture: %s", output.String())
		}
	})

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(base, "start")
	resultPath := filepath.Join(base, "result.json")
	caller := exec.Command(os.Args[0], "-test.run=^TestRestrictedJobLifecycleCaller$")
	caller.Env = append(os.Environ(),
		"ASTRORDER_BROKER_TEST_CALLER=1",
		"ASTRORDER_BROKER_TEST_GATE="+gate,
		"ASTRORDER_BROKER_TEST_ROOT="+root,
		"ASTRORDER_BROKER_TEST_STATE="+state,
		"ASTRORDER_BROKER_TEST_RESULT="+resultPath,
	)
	if err := caller.Start(); err != nil {
		t.Fatal(err)
	}
	defer caller.Process.Kill()
	finished := make(chan error, 1)
	go func() { finished <- caller.Wait() }()
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(caller.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, []byte("start"), 0600); err != nil {
		t.Fatal(err)
	}
	var body []byte
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		body, err = os.ReadFile(resultPath)
		if err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("restricted caller exited before returning a result: %v", err)
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	var status struct {
		OK      bool   `json:"ok"`
		State   string `json:"state"`
		Process struct {
			PID int `json:"pid"`
		} `json:"process"`
	}
	if err := json.Unmarshal(body, &status); err != nil || !status.OK || status.State != "running" || status.Process.PID <= 0 {
		t.Fatalf("restricted caller did not start isolated App: body=%s err=%v", body, err)
	}
	if err := windows.TerminateJobObject(job, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("restricted caller survived job termination")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restricted caller did not exit")
	}
	var after bytes.Buffer
	if code := run([]string{"app", "status", "--root", root, "--state-dir", state}, &after); code != 0 {
		t.Fatalf("check isolated App after caller termination: %s", after.String())
	}
	var envelope struct {
		App struct {
			State   string `json:"state"`
			Process struct {
				PID int `json:"pid"`
			} `json:"process"`
		} `json:"app"`
	}
	if err := json.Unmarshal(after.Bytes(), &envelope); err != nil || envelope.App.State != "running" || envelope.App.Process.PID != status.Process.PID {
		t.Fatalf("isolated App did not survive caller job termination: %s err=%v", after.String(), err)
	}
}
