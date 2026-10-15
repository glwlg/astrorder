package connections

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemoteVersionFindsAgentOutsideNoninteractivePATH(t *testing.T) {
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	home := filepath.ToSlash(t.TempDir())
	for kind, path := range map[string]string{"hermes": "/.hermes/hermes-agent/venv/bin/hermes", "grok": "/.grok/bin/grok", "codex": "/.local/bin/codex"} {
		t.Run(kind, func(t *testing.T) {
			full := filepath.FromSlash(home + path)
			if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("#!/bin/sh\nprintf '%s\\n' '"+kind+" 1.2.3'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(sh, "--noprofile", "--norc", "-c", remoteAgentSetup(kind)+`exec "$p" --version`)
			cmd.Env = append(os.Environ(), "HOME="+home, "PATH=/usr/bin:/bin")
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != kind+" 1.2.3" {
				t.Fatalf("noninteractive %s probe: %v %s", kind, err, out)
			}
		})
	}
}

func TestUpgradeProcessStreamsRealExitAndCancellation(t *testing.T) {
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	var output bytes.Buffer
	code, err := runUpgradeProcess(context.Background(), exec.Command(sh, "--noprofile", "--norc", "-c", "printf 'stdout'; printf 'stderr' >&2; exit 7"), &output)
	if err != nil || code != 7 || !strings.Contains(output.String(), "stdout") || !strings.Contains(output.String(), "stderr") {
		t.Fatalf("code=%d err=%v output=%q", code, err, output.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, err = runUpgradeProcess(ctx, exec.CommandContext(ctx, sh, "-c", "exit 0"), &output)
	if code != -1 || err != context.Canceled {
		t.Fatalf("canceled updater returned %d %v", code, err)
	}
}

func TestLocalUpdaterResolvesNativeLauncher(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launcher layout")
	}
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", home)
	t.Setenv("PATH", t.TempDir())
	path := filepath.Join(home, "hermes", "bin", "hermes.exe")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := localAgentExecutable(context.Background(), "hermes")
	if err != nil || got != path {
		t.Fatalf("launcher resolution: %q %v", got, err)
	}
}
