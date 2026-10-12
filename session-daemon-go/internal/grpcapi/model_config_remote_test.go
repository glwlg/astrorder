package grpcapi_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/modelconfig"
	"astrorder.dev/session-daemon/internal/process"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRemoteModelHelperChild(t *testing.T) {
	if len(os.Args) < 5 || os.Args[len(os.Args)-3] != "--remote-model-helper" {
		t.Skip("subprocess entry only")
	}
	cmd := exec.Command(os.Args[len(os.Args)-2])
	cmd.Env = process.NativeEnvironment([]string{"HOME=" + os.Args[len(os.Args)-1], "USERPROFILE=" + os.Args[len(os.Args)-1]})
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
	os.Exit(0)
}

func buildRemoteModelHelper(t *testing.T) string {
	t.Helper()
	goName, helperName := "go", "astrorder-model-helper"
	if runtime.GOOS == "windows" {
		goName += ".exe"
		helperName += ".exe"
	}
	path := filepath.Join(t.TempDir(), helperName)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", goName), "build", "-o", path, "../../cmd/astrorder-model-helper")
	cmd.Env = process.NativeEnvironment([]string{"GOPROXY=off"})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build native model helper: %v\n%s", err, out)
	}
	return path
}

func realModelHelperRunner(t *testing.T, helper, home string, calls *int) func(context.Context, string, ...string) *exec.Cmd {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		*calls++
		if name != "ssh" && name != "wsl.exe" {
			t.Errorf("unexpected remote transport executable %q", name)
		}
		if len(args) == 0 || !strings.Contains(args[len(args)-1], "astrorder-model-helper") {
			t.Errorf("missing helper command: %v", args)
		}
		return exec.CommandContext(ctx, binary, "-test.run=^TestRemoteModelHelperChild$", "--", "--remote-model-helper", helper, home)
	}
}

func TestModelConfigRemoteHelperPlanApply(t *testing.T) {
	helper := buildRemoteModelHelper(t)
	targets := map[string]*sessiondv1.ModelConfigTarget{
		"ssh": {Target: &sessiondv1.ModelConfigTarget_Ssh{Ssh: &sessiondv1.SshTarget{Config: &sessiondv1.SshConnectConfig{Host: "fixture.invalid", Port: 2222}}}},
		"wsl": {Target: &sessiondv1.ModelConfigTarget_Wsl{Wsl: &sessiondv1.WslTarget{Distro: "Fixture"}}},
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, modelconfig.RelCodexConfig)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("model = \"fixture-original\"\n")
			updated := []byte("model = \"fixture-updated\"\n")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			f := startModelConfigFixture(t, nil, realModelHelperRunner(t, helper, home, &calls))
			expected := fmt.Sprintf("%x", sha256.Sum256(original))
			file := &sessiondv1.ModelConfigFileInput{File: sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG, Content: updated, ExpectedSha256: &expected}
			plan, err := f.client.Plan(f.ctx, &sessiondv1.ModelConfigPlanRequest{Target: target, ProposedFiles: []*sessiondv1.ModelConfigFileInput{file}})
			if err != nil || len(plan.GetChanges()) != 1 || !plan.Changes[0].Changed || plan.Changes[0].CurrentSha256 != expected {
				t.Fatalf("remote gRPC preview lost change or digest: %v, %v", plan, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != string(original) {
				t.Fatalf("remote gRPC preview wrote target: %v", err)
			}
			stale := modelconfig.EmptySHA256
			_, err = f.client.Apply(f.ctx, &sessiondv1.ModelConfigApplyRequest{
				Target: target,
				Meta:   &sessiondv1.CommandMeta{OperationId: "remote-stale", Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1},
				Files:  []*sessiondv1.ModelConfigFileInput{{File: file.File, Content: updated, ExpectedSha256: &stale}},
			})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("stale remote apply should fail: %v", err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != string(original) {
				t.Fatalf("stale remote apply wrote target: %v", err)
			}
			request := &sessiondv1.ModelConfigApplyRequest{
				Target: target,
				Meta:   &sessiondv1.CommandMeta{OperationId: "remote-apply", Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1},
				Files:  []*sessiondv1.ModelConfigFileInput{file},
			}
			applied, err := f.client.Apply(f.ctx, request)
			if err != nil || applied.GetOperationState() != sessiondv1.OperationState_OPERATION_STATE_COMPLETED || len(applied.GetDigests()) != 1 {
				t.Fatalf("remote apply missing completed receipt: %v, %v", applied, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != string(updated) {
				t.Fatalf("remote apply failed exact readback: %v", err)
			}
			beforeRetry := calls
			retry, err := f.client.Apply(f.ctx, request)
			if err != nil || len(retry.GetDigests()) != 1 || retry.Digests[0].Sha256 != applied.Digests[0].Sha256 || calls != beforeRetry {
				t.Fatalf("completed retry lost digest or dispatched again: %v, %v, calls=%d/%d", retry, err, calls, beforeRetry)
			}
			readback, err := f.client.Plan(f.ctx, &sessiondv1.ModelConfigPlanRequest{Target: target})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, state := range readback.CurrentFiles {
				if state.File == file.File {
					found = state.Exists && string(state.Content) == string(updated) && state.Sha256 == applied.Digests[0].Sha256
				}
			}
			if !found {
				t.Fatal("remote gRPC readback does not match applied file and receipt")
			}
			if entries, err := os.ReadDir(f.home); err != nil || len(entries) != 0 {
				t.Fatalf("remote request wrote local configuration home: %v", err)
			}
		})
	}
}
