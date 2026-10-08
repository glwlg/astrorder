package grok_test

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/grok"
	"context"
	"os"
	"testing"
)

func TestGrokConfigReloadKeepsExistingHandle(t *testing.T) {
	t.Setenv("TEST_MODE", "fake_acp_basic")
	root := t.TempDir()
	a := grok.New(grok.Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestFakeACP_CreateAndSpawn"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	result, err := a.Create(context.Background(), core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	reloader, ok := any(a).(core.ConfigReloader)
	if !ok {
		t.Fatal("Grok config reloader is not registered")
	}
	if err := reloader.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.Snapshot()[result.SessionID].Status != "idle" {
		t.Fatal("reload changed native ownership")
	}
	if _, err := a.Command(context.Background(), core.Request{SessionID: result.SessionID, Action: "session.models"}); err != nil {
		t.Fatal("reload stopped existing native client", err)
	}
}
