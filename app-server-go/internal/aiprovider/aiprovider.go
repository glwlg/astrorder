// Package aiprovider manages unified AI providers (OpenAI-compatible, Gemini-compatible,
// and extended OpenAI-compatible gateways such as Magpie and OpenCodeX) so office tools
// (digest, document refinement, meetings, auto session titles) and gateway sync share
// one consistent provider directory.
package aiprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"

	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
)

var seedMu sync.Mutex

const (
	PrefAIProviders = "services:ai_providers"
	prefLegacyLLM   = "services:llm"
	prefMeetings    = "meetings:settings"

	VendorStandard  = ""
	VendorMagpie    = "magpie"
	VendorOpenCodeX = "opencodex"
)

// Provider is the persisted record for one AI provider.
type Provider struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Protocol        string            `json:"protocol"`                   // "openai" | "gemini" | "agent"
	Vendor          string            `json:"vendor,omitempty"`           // "" | "magpie" | "opencodex"
	BaseURL         string            `json:"base_url"`                   // Inference Base URL (empty for protocol="agent")
	APIKey          string            `json:"api_key,omitempty"`          // Secret (masked in PublicProvider)
	AgentID         string            `json:"agent_id,omitempty"`         // Target Agent ID when protocol="agent"
	AgentKind       string            `json:"agent_kind,omitempty"`       // Target Agent kind (codex/antigravity/hermes/grok)
	Models          []string          `json:"models,omitempty"`           // Cached fetched/custom model IDs
	Enabled         bool              `json:"enabled"`                    // Selectable across features
	ManagementURL   string            `json:"management_url,omitempty"`   // Magpie / OpenCodeX extra API
	ManagementKey   string            `json:"management_key,omitempty"`   // Magpie management key (secret)
	StatisticsURL   string            `json:"statistics_url,omitempty"`   // Optional gateway stats page URL
	TargetOverrides map[string]string `json:"target_overrides,omitempty"` // Optional Agent sync target overrides
}

// PublicProvider is the secret-masked wire representation returned to browsers.
type PublicProvider struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Protocol            string            `json:"protocol"`
	Vendor              string            `json:"vendor,omitempty"`
	BaseURL             string            `json:"base_url"`
	APIKeySet           bool              `json:"api_key_set"`
	MaskedKey           string            `json:"masked_key,omitempty"`
	AgentID             string            `json:"agent_id,omitempty"`
	AgentKind           string            `json:"agent_kind,omitempty"`
	Models              []string          `json:"models"`
	Enabled             bool              `json:"enabled"`
	ManagementURL       string            `json:"management_url,omitempty"`
	ManagementKeySet    bool              `json:"management_key_set,omitempty"`
	MaskedManagementKey string            `json:"masked_management_key,omitempty"`
	StatisticsURL       string            `json:"statistics_url,omitempty"`
	TargetOverrides     map[string]string `json:"target_overrides,omitempty"`
}

// Config is the complete AI providers configuration stored in workspace_preferences.
type Config struct {
	Providers         []Provider `json:"providers"`
	DefaultProviderID string     `json:"default_provider_id"`
	DefaultModel      string     `json:"default_model"`
	DefaultReasoning  string     `json:"default_reasoning"` // "none" | "low" | "medium" | "high"
}

// PublicConfig is the secret-masked response for GET /api/v1/services/providers.
type PublicConfig struct {
	Providers             []PublicProvider `json:"providers"`
	DefaultProviderID     string           `json:"default_provider_id"`
	DefaultModel          string           `json:"default_model"`
	DefaultReasoning      string           `json:"default_reasoning"`
	EffectiveConfigured   bool             `json:"effective_configured"`
	EffectiveModel        string           `json:"effective_model,omitempty"`
	EffectiveProviderName string           `json:"effective_provider_name,omitempty"`
}

// ProviderPatch is accepted when creating or updating a provider.
type ProviderPatch struct {
	ID                 string            `json:"id,omitempty"`
	Name               string            `json:"name"`
	Protocol           string            `json:"protocol"`
	Vendor             string            `json:"vendor,omitempty"`
	BaseURL            string            `json:"base_url"`
	APIKey             *string           `json:"api_key,omitempty"`
	ClearAPIKey        bool              `json:"clear_api_key,omitempty"`
	AgentID            string            `json:"agent_id,omitempty"`
	AgentKind          string            `json:"agent_kind,omitempty"`
	Models             []string          `json:"models,omitempty"`
	Enabled            *bool             `json:"enabled,omitempty"`
	ManagementURL      string            `json:"management_url,omitempty"`
	ManagementKey      *string           `json:"management_key,omitempty"`
	ClearManagementKey bool              `json:"clear_management_key,omitempty"`
	StatisticsURL      string            `json:"statistics_url,omitempty"`
	TargetOverrides    map[string]string `json:"target_overrides,omitempty"`
	SetAsDefault       bool              `json:"set_as_default,omitempty"`
	DefaultModel       string            `json:"default_model,omitempty"`
}

// ResolvedModel holds everything needed to invoke an LLM completion.
type ResolvedModel struct {
	ProviderID   string
	ProviderName string
	Protocol     string
	Vendor       string
	BaseURL      string
	APIKey       string
	AgentID      string
	AgentKind    string
	Model        string
	Reasoning    string
}

