package modelconfig

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/process"
)

// Re-exec keeps the real helper's home private even though the transport runner
// replaces its child's environment with the native process environment.
func TestModelConfigHelperSubprocess(t *testing.T) {
	if len(os.Args) < 5 || os.Args[len(os.Args)-3] != "--model-config-helper-fixture" {
		t.Skip("only invoked by the remote helper fixture")
	}
	helper, home := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	cmd := exec.Command(helper)
	cmd.Env = process.NativeEnvironment([]string{"HOME=" + home, "USERPROFILE=" + home})
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
	os.Exit(0)
}

func buildModelConfigHelper(t *testing.T) string {
	t.Helper()
	name, goName := "astrorder-model-helper", "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
		goName += ".exe"
	}
	helper := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", goName), "build", "-o", helper, "../../cmd/astrorder-model-helper")
	cmd.Env = process.NativeEnvironment([]string{"GOPROXY=off"})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build real Go helper: %v\n%s", err, out)
	}
	return helper
}

func remoteHelperFixture(t *testing.T, helper, home string) *Service {
	t.Helper()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{
		AllowedRoots: []string{t.TempDir()},
		RemoteCommandRunner: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name != "ssh" && name != "wsl.exe" {
				t.Errorf("unexpected transport executable %q", name)
			}
			if name == "ssh" && len(args) > 0 && args[0] == name {
				t.Error("SSH argv repeats the executable as a positional argument")
			}
			if len(args) == 0 || !strings.Contains(args[len(args)-1], "astrorder-model-helper") {
				t.Errorf("transport omitted helper: %v", args)
			}
			return exec.CommandContext(ctx, testBinary, "-test.run=^TestModelConfigHelperSubprocess$", "--", "--model-config-helper-fixture", helper, home)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func remoteModelConfigTargets() map[string]map[string]any {
	return map[string]map[string]any{
		"ssh": {"kind": "ssh", "settings": map[string]any{"host": "fixture.invalid", "port": 2222, "user": "fixture"}},
		"wsl": {"kind": "wsl", "distro": "Fixture"},
	}
}

func TestRemoteModelConfigPreviewIncludesChanges(t *testing.T) {
	helper := buildModelConfigHelper(t)
	for name, target := range remoteModelConfigTargets() {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, RelCodexConfig)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			original := "model = \"fixture-original\"\n"
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			svc := remoteHelperFixture(t, helper, home)
			res, err := svc.Execute(context.Background(), "model_config.plan", map[string]any{
				"target":           target,
				"files":            map[string]any{FileCodexConfig: "model = \"fixture-updated\"\n"},
				"expected_digests": map[string]any{FileCodexConfig: sha256Hex([]byte(original))},
			})
			if err != nil {
				t.Fatal(err)
			}
			changes, ok := res["changes"].([]any)
			if !ok || len(changes) != 1 {
				t.Fatalf("remote preview lost proposed changes: %v", res["changes"])
			}
			change, _ := changes[0].(map[string]any)
			if change["file"] != FileCodexConfig || change["changed"] != true || change["current_sha256"] != sha256Hex([]byte(original)) {
				t.Fatalf("incorrect remote preview change: %v", change)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != original {
				t.Fatalf("preview mutated target: %v", err)
			}
			if entries, err := os.ReadDir(svc.allowedRoots[0]); err != nil || len(entries) != 0 {
				t.Fatalf("remote preview touched local root: %v", err)
			}
		})
	}
}

func TestRemoteModelConfigApplyPreservesExpectedDigest(t *testing.T) {
	helper := buildModelConfigHelper(t)
	for name, target := range remoteModelConfigTargets() {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, RelCodexConfig)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			original := "model = \"fixture-original\"\n"
			updated := "model = \"fixture-updated\"\n"
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			svc := remoteHelperFixture(t, helper, home)
			request := map[string]any{
				"target":           target,
				"files":            map[string]any{FileCodexConfig: updated},
				"expected_digests": map[string]any{FileCodexConfig: EmptySHA256},
			}
			_, err := svc.Execute(context.Background(), "model_config.apply", request)
			if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
				t.Fatalf("remote apply ignored stale expected digest: %v", err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != original {
				t.Fatalf("rejected apply changed target: %v", err)
			}
			request["expected_digests"] = map[string]any{FileCodexConfig: sha256Hex([]byte(original))}
			res, err := svc.Execute(context.Background(), "model_config.apply", request)
			if err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != updated {
				t.Fatalf("successful apply did not update exact target: %v", err)
			}
			receipt, _ := res["receipt"].(map[string]any)
			digests, _ := receipt["digests"].(map[string]any)
			if digests[FileCodexConfig] != sha256Hex([]byte(updated)) {
				t.Fatalf("remote apply lost digest receipt: %v", digests)
			}
			readback, err := svc.Execute(context.Background(), "model_config.plan", map[string]any{"target": target})
			if err != nil {
				t.Fatal(err)
			}
			state, _ := readback[FileCodexConfig].(map[string]any)
			if state["sha256"] != digests[FileCodexConfig] || state["content"] != updated {
				t.Fatal("remote readback does not match applied content and digest")
			}
			if entries, err := os.ReadDir(svc.allowedRoots[0]); err != nil || len(entries) != 0 {
				t.Fatalf("remote apply touched local root: %v", err)
			}
		})
	}
}
