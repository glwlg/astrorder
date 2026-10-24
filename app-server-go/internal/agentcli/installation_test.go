package agentcli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallationEnvironmentLoadsOnlyOwnHome(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "skills", "astrorder-browser", "scripts", "astrorder-agent")
	if err := os.WriteFile(filepath.Join(home, "astrorder-transport.json"), []byte(`{"url":"http://host:30001","token":"fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	lookup, err := installationEnvironment(func(string) (string, bool) { return "", false }, exe, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"ASTRORDER_AGENT_URL": "http://host:30001", "ASTRORDER_AGENT_TOKEN": "fixture", "ASTRORDER_CALLER_CONFIG": filepath.Join(home, "astrorder-caller.json")} {
		got, ok := lookup(key)
		if !ok || got != want {
			t.Fatalf("%s missing or incorrect", key)
		}
	}
	if _, ok := lookup("CODEX_THREAD_ID"); ok {
		t.Fatal("session identity must remain runtime supplied")
	}
	override, err := installationEnvironment(func(k string) (string, bool) { return "explicit", k == "ASTRORDER_AGENT_URL" }, exe, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := override("ASTRORDER_AGENT_URL"); value != "explicit" {
		t.Fatal("explicit setting lost")
	}
}
