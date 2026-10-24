package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	cachedCtlTestCLIPath string
	buildCtlTestCLIOnce  sync.Once
	buildCtlTestCLIErr   error
)

func getCtlTestCLI(t *testing.T) string {
	t.Helper()
	buildCtlTestCLIOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "ctl-test-cli-*")
		if err != nil {
			buildCtlTestCLIErr = err
			return
		}
		src := filepath.Join(tmpDir, "main.go")
		if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
			buildCtlTestCLIErr = err
			return
		}
		exe := filepath.Join(tmpDir, "astrorder-agent-fixture.exe")
		cmd := exec.Command("go", "build", "-o", exe, src)
		if err := cmd.Run(); err != nil {
			buildCtlTestCLIErr = err
			return
		}
		cachedCtlTestCLIPath = filepath.Clean(exe)
	})
	if buildCtlTestCLIErr != nil {
		t.Fatalf("build test CLI fixture: %v", buildCtlTestCLIErr)
	}
	return cachedCtlTestCLIPath
}

func TestAgentSkillCmd_UsageAndUnknown(t *testing.T) {
	var buf bytes.Buffer
	code := run([]string{"agent-skill"}, &buf)
	if code != 1 {
		t.Fatalf("expected exit code 1 for no subcommand, got %d", code)
	}
	var res map[string]any
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error output: %v", err)
	}
	if res["ok"] != false || !strings.Contains(res["error"].(string), "usage: astrorderctl agent-skill") {
		t.Fatalf("unexpected error response: %v", res)
	}

	buf.Reset()
	code = run([]string{"agent-skill", "unknown"}, &buf)
	if code != 1 {
		t.Fatalf("expected exit code 1 for unknown subcommand, got %d", code)
	}
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error output: %v", err)
	}
	if res["ok"] != false || !strings.Contains(res["error"].(string), "unknown agent-skill command") {
		t.Fatalf("unexpected error response: %v", res)
	}
}

func TestAgentSkillCmd_InstallSuccessAndIdempotent(t *testing.T) {
	home := t.TempDir()
	cli := getCtlTestCLI(t)

	var buf bytes.Buffer
	args := []string{
		"agent-skill", "install",
		"--runtime", "hermes",
		"--home", home,
		"--cli", cli,
		"--agent-id", "hermes",
		"--connection-id", "local",
		"--profile-name", "default",
	}

	code := run(args, &buf)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, output: %s", code, buf.String())
	}

	var res map[string]any
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response: %v, output: %s", err, buf.String())
	}
	if res["ok"] != true {
		t.Fatalf("expected ok == true, got: %v", res)
	}
	if res["runtime"] != "hermes" || res["agent_id"] != "hermes" {
		t.Fatalf("unexpected response fields: %v", res)
	}

	// Idempotent rerun
	buf.Reset()
	code = run(args, &buf)
	if code != 0 {
		t.Fatalf("expected exit code 0 on idempotent run, got %d, output: %s", code, buf.String())
	}
	var res2 map[string]any
	if err := json.Unmarshal(buf.Bytes(), &res2); err != nil {
		t.Fatalf("unmarshal response 2: %v", err)
	}
	if res2["ok"] != true {
		t.Fatalf("expected ok == true on idempotent run, got: %v", res2)
	}
}

func TestAgentSkillCmd_InstallErrors(t *testing.T) {
	home := t.TempDir()
	cli := getCtlTestCLI(t)

	// Unexpected positional argument
	var buf bytes.Buffer
	code := run([]string{
		"agent-skill", "install",
		"--runtime", "hermes",
		"--home", home,
		"--cli", cli,
		"--agent-id", "hermes",
		"--connection-id", "local",
		"--profile-name", "default",
		"extra-arg",
	}, &buf)
	if code != 1 {
		t.Fatalf("expected exit code 1 for unexpected arg, got %d", code)
	}
	var res map[string]any
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res["ok"] != false || !strings.Contains(res["error"].(string), "unexpected agent-skill install positional arguments") {
		t.Fatalf("unexpected error response: %v", res)
	}

	// Missing cli
	buf.Reset()
	code = run([]string{
		"agent-skill", "install",
		"--runtime", "hermes",
		"--home", home,
		"--cli", filepath.Join(home, "nonexistent.exe"),
		"--agent-id", "hermes",
		"--connection-id", "local",
		"--profile-name", "default",
	}, &buf)
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing cli, got %d", code)
	}
	var res2 map[string]any
	if err := json.Unmarshal(buf.Bytes(), &res2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res2["ok"] != false || !strings.Contains(res2["error"].(string), "does not exist") {
		t.Fatalf("unexpected error response: %v", res2)
	}
}
