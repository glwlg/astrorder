package grok_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/grok"
)

func TestGrokRemoteControlMetadataSurvivesReattachAndSnapshot(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "calls.log")
	t.Setenv("GROK_ACP_CALL_LOG", logPath)
	t.Setenv("TEST_MODE", "fake_acp_runtime_control")
	adapter := grok.New(grok.Config{Executable: os.Args[0], Arguments: []string{"-test.run=TestFakeACP_RuntimeControlInitializeOnly"}, Workspace: root, Allowed: []string{root}, AgentID: "ssh-grok-saved", AgentName: "Debian · Grok", ConnectionID: "saved"})
	defer adapter.Close()
	request := core.Request{AgentType: "grok", SessionID: "probe-grok-saved", Fields: map[string]any{"cwd": root, "params": map[string]any{"runtime_control": true}}}
	for i := 0; i < 2; i++ {
		result, err := adapter.Spawn(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		models, err := adapter.Command(context.Background(), core.Request{SessionID: request.SessionID, Action: "session.models"})
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []core.Result{result, models, adapter.Snapshot()[request.SessionID]} {
			for key, want := range map[string]string{"connection_id": "saved", "name": "Debian · Grok", "agent_id": "ssh-grok-saved", "runtime_id": "ssh-grok-saved", "source_id": "ssh-grok-saved"} {
				if value.Payload[key] != want {
					t.Fatalf("lost %s: %+v", key, value.Payload)
				}
			}
		}
	}
	calls := readCalls(t, logPath)
	if countCalls(calls, "initialize") != 1 {
		t.Fatalf("restarted transport: %v", calls)
	}
	assertNoBusinessCalls(t, calls)
}
