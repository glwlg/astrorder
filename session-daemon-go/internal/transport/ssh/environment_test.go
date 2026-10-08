package ssh

import (
	"os/exec"
	"strings"
	"testing"
)

func TestProviderBootstrapKeepsSecretsOffCommandLine(t *testing.T) {
	spec := CommandSpec{Executable: "/usr/bin/env", Environment: map[string]string{"MAGPIE_API_KEY": "test-only-credential\nwith newline"}}
	command, err := BuildRemoteCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(command, "test-only-credential") {
		t.Fatal("credential leaked into argv")
	}
	input, err := environmentBootstrap(spec.Environment)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) == 0 {
		t.Fatal("missing bootstrap")
	}
	if _, err := BuildRemoteCommand(CommandSpec{Executable: "env", Environment: map[string]string{"ASTRORDER_BROWSER_SECRET": "control"}}); err == nil {
		t.Fatal("control credential accepted")
	}
	if shell, err := exec.LookPath("sh"); err == nil {
		cmd := exec.Command(shell, "-c", command)
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		cmd.Stdin = strings.NewReader(string(input))
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "MAGPIE_API_KEY=test-only-credential\nwith newline") {
			t.Fatal("bootstrap changed provider value")
		}
	}
}
