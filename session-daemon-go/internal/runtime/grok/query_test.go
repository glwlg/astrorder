package grok

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"testing"
	"time"
)

func TestACPInitializeQueryNeverCreatesNativeSession(t *testing.T) {
	t.Setenv("ASTRORDER_ACP_FAILURE_FIXTURE", "1")
	root := t.TempDir()
	a := New(Config{Executable: os.Args[0], Arguments: []string{"-test.run=^TestACPFailureFixture$"}, Workspace: root, Allowed: []string{root}})
	defer a.Close()
	r := core.NewRegistry()
	r.Register("grok", a)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := r.Query(ctx, core.Request{AgentType: "grok", Fields: map[string]any{"method": "initialize", "params": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	if result["protocolVersion"] == nil {
		t.Fatal("native initialize result missing", result)
	}
	if len(a.Snapshot()) != 0 || len(r.Sessions()) != 0 || len(a.clients) != 0 {
		t.Fatal("read-only initialization retained runtime ownership")
	}
	if _, err := r.Query(ctx, core.Request{AgentType: "grok", Fields: map[string]any{"method": "session/new"}}); err == nil {
		t.Fatal("unregistered runtime method created hidden native session")
	}
}
