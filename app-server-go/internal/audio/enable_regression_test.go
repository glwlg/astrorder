package audio

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestEnableRunningWorkerDoesNotDeadlockOrSpawnAgain(t *testing.T) {
	manager, err := newLocalR2T2Manager(Options{
		ResourceDir: t.TempDir(),
		ProcessFactory: func(context.Context, string, ...string) (*exec.Cmd, error) {
			t.Error("running worker must not spawn another model")
			return nil, nil
		},
	}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manager.workerRunning = true
	manager.state = "running"
	manager.cmd = &exec.Cmd{Process: &os.Process{Pid: 1}}
	for _, wait := range []bool{false, true} {
		done := make(chan error, 1)
		go func() { done <- manager.Enable(wait, false) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("re-enabling a running model deadlocked")
		}
	}
}
