//go:build windows

package lifecycle

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestInspectExitedProcessWithRetainedHandle(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "exit")
	command := exec.Command(os.Args[0], "-test.run=^TestRetainedProcessHelper$")
	command.Env = append(os.Environ(), "ASTRORDER_RETAINED_PROCESS_GATE="+gate)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err := os.WriteFile(gate, []byte("exit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectProcess(command.Process.Pid); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exited process with retained handle was not identified as gone: %v", err)
	}
}

func TestInspectDoesNotTreatProtectedListedProcessAsGone(t *testing.T) {
	creationTime, listed, err := processSnapshotCreationTime(4)
	if err != nil {
		t.Fatal(err)
	}
	if !listed {
		t.Skip("Windows system process was not listed")
	}
	if _, err := InspectProcess(4); errors.Is(err, os.ErrNotExist) {
		t.Fatal("a listed protected process was mistaken for an exited process")
	}
	if creationTime == 0 {
		t.Fatal("listed protected process has no creation time")
	}
}

func TestReusedProtectedPIDWithoutListenerIsStopped(t *testing.T) {
	creationTime, listed, err := processSnapshotCreationTime(4)
	if err != nil || !listed || creationTime == 0 {
		t.Skipf("Windows system process creation time unavailable: listed=%v err=%v", listed, err)
	}
	if _, err := InspectProcess(4); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Skipf("Windows system process was not protected as expected: %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	identity := ProcessIdentity{PID: 4, ExecutablePath: executable, CreationTime: creationTime + 1}
	state := t.TempDir()
	if err := writeDaemonProcessMetadata(state, identity); err != nil {
		t.Fatal(err)
	}
	address := unusedLoopbackAddress(t)
	options := validatedDaemonOptions{DaemonOptions: DaemonOptions{
		StateDir: state, Port: mustPort(t, address), ExecutablePath: executable,
	}}
	status, err := daemonStatus(context.Background(), options)
	if err != nil || status.State != StateStopped {
		t.Fatalf("reused protected PID was not stopped: status=%+v err=%v", status, err)
	}
	identity.CreationTime = creationTime
	if err := writeDaemonProcessMetadata(state, identity); err != nil {
		t.Fatal(err)
	}
	status, err = daemonStatus(context.Background(), options)
	if err != nil || status.State != StateConflict {
		t.Fatalf("inaccessible process with matching creation time was not a conflict: status=%+v err=%v", status, err)
	}
}

func TestReusedPIDWithoutListenerIsStopped(t *testing.T) {
	current, err := InspectProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	stale := current
	stale.CreationTime++
	address := unusedLoopbackAddress(t)
	port := mustPort(t, address)
	state := t.TempDir()
	if err := writeDaemonProcessMetadata(state, stale); err != nil {
		t.Fatal(err)
	}
	daemonOptions := validatedDaemonOptions{DaemonOptions: DaemonOptions{
		StateDir: state, Port: port, ExecutablePath: current.ExecutablePath,
	}}
	status, err := daemonStatus(context.Background(), daemonOptions)
	if err != nil || status.State != StateStopped {
		t.Fatalf("reused daemon PID was not stopped: status=%+v err=%v", status, err)
	}

	if err := writeProcessMetadata(state, stale); err != nil {
		t.Fatal(err)
	}
	appOptions := validatedAppOptions{AppOptions: AppOptions{
		StateDir: state, Address: address, ExecutablePath: current.ExecutablePath,
	}, port: port}
	app, err := appStatus(context.Background(), appOptions)
	if err != nil || app.State != StateStopped {
		t.Fatalf("reused App PID was not stopped: status=%+v err=%v", app, err)
	}

	listener, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	status, err = daemonStatus(context.Background(), daemonOptions)
	if err != nil || status.State != StateConflict {
		t.Fatalf("foreign listener was not a conflict: status=%+v err=%v", status, err)
	}
	listener.Close()
	if err := writeDaemonProcessMetadata(state, current); err != nil {
		t.Fatal(err)
	}
	status, err = daemonStatus(context.Background(), daemonOptions)
	if err != nil || status.State != StateConflict {
		t.Fatalf("live saved daemon process without a listener was not a conflict: status=%+v err=%v", status, err)
	}
}

func TestRetainedProcessHelper(t *testing.T) {
	gate := os.Getenv("ASTRORDER_RETAINED_PROCESS_GATE")
	if gate == "" {
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(gate); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("parent did not release the process")
}
