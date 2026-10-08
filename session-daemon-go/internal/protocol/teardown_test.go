package protocol

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type teardownFixture struct {
	daemon *Daemon
	called int
}

func (a *teardownFixture) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "teardown", Status: "idle"}, nil
}
func (a *teardownFixture) Close() error {
	a.called++
	if err := a.daemon.Emit("teardown", "fixture.closed", map[string]any{}, "idle"); err != nil {
		return errors.New("journal was closed before native cleanup")
	}
	return errors.New("fixture cleanup error")
}
func TestDaemonCloseCleansOwnedRuntimesBeforeJournal(t *testing.T) {
	d, err := Open("isolated", filepath.Join(t.TempDir(), "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := &teardownFixture{daemon: d}
	d.RegisterRuntime("fixture", a)
	err = d.Close()
	if err == nil || err.Error() != "fixture cleanup error" || a.called != 1 {
		t.Fatal("owned runtime teardown missing or wrong order", err, a.called)
	}
	if second := d.Close(); second == nil || a.called != 1 {
		t.Fatal("daemon close is not idempotent", second, a.called)
	}
}
