package aiprovider

import (
	"context"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "providers.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSeedFromLegacySourcesIncludesLLMGatewayAndMeetingCustom(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if err := st.SetPreference(ctx, "services:llm", map[string]any{
		"base_url":  "https://api.deepseek.com/v1",
		"api_key":   "sk-deepseek-1234567890",
		"model":     "deepseek-chat",
		"reasoning": "medium",
	}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{
		"gateway_type":   "magpie",
		"management_url": "https://magpie.internal",
		"inference_url":  "https://magpie.internal/v1",
		"api_key":        "mp-secret-1234567890",
		"management_key": "mgmt-k-9999",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "meetings:settings", map[string]any{
		"model_source": "custom",
		"base_url":     "https://magpie.internal/v1",
		"api_key":      "mp-secret-1234567890",
		"model":        "guolian-llm-max",
	}, true); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("expected 2 merged providers (DeepSeek + Magpie), got %d: %+v", len(cfg.Providers), cfg.Providers)
	}
	if cfg.DefaultModel != "deepseek-chat" || cfg.DefaultReasoning != "medium" {
		t.Fatalf("unexpected default: %+v", cfg)
	}

	var magpie *Provider
	for i := range cfg.Providers {
		if cfg.Providers[i].Vendor == VendorMagpie {
			magpie = &cfg.Providers[i]
		}
	}
	if magpie == nil {
		t.Fatalf("expected magpie vendor provider to be seeded: %+v", cfg.Providers)
	}
	if magpie.ManagementURL != "https://magpie.internal" || magpie.ManagementKey != "mgmt-k-9999" {
		t.Fatalf("unexpected magpie fields: %+v", magpie)
	}
	if len(magpie.Models) != 1 || magpie.Models[0] != "guolian-llm-max" {
		t.Fatalf("expected custom meeting model merged into matching magpie provider, got %+v", magpie.Models)
	}
}

func TestUpsertDefaultAndGatewayBidirectionalSync(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	gemKey := "AIza-secret-gemini-123456"
	cfg, gem, err := UpsertProvider(ctx, st, ProviderPatch{
		Name:         "Google Gemini",
		Protocol:     llmcompat.ProtocolGemini,
		BaseURL:      "https://generativelanguage.googleapis.com/v1beta",
		APIKey:       &gemKey,
		Models:       []string{"gemini-2.5-flash", "gemini-2.5-pro"},
		SetAsDefault: true,
		DefaultModel: "gemini-2.5-flash",
	})
	if err != nil {
		t.Fatalf("upsert gemini: %v", err)
	}
	if cfg.DefaultProviderID != gem.ID || cfg.DefaultModel != "gemini-2.5-flash" {
		t.Fatalf("unexpected default after upsert: %+v", cfg)
	}

	// Default should mirror to legacy services:llm including protocol
	resolved := ResolveDefault(ctx, st)
	if resolved.Protocol != llmcompat.ProtocolGemini || resolved.Model != "gemini-2.5-flash" || resolved.APIKey != gemKey {
		t.Fatalf("unexpected ResolveDefault: %+v", resolved)
	}

	// Add Magpie provider and verify it mirrors to modelgateway
	mpKey := "sk-magpie-abcdefghij"
	mpMgmtKey := "k-magpie-admin-1234"
	_, mp, err := UpsertProvider(ctx, st, ProviderPatch{
		Name:          "公司 Magpie",
		Protocol:      llmcompat.ProtocolOpenAI,
		Vendor:        VendorMagpie,
		BaseURL:       "https://magpie.corp/v1",
		APIKey:        &mpKey,
		ManagementURL: "https://magpie.corp",
		ManagementKey: &mpMgmtKey,
		Models:        []string{"guolian-llm-max"},
	})
	if err != nil {
		t.Fatalf("upsert magpie: %v", err)
	}

	gw, err := modelgateway.GetGatewayConfig(ctx, st)
	if err != nil {
		t.Fatalf("GetGatewayConfig: %v", err)
	}
	if gw.GatewayType != "magpie" || gw.InferenceURL != "https://magpie.corp/v1" || gw.ManagementURL != "https://magpie.corp" || gw.APIKey != mpKey || gw.ManagementKey != mpMgmtKey {
		t.Fatalf("magpie provider did not mirror into modelgateway: %+v", gw)
	}

	// Updating without api_key preserves saved api_key
	_, mp2, err := UpsertProvider(ctx, st, ProviderPatch{
		ID:            mp.ID,
		Name:          "公司 Magpie 改名",
		Protocol:      llmcompat.ProtocolOpenAI,
		Vendor:        VendorMagpie,
		BaseURL:       "https://magpie.corp/v1",
		ManagementURL: "https://magpie.corp",
		Models:        []string{"guolian-llm-max", "qwen-plus"},
	})
	if err != nil {
		t.Fatalf("update magpie: %v", err)
	}
	if mp2.APIKey != mpKey || mp2.ManagementKey != mpMgmtKey {
		t.Fatalf("omitted secrets must be preserved: %+v", mp2)
	}

	// Public view never leaks raw keys
	pub := cfg.ToPublic()
	if pub.Providers[0].MaskedKey == gemKey || !pub.Providers[0].APIKeySet {
		t.Fatalf("public view masking broken: %+v", pub.Providers[0])
	}
}

func TestAgentBackedProviderWithoutAPIKey(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	cfg, saved, err := UpsertProvider(ctx, st, ProviderPatch{
		Name:         "OpenAI Codex（免 Key 直连）",
		Protocol:     llmcompat.ProtocolAgent,
		AgentID:      "codex-local",
		AgentKind:    "codex",
		Models:       []string{"gpt-5.4", "gpt-5.3-codex"},
		SetAsDefault: true,
		DefaultModel: "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("upsert agent provider: %v", err)
	}
	if saved.Protocol != llmcompat.ProtocolAgent || saved.AgentID != "codex-local" || saved.APIKey != "" || saved.BaseURL != "" {
		t.Fatalf("unexpected agent provider saved: %+v", saved)
	}
	pub := cfg.ToPublic()
	if !pub.EffectiveConfigured {
		t.Fatalf("expected EffectiveConfigured=true for agent-backed provider without API key: %+v", pub)
	}

	resolved := ResolveDefault(ctx, st)
	if !resolved.IsConfigured() {
		t.Fatalf("expected ResolveDefault().IsConfigured()=true for agent-backed provider: %+v", resolved)
	}
	if resolved.Protocol != llmcompat.ProtocolAgent || resolved.AgentID != "codex-local" || resolved.Model != "gpt-5.4" {
		t.Fatalf("unexpected resolved default: %+v", resolved)
	}

	// Upserting same AgentID without explicit ID deduplicates cleanly instead of duplicating entries.
	cfg2, saved2, err := UpsertProvider(ctx, st, ProviderPatch{
		Name:         "Codex 更新",
		Protocol:     llmcompat.ProtocolAgent,
		AgentID:      "codex-local",
		AgentKind:    "codex",
		Models:       []string{"gpt-5.4"},
		SetAsDefault: true,
		DefaultModel: "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("upsert duplicate agent provider: %v", err)
	}
	if saved2.ID != saved.ID || len(cfg2.Providers) != 1 {
		t.Fatalf("expected deduplication by AgentID, got id=%s len=%d", saved2.ID, len(cfg2.Providers))
	}
}
