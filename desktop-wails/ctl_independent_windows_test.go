//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCtlUpdateCallerProcess(t *testing.T) {
	if os.Getenv("ASTRORDER_CTL_CALLER") != "1" {
		return
	}
	gate := os.Getenv("ASTRORDER_CTL_CALLER_GATE")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(1)
		}
		time.Sleep(10 * time.Millisecond)
	}
	client := &CtlClient{
		CtlPath:  os.Args[0],
		RootDir:  filepath.Dir(gate),
		StateDir: filepath.Dir(gate),
	}
	_, err := client.invokeIndependent(context.Background(), "-test.run=^TestCtlUpdateHelperProcess$")
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestCtlDaemonUpdateSurvivesCallerJobTermination(t *testing.T) {
	base := t.TempDir()
	marker := filepath.Join(base, "finished")
	gate := filepath.Join(base, "start-update")
	t.Setenv("ASTRORDER_CTL_HELPER", "1")
	t.Setenv("ASTRORDER_CTL_HELPER_MARKER", marker)
	t.Setenv("ASTRORDER_CTL_CALLER", "1")
	t.Setenv("ASTRORDER_CTL_CALLER_GATE", gate)

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}

	caller := exec.Command(os.Args[0], "-test.run=^TestCtlUpdateCallerProcess$")
	caller.Dir = base
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
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker + ".started"); err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("caller exited before independent controller started: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("independent controller did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("controller completed before caller termination was exercised")
	}
	if err := windows.TerminateJobObject(job, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("caller was not terminated")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("caller survived job termination")
	}
	for {
		if body, err := os.ReadFile(marker); err == nil && string(body) == "complete" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("independent controller stopped with the caller job")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