// IsConfigured reports whether this resolved model can execute completions (either via
// an API-key HTTP provider or via a connected Agent runtime).
func (r ResolvedModel) IsConfigured() bool {
	if llmcompat.NormalizeProtocol(r.Protocol) == llmcompat.ProtocolAgent {
		return strings.TrimSpace(r.AgentID) != ""
	}
	return strings.TrimSpace(r.BaseURL) != "" && strings.TrimSpace(r.APIKey) != ""
}

// ToChatRequest converts a ResolvedModel into a protocol-neutral llmcompat.ChatRequest.
func (r ResolvedModel) ToChatRequest(maxTokens int, messages []llmcompat.ChatMessage) llmcompat.ChatRequest {
	return llmcompat.ChatRequest{
		Protocol:  llmcompat.NormalizeProtocol(r.Protocol),
		BaseURL:   strings.TrimRight(strings.TrimSpace(r.BaseURL), "/"),
		APIKey:    strings.TrimSpace(r.APIKey),
		AgentID:   strings.TrimSpace(r.AgentID),
		Model:     strings.TrimSpace(r.Model),
		Reasoning: strings.TrimSpace(r.Reasoning),
		MaxTokens: maxTokens,
		Messages:  messages,
	}
}

func randomID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

// NormalizeVendor validates and normalizes optional gateway vendor extensions.
func NormalizeVendor(protocol, vendor string) string {
	if llmcompat.NormalizeProtocol(protocol) != llmcompat.ProtocolOpenAI {
		return VendorStandard
	}
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case VendorMagpie:
		return VendorMagpie
	case VendorOpenCodeX:
		return VendorOpenCodeX
	default:
		return VendorStandard
	}
}

// NormalizeReasoning coerces reasoning effort into valid values.
func NormalizeReasoning(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none", "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "low"
	}
}

// MaskSecret masks an API key or management key safely for UI display.
func MaskSecret(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if len(s) > 12 {
		return s[:6] + "..." + s[len(s)-4:]
	}
	return "已配置"
}

func validateHTTPURL(raw, label string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", errors.New(label + "必须是完整的 http 或 https 地址")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New(label + "必须是完整的 http 或 https 地址")
	}
	return trimmed, nil
}

func cleanModels(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		m := strings.TrimSpace(item)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

func normalizeURLKey(raw string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(raw), "/"))
}

func inferProviderName(baseURL, protocol, vendor string) string {
	switch vendor {
	case VendorMagpie:
		return "Magpie 网关"
	case VendorOpenCodeX:
		return "OpenCodeX 网关"
	}
	if protocol == llmcompat.ProtocolGemini {
		return "Google Gemini"
	}
	if parsed, err := url.Parse(strings.TrimSpace(baseURL)); err == nil && parsed.Host != "" {
		host := strings.ToLower(parsed.Hostname())
		switch {
		case strings.Contains(host, "deepseek"):
			return "DeepSeek"
		case strings.Contains(host, "openai.com"):
			return "OpenAI"
		case strings.Contains(host, "dashscope"):
			return "通义千问"
		case strings.Contains(host, "openrouter"):
			return "OpenRouter"
		case strings.Contains(host, "googleapis"):
			return "Google Gemini"
		default:
			return parsed.Hostname()
		}
	}
	return "自定义供应商"
}

func inferProtocolFromURL(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err == nil && strings.EqualFold(parsed.Hostname(), "generativelanguage.googleapis.com") {
		if !strings.Contains(strings.ToLower(parsed.Path), "/openai") {
			return llmcompat.ProtocolGemini
		}
	}
	return llmcompat.ProtocolOpenAI
}

func normalizeConfig(cfg Config) Config {
	out := Config{
		Providers:         make([]Provider, 0, len(cfg.Providers)),
		DefaultProviderID: strings.TrimSpace(cfg.DefaultProviderID),
		DefaultModel:      strings.TrimSpace(cfg.DefaultModel),
		DefaultReasoning:  NormalizeReasoning(cfg.DefaultReasoning),
	}
	seenIDs := make(map[string]bool, len(cfg.Providers))
	for _, p := range cfg.Providers {
		id := strings.TrimSpace(p.ID)
		if id == "" || seenIDs[id] {
			id = randomID("prv")
		}
		seenIDs[id] = true
		proto := llmcompat.NormalizeProtocol(p.Protocol)
		vendor := NormalizeVendor(proto, p.Vendor)
		base := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = inferProviderName(base, proto, vendor)
		}
		models := cleanModels(p.Models)
		overrides := map[string]string{}
		for k, v := range p.TargetOverrides {
			if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
				overrides[strings.TrimSpace(k)] = strings.TrimRight(strings.TrimSpace(v), "/")
			}
		}
		out.Providers = append(out.Providers, Provider{
			ID:              id,
			Name:            name,
			Protocol:        proto,
			Vendor:          vendor,
			BaseURL:         base,
			APIKey:          strings.TrimSpace(p.APIKey),
			AgentID:         strings.TrimSpace(p.AgentID),
			AgentKind:       strings.TrimSpace(p.AgentKind),
			Models:          models,
			Enabled:         p.Enabled,
			ManagementURL:   strings.TrimRight(strings.TrimSpace(p.ManagementURL), "/"),
			ManagementKey:   strings.TrimSpace(p.ManagementKey),
			StatisticsURL:   strings.TrimSpace(p.StatisticsURL),
			TargetOverrides: overrides,
		})
	}

	// Ensure DefaultProviderID points to an existing provider when available.
	defaultExists := false
	for _, p := range out.Providers {
		if p.ID == out.DefaultProviderID {
			defaultExists = true
			if out.DefaultModel == "" && len(p.Models) > 0 {
				out.DefaultModel = p.Models[0]
			}
			break
		}
	}
	if !defaultExists {
		out.DefaultProviderID = ""
		for _, p := range out.Providers {
			if p.Enabled && (p.APIKey != "" || (p.Protocol == llmcompat.ProtocolAgent && p.AgentID != "")) {
				out.DefaultProviderID = p.ID
				if out.DefaultModel == "" && len(p.Models) > 0 {
					out.DefaultModel = p.Models[0]
				}
				break
			}
		}
		if out.DefaultProviderID == "" && len(out.Providers) > 0 {
			out.DefaultProviderID = out.Providers[0].ID
			if out.DefaultModel == "" && len(out.Providers[0].Models) > 0 {
				out.DefaultModel = out.Providers[0].Models[0]
			}
		}
	}
	return out
}

