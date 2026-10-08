package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"testing"
)

func TestConfigReloadPreservesExistingSessionTransports(t *testing.T) {
	root := t.TempDir()
	a := NewAdapter(Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestAdapterOwnsMultipleNativeSessions"}, Environment: []string{"ASTRORDER_ADAPTER_HELPER=1"}, Workspace: root, Allowed: []string{root}}, nil)
	defer a.Close()
	first, err := a.Create(context.Background(), core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	rt := a.sessions[first.SessionID].runtime
	before := rt.command.Process.Pid
	reloader, ok := any(a).(core.ConfigReloader)
	if !ok {
		t.Fatal("native configuration reloader is not registered")
	}
	if err := reloader.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.command.Process.Pid != before || rt.Status() != "idle" {
		t.Fatal("configuration reload replaced existing session")
	}
	next, err := a.Create(context.Background(), core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if next.SessionID == first.SessionID {
		t.Fatal("new session reused cached native process")
	}
	if _, err := a.Command(context.Background(), core.Request{SessionID: first.SessionID, Action: "session.models"}); err != nil {
		t.Fatal("existing transport was stopped", err)
	}
}
