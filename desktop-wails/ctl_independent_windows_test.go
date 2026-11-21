//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCtlUpdateCallerProcess(t *testing.T) {
	gate := os.Getenv("ASTRORDER_CTL_CALLER_GATE")
	if gate == "" {
		return
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("caller start timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	client := &CtlClient{CtlPath: os.Getenv("ASTRORDER_TEST_CTL"), RootDir: filepath.Dir(gate), StateDir: filepath.Join(filepath.Dir(gate), "state")}
	_, err := client.Run(context.Background(), "daemon", "update", false)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCtlDaemonUpdateSurvivesCallerJobTermination(t *testing.T) {
	ctl, daemon := os.Getenv("ASTRORDER_TEST_CTL"), os.Getenv("ASTRORDER_TEST_SESSIOND")
	if ctl == "" || daemon == "" {
		t.Skip("requires isolated Windows controller and daemon artifacts")
	}
	root := t.TempDir()
	state := filepath.Join(root, "state")
	bin := filepath.Join(root, "bin")
	dist := filepath.Join(root, "frontend", "dist")
	for _, dir := range []string{bin, dist, state} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// This installation needs only its own PTY runtime.
	config, err := json.Marshal(map[string]any{
		"runtimes":     []map[string]any{{"type": "pty", "allowed": []string{root}}},
		"model_config": map[string]any{"home": root},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "session-daemon.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(bin, "astrorder-sessiond.exe")
	app := filepath.Join(bin, "astrorder-app.exe")
	for _, path := range []string{old, app, filepath.Join(dist, "index.html")} {
		if err := os.WriteFile(path, []byte("isolated fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	candidate, err := os.ReadFile(daemon)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(old+".new", candidate, 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	out, err := exec.Command(ctl, "provision", "--root", root, "--state-dir", state, "--data-dir", filepath.Join(state, "data"), "--app-executable", app, "--grpc-target", addr).CombinedOutput()
	if err != nil {
		t.Fatalf("provision disposable installation: %s: %v", out, err)
	}
	t.Cleanup(func() {
		if out, err := exec.Command(ctl, "daemon", "stop", "--root", root, "--state-dir", state).CombinedOutput(); err != nil {
			t.Errorf("clean up disposable daemon: %s: %v", out, err)
		}
	})
	// Keep the replacement pending until the desktop and its CLI have exited.
	held, err := os.Open(old)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	gate := filepath.Join(root, "start-update")
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	caller := exec.Command(os.Args[0], "-test.run=^TestCtlUpdateCallerProcess$")
	caller.Dir = root
	caller.Env = append(os.Environ(), "ASTRORDER_CTL_CALLER_GATE="+gate)
	caller.Stdout = os.Stdout
	caller.Stderr = os.Stderr
	if err = caller.Start(); err != nil {
		t.Fatal(err)
	}
	defer caller.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- caller.Wait() }()
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(caller.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err = windows.AssignProcessToJobObject(job, handle); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(gate, []byte("start"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		backups, err := filepath.Glob(old + ".backup-*")
		if err != nil {
			t.Fatal(err)
		}
		if len(backups) > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("caller exited before handoff: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("update did not reach replacement")
		}
		time.Sleep(15 * time.Millisecond)
	}
	if err = windows.TerminateJobObject(job, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected caller termination")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("caller still running")
	}
	if err = held.Close(); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(old + ".new"); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("update did not finish after caller exited")
		}
		time.Sleep(50 * time.Millisecond)
	}
	raw, err := exec.Command(ctl, "service", "daemon", "status", "--root", root, "--state-dir", state).Output()
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		State            string
		IdentityVerified bool `json:"identity_verified"`
		PortOwned        bool `json:"port_owned"`
		Port             int
		Process          struct {
			Path string `json:"executable_path"`
		}
	}
	if json.Unmarshal(raw, &status) != nil || status.State != "running" || !status.IdentityVerified || !status.PortOwned || status.Process.Path != old || fmt.Sprintf("127.0.0.1:%d", status.Port) != addr {
		t.Fatalf("updated fixture has invalid status: %s", raw)
	}
}