// ToPublic converts a full Config to a secret-free PublicConfig.
func (c Config) ToPublic() PublicConfig {
	pub := PublicConfig{
		Providers:         make([]PublicProvider, 0, len(c.Providers)),
		DefaultProviderID: c.DefaultProviderID,
		DefaultModel:      c.DefaultModel,
		DefaultReasoning:  NormalizeReasoning(c.DefaultReasoning),
	}
	for _, p := range c.Providers {
		models := p.Models
		if models == nil {
			models = []string{}
		}
		pub.Providers = append(pub.Providers, PublicProvider{
			ID:                  p.ID,
			Name:                p.Name,
			Protocol:            p.Protocol,
			Vendor:              p.Vendor,
			BaseURL:             p.BaseURL,
			APIKeySet:           p.APIKey != "",
			MaskedKey:           MaskSecret(p.APIKey),
			AgentID:             p.AgentID,
			AgentKind:           p.AgentKind,
			Models:              models,
			Enabled:             p.Enabled,
			ManagementURL:       p.ManagementURL,
			ManagementKeySet:    p.ManagementKey != "",
			MaskedManagementKey: MaskSecret(p.ManagementKey),
			StatisticsURL:       p.StatisticsURL,
			TargetOverrides:     p.TargetOverrides,
		})
		if p.ID == c.DefaultProviderID {
			pub.EffectiveProviderName = p.Name
			pub.EffectiveModel = c.DefaultModel
			if pub.EffectiveModel == "" && len(p.Models) > 0 {
				pub.EffectiveModel = p.Models[0]
			}
			if p.Protocol == llmcompat.ProtocolAgent {
				pub.EffectiveConfigured = p.Enabled && p.AgentID != ""
			} else {
				pub.EffectiveConfigured = p.Enabled && p.BaseURL != "" && p.APIKey != "" && pub.EffectiveModel != ""
			}
		}
	}
	return pub
}

func decodeStoredConfig(st *store.Store, val any) (Config, bool, bool, error) {
	if val == nil {
		return Config{}, false, false, nil
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return Config{}, false, false, nil
	}
	var saved Config
	if err := json.Unmarshal(raw, &saved); err != nil {
		return Config{}, false, false, nil
	}
	decrypted, needsMigration, err := decryptConfigFromStorage(st, saved)
	if err != nil {
		return Config{}, true, false, err
	}
	return decrypted, true, needsMigration, nil
}

func persistEncryptedConfig(ctx context.Context, st *store.Store, cfg Config) error {
	norm := normalizeConfig(cfg)
	encrypted, err := encryptConfigForStorage(st, norm)
	if err != nil {
		return err
	}
	return st.SetPreference(ctx, PrefAIProviders, encrypted, true)
}

func migratePlaintextConfigLocked(ctx context.Context, st *store.Store) (Config, bool, bool, error) {
	seedMu.Lock()
	defer seedMu.Unlock()

	pref, err := st.GetPreference(ctx, PrefAIProviders)
	if err != nil || !pref.Exists || pref.Value == nil {
		return Config{}, false, false, err
	}
	cfg, parsed, stillNeeds, decErr := decodeStoredConfig(st, pref.Value)
	if decErr != nil {
		return Config{}, false, false, decErr
	}
	if !parsed || !stillNeeds {
		return cfg, parsed, false, nil
	}
	if err := persistEncryptedConfig(ctx, st, cfg); err != nil {
		return cfg, true, false, err
	}
	return cfg, true, true, nil
}

// MigratePlaintextSecrets encrypts any legacy plaintext APIKey/ManagementKey entries
// stored in services:ai_providers in-place. It is safe to call repeatedly.
func MigratePlaintextSecrets(ctx context.Context, st *store.Store) (bool, error) {
	if st == nil {
		return false, nil
	}
	_, _, migrated, err := migratePlaintextConfigLocked(ctx, st)
	return migrated, err
}

