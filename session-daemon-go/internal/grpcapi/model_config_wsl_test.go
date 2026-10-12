//go:build windows

package grpcapi_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/modelconfig"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Opt-in real WSL transport with a freshly built Linux helper and a private
// home. This never resolves or writes the distro user's native configuration.
func TestModelConfigRealWSLHelper(t *testing.T) {
	helper := os.Getenv("ASTRORDER_TEST_WSL_MODEL_HELPER")
	home := os.Getenv("ASTRORDER_TEST_WSL_MODEL_HOME")
	distro := os.Getenv("ASTRORDER_TEST_WSL_MODEL_DISTRO")
	assets := os.Getenv("ASTRORDER_TEST_MODEL_HELPER_ASSETS")
	if helper == "" || home == "" || distro == "" {
		t.Skip("requires explicit Linux helper, private WSL home and distro")
	}
	calls := 0
	f := startModelConfigFixtureWithAssets(t, nil, assets, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		calls++
		if name != "wsl.exe" || len(args) < 4 || args[0] != "-d" || args[1] != distro || args[2] != "--exec" {
			t.Errorf("incorrect WSL helper argv: %q %v", name, args)
		}
		if assets != "" {
			return exec.CommandContext(ctx, name, append([]string{"-d", distro, "--exec", "env", "HOME=" + home}, args[3:]...)...)
		}
		return exec.CommandContext(ctx, name, "-d", distro, "--exec", "env", "HOME="+home, helper)
	})
	target := &sessiondv1.ModelConfigTarget{Target: &sessiondv1.ModelConfigTarget_Wsl{Wsl: &sessiondv1.WslTarget{Distro: distro}}}
	content := []byte("model = \"isolated-wsl-model\"\n")
	expected := modelconfig.EmptySHA256
	file := &sessiondv1.ModelConfigFileInput{File: sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG, Content: content, ExpectedSha256: &expected}
	plan, err := f.client.Plan(f.ctx, &sessiondv1.ModelConfigPlanRequest{Target: target, ProposedFiles: []*sessiondv1.ModelConfigFileInput{file}})
	if err != nil || len(plan.GetChanges()) != 1 || !plan.Changes[0].Changed {
		if err != nil {
			_, detail := f.daemon.ExecuteModelConfig(context.Background(), map[string]any{"action": "model_config.plan", "target": map[string]any{"kind": "wsl", "distro": distro}})
			t.Logf("private WSL fixture failure: %v", detail)
		}
		t.Fatalf("real WSL preview failed: %v, %v", plan, err)
	}
	for _, state := range plan.CurrentFiles {
		if state.File == file.File && state.Exists {
			t.Fatal("WSL fixture home must start empty")
		}
	}
	request := &sessiondv1.ModelConfigApplyRequest{
		Target: target,
		Meta:   &sessiondv1.CommandMeta{OperationId: "isolated-wsl-apply", Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1},
		Files:  []*sessiondv1.ModelConfigFileInput{file},
	}
	applied, err := f.client.Apply(f.ctx, request)
	if err != nil || applied.GetOperationState() != sessiondv1.OperationState_OPERATION_STATE_COMPLETED || len(applied.GetDigests()) != 1 {
		t.Fatalf("real WSL apply failed: %v, %v", applied, err)
	}
	beforeRetry := calls
	retry, err := f.client.Apply(f.ctx, request)
	if err != nil || len(retry.GetDigests()) != 1 || retry.Digests[0].Sha256 != applied.Digests[0].Sha256 || calls != beforeRetry {
		t.Fatalf("real WSL completed retry dispatched or lost receipt: %v, %v", retry, err)
	}
	request.Meta.OperationId = "isolated-wsl-stale"
	_, err = f.client.Apply(f.ctx, request)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("real WSL stale apply did not fail: %v", err)
	}
	readback, err := f.client.Plan(f.ctx, &sessiondv1.ModelConfigPlanRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, state := range readback.CurrentFiles {
		if state.File == file.File {
			found = state.Exists && string(state.Content) == string(content) && state.Sha256 == applied.Digests[0].Sha256
		}
	}
	if !found {
		t.Fatal("real WSL readback does not match applied content and digest")
	}
	if assets != "" {
		check := exec.Command("wsl.exe", "-d", distro, "--", "find", home+"/.local/lib/astrorder/model-helper", "-name", "astrorder-model-helper", "-type", "f")
		output, err := check.Output()
		if err != nil || !strings.Contains(string(output), "/model-helper/") {
			t.Fatalf("bundled helper was not installed in private WSL home: %v", err)
		}
	}
	if entries, err := os.ReadDir(f.home); err != nil || len(entries) != 0 {
		t.Fatalf("real WSL request wrote local configuration home: %v", err)
	}
}
