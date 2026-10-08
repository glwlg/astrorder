package process_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/process"
)

func TestOwnedProcessSurvivesOwnerExit(t *testing.T) {
	switch os.Getenv("ASTRORDER_PROCESS_ROLE") {
	case "owner":
		runOwner()
		return
	case "worker":
		runWorker()
		return
	}

	directory := t.TempDir()
	marker := filepath.Join(directory, "alive")
	command := exec.Command(os.Args[0], "-test.run=TestOwnedProcessSurvivesOwnerExit")
	command.Env = append(os.Environ(), "ASTRORDER_PROCESS_ROLE=owner", "ASTRORDER_PROCESS_MARKER="+marker)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pid := waitForPID(t, marker+".pid")
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker+".owner-exited", []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			if !process.Alive(pid) {
				t.Fatal("owned process exited together with its owner")
			}
			waitForExit(t, pid)
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("owned process did not continue after its owner exited")
}

func runOwner() {
	marker := os.Getenv("ASTRORDER_PROCESS_MARKER")
	worker, err := process.Start(context.Background(), process.Spec{
		Path: os.Args[0],
		Args: []string{"-test.run=TestOwnedProcessSurvivesOwnerExit"},
		Env:  []string{"ASTRORDER_PROCESS_ROLE=worker", "ASTRORDER_PROCESS_MARKER=" + marker},
	})
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(marker+".pid", []byte(strconv.Itoa(worker.PID())), 0o600); err != nil {
		panic(err)
	}
	if err := worker.Release(); err != nil {
		panic(err)
	}
}

func runWorker() {
	marker := os.Getenv("ASTRORDER_PROCESS_MARKER")
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker + ".owner-exited"); err == nil {
			if err := os.WriteFile(marker, []byte("alive"), 0o600); err != nil {
				os.Exit(2)
			}
			time.Sleep(2 * time.Second)
			os.Exit(0)
		}
		time.Sleep(30 * time.Millisecond)
	}
	os.Exit(3)
}

func waitForExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if !process.Alive(pid) {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("owned process did not exit after completing its marker work")
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if err == nil {
			pid, _ := strconv.Atoi(string(payload))
			if pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("owner did not start the detached process")
	return 0
}