// LoadConfig loads the unified AI providers config, automatically seeding and
// persisting from legacy services:llm, analytics:llm_gateway (Magpie/OpenCodeX),
// and custom meetings:settings if services:ai_providers has not been saved yet.
// Legacy plaintext keys in services:ai_providers are read transparently and
// lazily migrated to AES-256-GCM ciphertext in-place.
func LoadConfig(ctx context.Context, st *store.Store) (Config, error) {
	if st == nil {
		return Config{Providers: []Provider{}, DefaultReasoning: "low"}, nil
	}
	pref, err := st.GetPreference(ctx, PrefAIProviders)
	if err != nil {
		return Config{Providers: []Provider{}, DefaultReasoning: "low"}, err
	}
	if pref.Exists && pref.Value != nil {
		cfg, parsed, needsMigration, decErr := decodeStoredConfig(st, pref.Value)
		if decErr != nil {
			return Config{Providers: []Provider{}, DefaultReasoning: "low"}, decErr
		}
		if parsed {
			if needsMigration {
				if migratedCfg, valid, _, mErr := migratePlaintextConfigLocked(ctx, st); mErr == nil && valid {
					return migratedCfg, nil
				}
			}
			return cfg, nil
		}
	}

	seedMu.Lock()
	defer seedMu.Unlock()

	// Re-check after acquiring lock in case a concurrent caller already persisted.
	if pref2, err2 := st.GetPreference(ctx, PrefAIProviders); err2 == nil && pref2.Exists && pref2.Value != nil {
		cfg, parsed, needsMigration, decErr := decodeStoredConfig(st, pref2.Value)
		if decErr != nil {
			return Config{Providers: []Provider{}, DefaultReasoning: "low"}, decErr
		}
		if parsed {
			if needsMigration {
				_ = persistEncryptedConfig(ctx, st, cfg)
			}
			return cfg, nil
		}
	}

	seeded := seedFromLegacySources(ctx, st)
	if len(seeded.Providers) > 0 {
		// Persist immediately (encrypted) so subsequent mutations to legacy preferences
		// (such as switching meetings:settings from "custom" to "provider") never drop
		// migrated providers.
		_ = persistEncryptedConfig(ctx, st, seeded)
	}
	return seeded, nil
}

func seedFromLegacySources(ctx context.Context, st *store.Store) Config {
	cfg := Config{
		Providers:        []Provider{},
		DefaultReasoning: "low",
	}

	// 1. Legacy services:llm (helper LLM)
	if pref, err := st.GetPreference(ctx, prefLegacyLLM); err == nil && pref.Exists {
		if saved, ok := pref.Value.(map[string]any); ok {
			base := strings.TrimRight(strings.TrimSpace(strVal(saved, "base_url")), "/")
			key := strings.TrimSpace(strVal(saved, "api_key"))
			model := strings.TrimSpace(strVal(saved, "model"))
			reasoning := NormalizeReasoning(strVal(saved, "reasoning"))
			proto := strings.TrimSpace(strVal(saved, "protocol"))
			if proto == "" {
				proto = inferProtocolFromURL(base)
			} else {
				proto = llmcompat.NormalizeProtocol(proto)
			}
			cfg.DefaultReasoning = reasoning
			if key != "" || (base != "" && base != "https://api.deepseek.com/v1") {
				if base == "" {
					base = "https://api.deepseek.com/v1"
				}
				var models []string
				if model != "" {
					models = []string{model}
				}
				p := Provider{
					ID:       "prv_default",
					Name:     inferProviderName(base, proto, VendorStandard),
					Protocol: proto,
					BaseURL:  base,
					APIKey:   key,
					Models:   models,
					Enabled:  true,
				}
				cfg.Providers = append(cfg.Providers, p)
				cfg.DefaultProviderID = p.ID
				cfg.DefaultModel = model
			}
		}
	}

	// 2. Existing modelgateway profiles (Magpie / OpenCodeX)
	if gw, err := modelgateway.GetGatewayConfig(ctx, st); err == nil {
		vendorOrder := []string{gw.GatewayType, VendorMagpie, VendorOpenCodeX}
		seenVendor := map[string]bool{}
		for _, v := range vendorOrder {
			v = strings.ToLower(strings.TrimSpace(v))
			if (v != VendorMagpie && v != VendorOpenCodeX) || seenVendor[v] {
				continue
			}
			seenVendor[v] = true
			prof, ok := gw.GatewayProfiles[v]
			if !ok {
				if v == gw.GatewayType {
					prof = modelgateway.GatewayProfile{
						GatewayType:     v,
						ManagementURL:   gw.ManagementURL,
						InferenceURL:    gw.InferenceURL,
						APIKey:          gw.APIKey,
						ManagementKey:   gw.ManagementKey,
						StatisticsURL:   gw.StatisticsURL,
						TargetOverrides: gw.TargetOverrides,
					}
				} else {
					continue
				}
			}
			infURL := strings.TrimRight(strings.TrimSpace(prof.InferenceURL), "/")
			mgmtURL := strings.TrimRight(strings.TrimSpace(prof.ManagementURL), "/")
			if infURL == "" && mgmtURL == "" && strings.TrimSpace(prof.APIKey) == "" {
				continue
			}
			if infURL == "" {
				infURL = mgmtURL
			}
			// Check if matches an existing provider by BaseURL
			matchedIdx := -1
			for i := range cfg.Providers {
				if normalizeURLKey(cfg.Providers[i].BaseURL) == normalizeURLKey(infURL) {
					matchedIdx = i
					break
				}
			}
			if matchedIdx >= 0 {
				cfg.Providers[matchedIdx].Vendor = v
				if mgmtURL != "" {
					cfg.Providers[matchedIdx].ManagementURL = mgmtURL
				}
				if strings.TrimSpace(prof.ManagementKey) != "" {
					cfg.Providers[matchedIdx].ManagementKey = strings.TrimSpace(prof.ManagementKey)
				}
				if cfg.Providers[matchedIdx].APIKey == "" && strings.TrimSpace(prof.APIKey) != "" {
					cfg.Providers[matchedIdx].APIKey = strings.TrimSpace(prof.APIKey)
				}
				if prof.StatisticsURL != "" {
					cfg.Providers[matchedIdx].StatisticsURL = prof.StatisticsURL
				}
				if len(prof.TargetOverrides) > 0 {
					cfg.Providers[matchedIdx].TargetOverrides = prof.TargetOverrides
				}
			} else {
				cfg.Providers = append(cfg.Providers, Provider{
					ID:              "prv_" + v,
					Name:            inferProviderName(infURL, llmcompat.ProtocolOpenAI, v),
					Protocol:        llmcompat.ProtocolOpenAI,
					Vendor:          v,
					BaseURL:         infURL,
					APIKey:          strings.TrimSpace(prof.APIKey),
					Models:          []string{},
					Enabled:         true,
					ManagementURL:   mgmtURL,
					ManagementKey:   strings.TrimSpace(prof.ManagementKey),
					StatisticsURL:   strings.TrimSpace(prof.StatisticsURL),
					TargetOverrides: prof.TargetOverrides,
				})
			}
		}
	}

	// 3. Legacy custom meetings:settings provider (so existing meeting model config isn't lost)
	if pref, err := st.GetPreference(ctx, prefMeetings); err == nil && pref.Exists {
		if saved, ok := pref.Value.(map[string]any); ok {
			if strings.EqualFold(strings.TrimSpace(strVal(saved, "model_source")), "custom") {
				base := strings.TrimRight(strings.TrimSpace(strVal(saved, "base_url")), "/")
				key := strings.TrimSpace(strVal(saved, "api_key"))
				model := strings.TrimSpace(strVal(saved, "model"))
				if base != "" && key != "" {
					matchedIdx := -1
					for i := range cfg.Providers {
						if normalizeURLKey(cfg.Providers[i].BaseURL) == normalizeURLKey(base) {
							matchedIdx = i
							break
						}
					}
					if matchedIdx >= 0 {
						if model != "" {
							cfg.Providers[matchedIdx].Models = cleanModels(append(cfg.Providers[matchedIdx].Models, model))
						}
					} else {
						proto := inferProtocolFromURL(base)
						var models []string
						if model != "" {
							models = []string{model}
						}
						cfg.Providers = append(cfg.Providers, Provider{
							ID:       "prv_meeting_legacy",
							Name:     inferProviderName(base, proto, VendorStandard),
							Protocol: proto,
							BaseURL:  base,
							APIKey:   key,
							Models:   models,
							Enabled:  true,
						})
					}
				}
			}
		}
	}

	return normalizeConfig(cfg)
}

