package toolchain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/store"
)

type fakeOnboardingInjector struct {
	called    bool
	envID     string
	agentID   string
	agentKind string
}

func (f *fakeOnboardingInjector) InjectForOnboarding(_ context.Context, envID, agentID, agentKind string) (bool, []string, []string) {
	f.called = true
	f.envID = envID
	f.agentID = agentID
	f.agentKind = agentKind
	return true, []string{"astrorder-sessions", "astrorder-workspace"}, nil
}

func TestConfigureOnboardingSkipKeyAndAgentLLM(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	codexDir := filepath.Join(tmp, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existingAuth := `{"tokens":{"access_token":"keep-oauth-token"}}`
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(existingAuth), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	injector := &fakeOnboardingInjector{}
	svc := NewService(Options{
		Store:              st,
		StateDir:           filepath.Join(tmp, "state"),
		DefaultWorkspace:   filepath.Join(tmp, "AstrorderWorkspace"),
		OnboardingInjector: injector,
	})
	defer svc.Close(context.Background())

	res, err := svc.ConfigureOnboarding(context.Background(), OnboardingConfigureRequest{
		EnvironmentID:      "local",
		AgentKind:          "codex",
		AgentID:            "codex-local",
		Model:              "gpt-5.4",
		SkipKey:            true,
		InjectCapabilities: true,
	})
	if err != nil {
		t.Fatalf("ConfigureOnboarding skip_key failed: %v", err)
	}
	if !res.OK || res.CredentialsWritten {
		t.Fatalf("expected ok=true credentials_written=false, got %+v", res)
	}
	if res.UniversalLLMMode != "agent" {
		t.Fatalf("expected universal_llm_mode=agent, got %+v", res)
	}
	if !res.CLIReady || len(res.InjectedSkills) != 2 || !injector.called {
		t.Fatalf("expected capabilities injected, got res=%+v injector=%+v", res, injector)
	}
	// Existing OAuth auth.json must remain untouched
	rawAuth, err := os.ReadFile(filepath.Join(codexDir, "auth.json"))
	if err != nil || string(rawAuth) != existingAuth {
		t.Fatalf("auth.json should not be overwritten when skip_key=true: %q err=%v", string(rawAuth), err)
	}
	// Default universal LLM should be configured with protocol=agent
	resolved := aiprovider.ResolveDefault(context.Background(), st)
	if !resolved.IsConfigured() || resolved.Protocol != llmcompat.ProtocolAgent || resolved.AgentID != "codex-local" || resolved.Model != "gpt-5.4" {
		t.Fatalf("unexpected resolved universal LLM: %+v", resolved)
	}
}

func TestConfigureOnboardingWithAPIKeyWritesCodexConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := NewService(Options{
		Store:            st,
		StateDir:         filepath.Join(tmp, "state"),
		DefaultWorkspace: filepath.Join(tmp, "AstrorderWorkspace"),
	})
	defer svc.Close(context.Background())

	res, err := svc.ConfigureOnboarding(context.Background(), OnboardingConfigureRequest{
		EnvironmentID: "local",
		AgentKind:     "codex",
		AgentID:       "codex-local",
		APIKey:        "sk-custom-onboarding-key",
		BaseURL:       "https://api.deepseek.com/v1",
		ProviderName:  "DeepSeek",
		Model:         "deepseek-chat",
		SkipKey:       false,
	})
	if err != nil {
		t.Fatalf("ConfigureOnboarding with key failed: %v", err)
	}
	if !res.OK || !res.CredentialsWritten || res.UniversalLLMMode != "api_key" {
		t.Fatalf("unexpected result: %+v", res)
	}

	rawAuth, err := os.ReadFile(filepath.Join(tmp, ".codex", "auth.json"))
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var authDoc map[string]any
	if err := json.Unmarshal(rawAuth, &authDoc); err != nil || authDoc["OPENAI_API_KEY"] != "sk-custom-onboarding-key" {
		t.Fatalf("unexpected auth.json: %s", string(rawAuth))
	}

	rawCfg, err := os.ReadFile(filepath.Join(tmp, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	cfgText := string(rawCfg)
	if !strings.Contains(cfgText, "https://api.deepseek.com/v1") || !strings.Contains(cfgText, `model = "deepseek-chat"`) {
		t.Fatalf("unexpected config.toml:\n%s", cfgText)
	}

	resolved := aiprovider.ResolveDefault(context.Background(), st)
	if !resolved.IsConfigured() || resolved.Protocol != llmcompat.ProtocolOpenAI || resolved.BaseURL != "https://api.deepseek.com/v1" || resolved.APIKey != "sk-custom-onboarding-key" {
		t.Fatalf("unexpected resolved LLM: %+v", resolved)
	}
}
