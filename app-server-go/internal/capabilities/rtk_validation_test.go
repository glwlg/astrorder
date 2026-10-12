package capabilities

import (
	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/envtarget/envtargettest"
	"context"
	"strings"
	"testing"
)

func TestRTKRejectsMalformedHermesConfigBeforeRunningCLI(t *testing.T) {
	env := newEnv("a")
	original := []byte("plugins:\n  enabled: []\n_left_core_installed:\n  - example\n  opencodex:\n    base_url: example\n")
	env.Files[env.P.Hermes+"/config.yaml"] = original
	calls := 0
	env.Run_ = func(c envtarget.Command, e *envtargettest.Env) (envtarget.Result, error) {
		calls++
		return envtarget.Result{}, nil
	}
	_, err := serviceFor(env).SetRTKAgent(context.Background(), "a", "hermes", true, "7")
	if err == nil || !strings.Contains(err.Error(), "配置无法解析") {
		t.Fatalf("expected configuration error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("ran CLI %d times on malformed config", calls)
	}
	if string(env.Files[env.P.Hermes+"/config.yaml"]) != string(original) {
		t.Fatal("changed config")
	}
	state := rtkAgentState("hermes", env.P, env.Files)
	if !strings.Contains(state.Detail, "配置无法解析") {
		t.Fatalf("hidden parse failure: %+v", state)
	}
}