func strVal(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// SaveConfig persists services:ai_providers (with APIKey and ManagementKey encrypted
// at rest) and keeps legacy services:llm and modelgateway (for Magpie / OpenCodeX
// vendors) synchronized.
func SaveConfig(ctx context.Context, st *store.Store, cfg Config) (Config, error) {
	if st == nil {
		return Config{}, errors.New("store unavailable")
	}
	norm := normalizeConfig(cfg)
	if err := persistEncryptedConfig(ctx, st, norm); err != nil {
		return Config{}, err
	}
	syncDefaultToLegacyLLM(ctx, st, norm)
	syncVendorsToModelGateway(ctx, st, norm)
	return norm, nil
}

func syncDefaultToLegacyLLM(ctx context.Context, st *store.Store, cfg Config) {
	var def *Provider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == cfg.DefaultProviderID {
			def = &cfg.Providers[i]
			break
		}
	}
	if def == nil {
		return
	}
	model := cfg.DefaultModel
	if model == "" && len(def.Models) > 0 {
		model = def.Models[0]
	}
	payload := map[string]any{
		"base_url":  def.BaseURL,
		"api_key":   def.APIKey,
		"model":     model,
		"reasoning": NormalizeReasoning(cfg.DefaultReasoning),
		"protocol":  def.Protocol,
	}
	_ = st.SetPreference(ctx, prefLegacyLLM, payload, true)
}

func syncVendorsToModelGateway(ctx context.Context, st *store.Store, cfg Config) {
	// Collect any Magpie / OpenCodeX vendor providers and sync their endpoints & keys
	// into analytics:llm_gateway so analytics, quotas, Jev, and Agent sync work seamlessly.
	vendorProviders := map[string]Provider{}
	for _, p := range cfg.Providers {
		if p.Vendor == VendorMagpie || p.Vendor == VendorOpenCodeX {
			if _, exists := vendorProviders[p.Vendor]; !exists {
				vendorProviders[p.Vendor] = p
			}
		}
	}
	if len(vendorProviders) == 0 {
		return
	}
	currentGW, err := modelgateway.GetGatewayConfig(ctx, st)
	if err != nil {
		return
	}

	// Keep the currently active gateway_type if present among vendorProviders,
	// otherwise activate whichever vendor provider is configured.
	activeVendor := currentGW.GatewayType
	if _, ok := vendorProviders[activeVendor]; !ok {
		for v := range vendorProviders {
			activeVendor = v
			break
		}
	}

	profilesPayload := map[string]any{}
	for v, p := range vendorProviders {
		existing := currentGW.GatewayProfiles[v]
		mgmtURL := p.ManagementURL
		if mgmtURL == "" {
			mgmtURL = existing.ManagementURL
		}
		if mgmtURL == "" {
			mgmtURL = p.BaseURL
		}
		entry := map[string]any{
			"gateway_type":   v,
			"inference_url":  p.BaseURL,
			"management_url": mgmtURL,
		}
		if p.APIKey != "" {
			entry["api_key"] = p.APIKey
		} else {
			entry["clear_key"] = true
		}
		if v == VendorMagpie {
			if p.ManagementKey != "" {
				entry["management_key"] = p.ManagementKey
			} else if existing.ManagementKey == "" {
				entry["clear_management_key"] = true
			}
		}
		if p.StatisticsURL != "" {
			entry["statistics_url"] = p.StatisticsURL
		}
		if len(p.TargetOverrides) > 0 {
			entry["target_overrides"] = p.TargetOverrides
		}
		profilesPayload[v] = entry
	}

	activeEntry, ok := profilesPayload[activeVendor].(map[string]any)
	if !ok {
		return
	}
	payload := map[string]any{
		"gateway_type":     activeVendor,
		"gateway_profiles": profilesPayload,
	}
	for k, val := range activeEntry {
		payload[k] = val
	}
	_, _ = modelgateway.SaveGatewayConfig(ctx, st, payload)
}

