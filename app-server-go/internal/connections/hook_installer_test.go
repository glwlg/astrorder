package connections

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeHelperRunner struct {
	output []byte
	err    error
	conn   string
	args   []string
}

func (f *fakeHelperRunner) RunHelper(_ context.Context, connectionID string, args ...string) ([]byte, error) {
	f.conn, f.args = connectionID, args
	return f.output, f.err
}

func TestHookInstallerRunsInstallHooksOnRemoteAndToleratesShellNoise(t *testing.T) {
	runner := &fakeHelperRunner{output: []byte("Welcome to zsh\n{\"ok\":true,\"command\":\"/h observe --spool /s\",\"rewrite\":{\"files\":2,\"handlers\":20},\"trust\":{\"hooks\":10,\"trusted\":10,\"changed\":10}}\n")}
	installer := &NativeHookInstaller{Resolver: runner}
	result, err := installer.Install(context.Background(), map[string]any{"kind": "codex", "connection_id": "ssh-1"})
	if err != nil || !result.OK || result.Trust == nil || result.Trust.Trusted != 10 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if runner.conn != "ssh-1" || len(runner.args) != 1 || runner.args[0] != "install-hooks" {
		t.Fatalf("remote call = %q %v", runner.conn, runner.args)
	}
}

func TestHookInstallerRejectsNonCodexAndOldHelpers(t *testing.T) {
	installer := &NativeHookInstaller{Resolver: &fakeHelperRunner{output: []byte("astrorder-helper: --stdio is required\n")}}
	if _, err := installer.Install(context.Background(), map[string]any{"kind": "hermes", "connection_id": "ssh-1"}); err == nil {
		t.Fatal("only Codex agents have hooks to install")
	}
	_, err := installer.Install(context.Background(), map[string]any{"kind": "codex", "connection_id": "ssh-1"})
	if err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("an old helper without install-hooks must give a clear error, got %v", err)
	}
	failing := &NativeHookInstaller{Resolver: &fakeHelperRunner{err: errors.New("ssh down")}}
	if _, err := failing.Install(context.Background(), map[string]any{"kind": "codex", "connection_id": "ssh-1"}); err == nil {
		t.Fatal("transport failures must surface")
	}
}

func TestHookInstallerLocalNeedsHelperBinary(t *testing.T) {
	installer := &NativeHookInstaller{LocalHelper: "Z:/definitely/missing/astrorder-helper.exe"}
	if _, err := installer.Install(context.Background(), map[string]any{"kind": "codex"}); err == nil {
		t.Fatal("a missing local helper must be an explicit error, not hooks pointing at nothing")
	}
}
