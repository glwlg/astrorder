package agentskill

import (
	"encoding/json"
	"os"
	"testing"
)

func TestFreshInstallGrokBindsOnlyDestinationStaticIdentity(t *testing.T) {
	home := t.TempDir()
	result, err := Install(InstallOptions{Runtime: "grok", Home: home, CLI: getTestCLI(t), AgentID: "grok-main", ConnectionID: "local", ProfileName: "default"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(result.CallerConfig)
	if err != nil {
		t.Fatal(err)
	}
	var caller map[string]any
	if err := json.Unmarshal(raw, &caller); err != nil {
		t.Fatal(err)
	}
	if len(caller) != 4 || caller["runtime"] != "grok" || caller["agent_id"] != "grok-main" {
		t.Fatalf("incorrect caller binding: keys=%d", len(caller))
	}
	if _, err := Install(InstallOptions{Runtime: "grok", Home: home, CLI: getTestCLI(t), AgentID: "grok-main", ConnectionID: "local", ProfileName: "default"}); err != nil {
		t.Fatal("idempotent Grok installation failed:", err)
	}
}