// UpsertProvider creates a new provider or updates an existing one by ID.
func UpsertProvider(ctx context.Context, st *store.Store, patch ProviderPatch) (Config, Provider, error) {
	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		return Config{}, Provider{}, err
	}

	proto := llmcompat.NormalizeProtocol(patch.Protocol)
	vendor := NormalizeVendor(proto, patch.Vendor)
	agentID := strings.TrimSpace(patch.AgentID)
	agentKind := strings.TrimSpace(patch.AgentKind)
	baseURL := ""
	if proto == llmcompat.ProtocolAgent {
		if agentID == "" {
			return Config{}, Provider{}, errors.New("请先选择要对接的 Agent")
		}
		vendor = VendorStandard
	} else {
		var uErr error
		baseURL, uErr = validateHTTPURL(patch.BaseURL, "接口地址 (Base URL)")
		if uErr != nil {
			return Config{}, Provider{}, uErr
		}
	}

	mgmtURL := strings.TrimSpace(patch.ManagementURL)
	if vendor != VendorStandard && mgmtURL != "" {
		cleanMgmt, mErr := validateHTTPURL(mgmtURL, "管理地址")
		if mErr != nil {
			return Config{}, Provider{}, mErr
		}
		mgmtURL = cleanMgmt
	} else if vendor == VendorStandard {
		mgmtURL = ""
	}

	name := strings.TrimSpace(patch.Name)
	if name == "" {
		if proto == llmcompat.ProtocolAgent {
			if agentKind != "" {
				name = "Agent 直连 · " + agentKind
			} else {
				name = "Agent 直连 · " + agentID
			}
		} else {
			name = inferProviderName(baseURL, proto, vendor)
		}
	}

	idx := -1
	if id := strings.TrimSpace(patch.ID); id != "" {
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == id {
				idx = i
				break
			}
		}
	}
	// Deduplicate agent-backed providers by AgentID when creating without an explicit ID.
	if idx < 0 && proto == llmcompat.ProtocolAgent && agentID != "" {
		for i := range cfg.Providers {
			if cfg.Providers[i].Protocol == llmcompat.ProtocolAgent && cfg.Providers[i].AgentID == agentID {
				idx = i
				break
			}
		}
	}

	var next Provider
	if idx >= 0 {
		existing := cfg.Providers[idx]
		next = existing
		next.Name = name
		next.Protocol = proto
		next.Vendor = vendor
		next.BaseURL = baseURL
		next.AgentID = agentID
		next.AgentKind = agentKind
		next.ManagementURL = mgmtURL
		if patch.StatisticsURL != "" {
			next.StatisticsURL = strings.TrimSpace(patch.StatisticsURL)
		}
		if patch.TargetOverrides != nil {
			next.TargetOverrides = patch.TargetOverrides
		}
		if patch.Models != nil {
			next.Models = cleanModels(patch.Models)
		}
		if patch.Enabled != nil {
			next.Enabled = *patch.Enabled
		}
		if patch.ClearAPIKey || proto == llmcompat.ProtocolAgent {
			next.APIKey = ""
		} else if patch.APIKey != nil && strings.TrimSpace(*patch.APIKey) != "" {
			next.APIKey = strings.TrimSpace(*patch.APIKey)
		}
		if vendor == VendorMagpie {
			if patch.ClearManagementKey {
				next.ManagementKey = ""
			} else if patch.ManagementKey != nil && strings.TrimSpace(*patch.ManagementKey) != "" {
				next.ManagementKey = strings.TrimSpace(*patch.ManagementKey)
			}
		} else {
			next.ManagementKey = ""
		}
		cfg.Providers[idx] = next
	} else {
		enabled := true
		if patch.Enabled != nil {
			enabled = *patch.Enabled
		}
		apiKey := ""
		if proto != llmcompat.ProtocolAgent && patch.APIKey != nil {
			apiKey = strings.TrimSpace(*patch.APIKey)
		}
		mgmtKey := ""
		if vendor == VendorMagpie && patch.ManagementKey != nil {
			mgmtKey = strings.TrimSpace(*patch.ManagementKey)
		}
		next = Provider{
			ID:              randomID("prv"),
			Name:            name,
			Protocol:        proto,
			Vendor:          vendor,
			BaseURL:         baseURL,
			APIKey:          apiKey,
			AgentID:         agentID,
			AgentKind:       agentKind,
			Models:          cleanModels(patch.Models),
			Enabled:         enabled,
			ManagementURL:   mgmtURL,
			ManagementKey:   mgmtKey,
			StatisticsURL:   strings.TrimSpace(patch.StatisticsURL),
			TargetOverrides: patch.TargetOverrides,
		}
		cfg.Providers = append(cfg.Providers, next)
	}

	if patch.SetAsDefault || cfg.DefaultProviderID == "" {
		cfg.DefaultProviderID = next.ID
		if strings.TrimSpace(patch.DefaultModel) != "" {
			cfg.DefaultModel = strings.TrimSpace(patch.DefaultModel)
		} else if cfg.DefaultModel == "" && len(next.Models) > 0 {
			cfg.DefaultModel = next.Models[0]
		}
	} else if cfg.DefaultProviderID == next.ID && strings.TrimSpace(patch.DefaultModel) != "" {
		cfg.DefaultModel = strings.TrimSpace(patch.DefaultModel)
	}

	saved, err := SaveConfig(ctx, st, cfg)
	if err != nil {
		return Config{}, Provider{}, err
	}
	for _, p := range saved.Providers {
		if p.ID == next.ID {
			return saved, p, nil
		}
	}
	return saved, next, nil
}

