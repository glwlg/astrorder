package kernelcontrol

import (
	"context"
	"errors"
	"testing"
)

func TestAppRestartRecoversConfirmedStoppedService(t *testing.T) {
	s := &Service{StateDir: t.TempDir(), Launch: func(string) error { return nil }}
	op, _ := s.Start("app", "restart", true)
	stopped := false
	transientInspection := true
	started := false
	s.Run = func(_ context.Context, target, action string) (map[string]any, error) {
		if action == "restart" {
			stopped = true
			return nil, errors.New("verify App process exit: Access is denied")
		}
		if action == "start" {
			started = true
			stopped = false
			return map[string]any{}, nil
		}
		pid := 1
		if started {
			pid = 2
		}
		if stopped {
			if transientInspection {
				transientInspection = false
				return nil, errors.New("inspect saved App process: Access is denied")
			}
			return map[string]any{"state": "stopped", "identity_verified": false, "port_owned": false}, nil
		}
		return map[string]any{"state": "running", "healthy": true, "identity_verified": true, "port_owned": true, "process": map[string]any{"pid": pid}}, nil
	}
	if err := s.Execute(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("App left stopped")
	}
}
func TestAppRestartNeverStartsOverConflictingProcess(t *testing.T) {
	s := &Service{StateDir: t.TempDir(), Launch: func(string) error { return nil }}
	op, _ := s.Start("app", "restart", true)
	calls := 0
	s.Run = func(_ context.Context, _, action string) (map[string]any, error) {
		if action == "restart" {
			return nil, errors.New("stop failed")
		}
		if action == "start" {
			t.Error("started over conflict")
		}
		calls++
		state := "running"
		if calls > 1 {
			state = "conflict"
		}
		return map[string]any{"state": state, "identity_verified": true, "port_owned": true}, nil
	}
	if err := s.Execute(context.Background(), op.ID); err == nil {
		t.Fatal("conflict reported success")
	}
}