// DeleteProvider removes a provider by ID.
func DeleteProvider(ctx context.Context, st *store.Store, id string) (Config, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Config{}, errors.New("缺少供应商 ID")
	}
	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		return Config{}, err
	}
	next := make([]Provider, 0, len(cfg.Providers))
	found := false
	for _, p := range cfg.Providers {
		if p.ID == id {
			found = true
			continue
		}
		next = append(next, p)
	}
	if !found {
		return Config{}, errors.New("未找到对应 AI 供应商")
	}
	cfg.Providers = next
	if cfg.DefaultProviderID == id {
		cfg.DefaultProviderID = ""
		cfg.DefaultModel = ""
	}
	return SaveConfig(ctx, st, cfg)
}

// SetDefault updates the global default AI provider, model, and reasoning effort.
func SetDefault(ctx context.Context, st *store.Store, providerID, model, reasoning string) (Config, error) {
	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		return Config{}, err
	}
	pid := strings.TrimSpace(providerID)
	m := strings.TrimSpace(model)
	if pid != "" {
		found := false
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == pid {
				found = true
				if m != "" {
					cfg.Providers[i].Models = cleanModels(append(cfg.Providers[i].Models, m))
				}
				break
			}
		}
		if !found {
			return Config{}, errors.New("指定的默认 AI 供应商不存在")
		}
		cfg.DefaultProviderID = pid
	}
	if m != "" {
		cfg.DefaultModel = m
	}
	if strings.TrimSpace(reasoning) != "" {
		cfg.DefaultReasoning = NormalizeReasoning(reasoning)
	}
	return SaveConfig(ctx, st, cfg)
}

// UpdateProviderModels persists freshly fetched models onto a saved provider.
func UpdateProviderModels(ctx context.Context, st *store.Store, providerID string, models []string) (Config, error) {
	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		return Config{}, err
	}
	cleaned := cleanModels(models)
	sort.Strings(cleaned)
	updated := false
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == providerID {
			cfg.Providers[i].Models = cleaned
			updated = true
			if cfg.DefaultProviderID == providerID && cfg.DefaultModel == "" && len(cleaned) > 0 {
				cfg.DefaultModel = cleaned[0]
			}
			break
		}
	}
	if !updated {
		return cfg, nil
	}
	return SaveConfig(ctx, st, cfg)
}

// ResolveDefault returns the effective default provider + model, falling back cleanly
// to legacy services:llm defaults when no provider is configured yet.
func ResolveDefault(ctx context.Context, st *store.Store) ResolvedModel {
	fallback := ResolvedModel{
		Protocol:  llmcompat.ProtocolOpenAI,
		BaseURL:   "https://api.deepseek.com/v1",
		Model:     "deepseek-chat",
		Reasoning: "low",
	}
	if st == nil {
		return fallback
	}
	cfg, err := LoadConfig(ctx, st)
	if err == nil && cfg.DefaultProviderID != "" {
		if res, ok := resolveFromConfig(cfg, cfg.DefaultProviderID, cfg.DefaultModel); ok {
			return res
		}
	}
	// Final fallback to legacy services:llm
	if pref, pErr := st.GetPreference(ctx, prefLegacyLLM); pErr == nil && pref.Exists {
		if m, ok := pref.Value.(map[string]any); ok {
			if s := strings.TrimSpace(strVal(m, "base_url")); s != "" {
				fallback.BaseURL = strings.TrimRight(s, "/")
			}
			fallback.APIKey = strings.TrimSpace(strVal(m, "api_key"))
			if s := strings.TrimSpace(strVal(m, "model")); s != "" {
				fallback.Model = s
			}
			if s := strings.TrimSpace(strVal(m, "reasoning")); s != "" {
				fallback.Reasoning = NormalizeReasoning(s)
			}
			if s := strings.TrimSpace(strVal(m, "protocol")); s != "" {
				fallback.Protocol = llmcompat.NormalizeProtocol(s)
			} else {
				fallback.Protocol = inferProtocolFromURL(fallback.BaseURL)
			}
		}
	}
	return fallback
}

// Resolve looks up a specific provider by ID and applies an optional model override.
func Resolve(ctx context.Context, st *store.Store, providerID, modelOverride string) (ResolvedModel, bool) {
	if st == nil || strings.TrimSpace(providerID) == "" {
		return ResolvedModel{}, false
	}
	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		return ResolvedModel{}, false
	}
	return resolveFromConfig(cfg, strings.TrimSpace(providerID), strings.TrimSpace(modelOverride))
}

func resolveFromConfig(cfg Config, providerID, modelOverride string) (ResolvedModel, bool) {
	for _, p := range cfg.Providers {
		if p.ID != providerID {
			continue
		}
		model := strings.TrimSpace(modelOverride)
		if model == "" && p.ID == cfg.DefaultProviderID {
			model = strings.TrimSpace(cfg.DefaultModel)
		}
		if model == "" && len(p.Models) > 0 {
			model = p.Models[0]
		}
		return ResolvedModel{
			ProviderID:   p.ID,
			ProviderName: p.Name,
			Protocol:     p.Protocol,
			Vendor:       p.Vendor,
			BaseURL:      p.BaseURL,
			APIKey:       p.APIKey,
			AgentID:      p.AgentID,
			AgentKind:    p.AgentKind,
			Model:        model,
			Reasoning:    NormalizeReasoning(cfg.DefaultReasoning),
		}, true
	}
	return ResolvedModel{}, false
}

// FindProvider returns a single saved provider by ID (including raw secrets for server-side calls).
func FindProvider(ctx context.Context, st *store.Store, providerID string) (Provider, bool) {
	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		return Provider{}, false
	}
	for _, p := range cfg.Providers {
		if p.ID == strings.TrimSpace(providerID) {
			return p, true
		}
	}
	return Provider{}, false
}

// SyncFromModelGateway mirrors changes made in Advanced UsageGatewaySettingsCard
// (PUT /api/v1/analytics/config) into services:ai_providers so Magpie/OpenCodeX
// providers never drift out of sync.
func SyncFromModelGateway(ctx context.Context, st *store.Store, gw modelgateway.GatewayConfig) error {
	if st == nil {
		return nil
	}
	// Only write if services:ai_providers already exists; otherwise initial LoadConfig seeds automatically.
	pref, err := st.GetPreference(ctx, PrefAIProviders)
	if err != nil || !pref.Exists {
		return nil
	}
	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		return err
	}
	vendor := strings.ToLower(strings.TrimSpace(gw.GatewayType))
	if vendor != VendorMagpie && vendor != VendorOpenCodeX {
		return nil
	}
	infURL := strings.TrimRight(strings.TrimSpace(gw.InferenceURL), "/")
	if infURL == "" {
		infURL = strings.TrimRight(strings.TrimSpace(gw.ManagementURL), "/")
	}
	if infURL == "" {
		return nil
	}
	updated := false
	for i := range cfg.Providers {
		if cfg.Providers[i].Vendor == vendor {
			cfg.Providers[i].BaseURL = infURL
			cfg.Providers[i].APIKey = strings.TrimSpace(gw.APIKey)
			cfg.Providers[i].ManagementURL = strings.TrimRight(strings.TrimSpace(gw.ManagementURL), "/")
			cfg.Providers[i].ManagementKey = strings.TrimSpace(gw.ManagementKey)
			cfg.Providers[i].StatisticsURL = strings.TrimSpace(gw.StatisticsURL)
			cfg.Providers[i].TargetOverrides = gw.TargetOverrides
			updated = true
			break
		}
	}
	if !updated {
		cfg.Providers = append(cfg.Providers, Provider{
			ID:              "prv_" + vendor,
			Name:            inferProviderName(infURL, llmcompat.ProtocolOpenAI, vendor),
			Protocol:        llmcompat.ProtocolOpenAI,
			Vendor:          vendor,
			BaseURL:         infURL,
			APIKey:          strings.TrimSpace(gw.APIKey),
			Models:          []string{},
			Enabled:         true,
			ManagementURL:   strings.TrimRight(strings.TrimSpace(gw.ManagementURL), "/"),
			ManagementKey:   strings.TrimSpace(gw.ManagementKey),
			StatisticsURL:   strings.TrimSpace(gw.StatisticsURL),
			TargetOverrides: gw.TargetOverrides,
		})
	}
	norm := normalizeConfig(cfg)
	return persistEncryptedConfig(ctx, st, norm)
}

// SyncFromLegacyLLM keeps services:ai_providers in sync if legacy /api/v1/services/llm/config is called.
func SyncFromLegacyLLM(ctx context.Context, st *store.Store, llm map[string]string) error {
	if st == nil {
		return nil
	}
	pref, err := st.GetPreference(ctx, PrefAIProviders)
	if err != nil || !pref.Exists {
		return nil
	}
	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		return err
	}
	base := strings.TrimRight(strings.TrimSpace(llm["base_url"]), "/")
	key := strings.TrimSpace(llm["api_key"])
	model := strings.TrimSpace(llm["model"])
	proto := llmcompat.NormalizeProtocol(llm["protocol"])
	if llm["protocol"] == "" {
		proto = inferProtocolFromURL(base)
	}
	cfg.DefaultReasoning = NormalizeReasoning(llm["reasoning"])
	cfg.DefaultModel = model

	updated := false
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == cfg.DefaultProviderID {
			cfg.Providers[i].BaseURL = base
			cfg.Providers[i].APIKey = key
			cfg.Providers[i].Protocol = proto
			if model != "" {
				cfg.Providers[i].Models = cleanModels(append(cfg.Providers[i].Models, model))
			}
			updated = true
			break
		}
	}
	if !updated && base != "" {
		p := Provider{
			ID:       randomID("prv"),
			Name:     inferProviderName(base, proto, VendorStandard),
			Protocol: proto,
			BaseURL:  base,
			APIKey:   key,
			Models:   cleanModels([]string{model}),
			Enabled:  true,
		}
		cfg.Providers = append(cfg.Providers, p)
		cfg.DefaultProviderID = p.ID
	}
	norm := normalizeConfig(cfg)
	return persistEncryptedConfig(ctx, st, norm)
}
