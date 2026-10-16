package modelgateway

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"astrorder.dev/app-server/internal/store"
)

const (
	PreferenceKey  = "analytics:llm_gateway"
	legacyUsageKey = "analytics:ocx_usage_url"
)

// GatewayProfile represents settings for a specific gateway type.
type GatewayProfile struct {
	StatisticsURL   string            `json:"statistics_url"`
	GatewayType     string            `json:"gateway_type"`
	ManagementURL   string            `json:"management_url"`
	InferenceURL    string            `json:"inference_url"`
	APIKey          string            `json:"api_key"`
	ManagementKey   string            `json:"management_key"`
	TargetOverrides map[string]string `json:"target_overrides"`
}

// GatewayConfig represents the full internal LLM gateway configuration.
type GatewayConfig struct {
	StatisticsURL   string                    `json:"statistics_url"`
	GatewayType     string                    `json:"gateway_type"`
	ManagementURL   string                    `json:"management_url"`
	InferenceURL    string                    `json:"inference_url"`
	APIKey          string                    `json:"api_key"`
	ManagementKey   string                    `json:"management_key"`
	TargetOverrides map[string]string         `json:"target_overrides"`
	GatewayProfiles map[string]GatewayProfile `json:"gateway_profiles"`
}

// PublicProfile is the masked view of a gateway profile.
type PublicProfile struct {
	StatisticsURL       string            `json:"statistics_url"`
	GatewayType         string            `json:"gateway_type"`
	ManagementURL       string            `json:"management_url"`
	InferenceURL        string            `json:"inference_url"`
	TargetOverrides     map[string]string `json:"target_overrides"`
	MaskedKey           string            `json:"masked_key"`
	MaskedManagementKey string            `json:"masked_management_key"`
}

// PublicGatewayConfig is the public response without raw secrets.
type PublicGatewayConfig struct {
	StatisticsURL       string                   `json:"statistics_url"`
	GatewayType         string                   `json:"gateway_type"`
	ManagementURL       string                   `json:"management_url"`
	InferenceURL        string                   `json:"inference_url"`
	TargetOverrides     map[string]string        `json:"target_overrides"`
	MaskedKey           string                   `json:"masked_key"`
	MaskedManagementKey string                   `json:"masked_management_key"`
	GatewayProfiles     map[string]PublicProfile `json:"gateway_profiles"`
}

func defaultGatewayProfiles() map[string]GatewayProfile {
	return map[string]GatewayProfile{
		"opencodex": {
			GatewayType:     "opencodex",
			ManagementURL:   "https://ocx.651971564.xyz",
			InferenceURL:    "https://llm.651971564.xyz/v1",
			APIKey:          "",
			ManagementKey:   "",
			TargetOverrides: map[string]string{},
		},
		"magpie": {
			GatewayType:     "magpie",
			ManagementURL:   "http://192.168.1.11:3430",
			InferenceURL:    "http://192.168.1.11:3425/v1",
			APIKey:          "",
			ManagementKey:   "",
			TargetOverrides: map[string]string{},
		},
	}
}

func defaultGatewayConfig() GatewayConfig {
	profiles := defaultGatewayProfiles()
	ocx := profiles["opencodex"]
	return GatewayConfig{
		GatewayType:     "opencodex",
		ManagementURL:   ocx.ManagementURL,
		InferenceURL:    ocx.InferenceURL,
		APIKey:          ocx.APIKey,
		ManagementKey:   ocx.ManagementKey,
		TargetOverrides: ocx.TargetOverrides,
		GatewayProfiles: profiles,
	}
}

func validateURL(raw, label string) (string, error) {
	val := strings.TrimRight(strings.TrimSpace(raw), "/")
	if val == "" {
		return "", errors.New(label + "必须是完整的 http 或 https 地址")
	}
	u, err := url.Parse(val)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New(label + "必须是完整的 http 或 https 地址")
	}
	return val, nil
}

func validateStatisticsURL(raw string) (string, error) {
	val := strings.TrimSpace(raw)
	if val != "" {
		u, err := url.Parse(val)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return "", errors.New("统计页地址必须是完整的 http 或 https 地址，且不能包含用户名或密码")
		}
	}
	return val, nil
}

func maskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 12 {
		return s[:6] + "..." + s[len(s)-4:]
	}
	return "已配置"
}

// ToPublic converts internal GatewayConfig to PublicGatewayConfig.
func (c GatewayConfig) ToPublic() PublicGatewayConfig {
	pubProfiles := make(map[string]PublicProfile, len(c.GatewayProfiles))
	for k, p := range c.GatewayProfiles {
		pubProfiles[k] = PublicProfile{
			StatisticsURL:       p.StatisticsURL,
			GatewayType:         p.GatewayType,
			ManagementURL:       p.ManagementURL,
			InferenceURL:        p.InferenceURL,
			TargetOverrides:     p.TargetOverrides,
			MaskedKey:           maskSecret(p.APIKey),
			MaskedManagementKey: maskSecret(p.ManagementKey),
		}
	}
	return PublicGatewayConfig{
		StatisticsURL:       c.StatisticsURL,
		GatewayType:         c.GatewayType,
		ManagementURL:       c.ManagementURL,
		InferenceURL:        c.InferenceURL,
		TargetOverrides:     c.TargetOverrides,
		MaskedKey:           maskSecret(c.APIKey),
		MaskedManagementKey: maskSecret(c.ManagementKey),
		GatewayProfiles:     pubProfiles,
	}
}

// GetGatewayConfig reads and validates the current gateway config from preferences.
func GetGatewayConfig(ctx context.Context, st *store.Store) (GatewayConfig, error) {
	pref, err := st.GetPreference(ctx, PreferenceKey)
	if err != nil {
		return GatewayConfig{}, err
	}
	def := defaultGatewayConfig()
	if !pref.Exists || pref.Value == nil {
		// check legacy
		legacyPref, lErr := st.GetPreference(ctx, legacyUsageKey)
		if lErr == nil && legacyPref.Exists && legacyPref.Value != nil {
			if s, ok := legacyPref.Value.(string); ok && s != "" {
				u, parseErr := url.Parse(strings.TrimSpace(s))
				if parseErr == nil && u.Host != "" {
					u.Path = strings.TrimRight(strings.TrimSuffix(u.Path, "/api/usage"), "/")
					u.RawQuery = ""
					def.ManagementURL = strings.TrimRight(u.String(), "/")
					p := def.GatewayProfiles["opencodex"]
					p.ManagementURL = def.ManagementURL
					def.GatewayProfiles["opencodex"] = p
				}
			}
		}
		return def, nil
	}

	m, ok := pref.Value.(map[string]any)
	if !ok {
		return def, nil
	}
	return parseAndMergeGatewayConfig(m, def)
}

func parseAndMergeGatewayConfig(m map[string]any, current GatewayConfig) (GatewayConfig, error) {
	gwType, _ := m["gateway_type"].(string)
	gwType = strings.ToLower(strings.TrimSpace(gwType))
	if gwType == "" {
		gwType = current.GatewayType
	}
	if gwType != "opencodex" && gwType != "magpie" {
		return current, errors.New("暂不支持此 LLM 网关类型")
	}

	profiles := make(map[string]GatewayProfile, len(current.GatewayProfiles))
	for key, profile := range current.GatewayProfiles {
		profiles[key] = profile
	}
	if current.GatewayProfiles == nil {
		profiles = defaultGatewayProfiles()
	}

	// Migrate the old top-level address only to the gateway that owned it.
	previousProfile := profiles[current.GatewayType]
	if previousProfile.StatisticsURL == "" && current.StatisticsURL != "" {
		previousProfile.StatisticsURL = current.StatisticsURL
		profiles[current.GatewayType] = previousProfile
	}

	// Read existing profiles from map if present
	if rawProfiles, ok := m["gateway_profiles"].(map[string]any); ok {
		for pType, rawP := range rawProfiles {
			pType = strings.ToLower(strings.TrimSpace(pType))
			if pType != "opencodex" && pType != "magpie" {
				continue
			}
			pMap, ok := rawP.(map[string]any)
			if !ok {
				continue
			}
			prof := profiles[pType]
			if raw, ok := pMap["statistics_url"].(string); ok {
				clean, err := validateStatisticsURL(raw)
				if err != nil {
					return current, err
				}
				prof.StatisticsURL = clean
			}
			if mURL, ok := pMap["management_url"].(string); ok && mURL != "" {
				if clean, err := validateURL(mURL, "管理地址"); err == nil {
					prof.ManagementURL = clean
				}
			}
			if iURL, ok := pMap["inference_url"].(string); ok && iURL != "" {
				if clean, err := validateURL(iURL, "推理 Base URL"); err == nil {
					prof.InferenceURL = clean
				}
			}
			if clearKey, _ := pMap["clear_key"].(bool); clearKey {
				prof.APIKey = ""
			} else if k, ok := pMap["api_key"].(string); ok && strings.TrimSpace(k) != "" {
				prof.APIKey = strings.TrimSpace(k)
			}
			if clearMgmt, _ := pMap["clear_management_key"].(bool); clearMgmt {
				prof.ManagementKey = ""
			} else if k, ok := pMap["management_key"].(string); ok && strings.TrimSpace(k) != "" {
				prof.ManagementKey = strings.TrimSpace(k)
			}
			if ov, ok := pMap["target_overrides"].(map[string]any); ok {
				cleanedOv := make(map[string]string)
				for tk, tv := range ov {
					if ts, ok := tv.(string); ok {
						if clean, err := validateURL(ts, "目标推理地址"); err == nil {
							cleanedOv[tk] = clean
						}
					}
				}
				prof.TargetOverrides = cleanedOv
			}
			profiles[pType] = prof
		}
	}

	// Active profile override from top-level
	activeProfile := profiles[gwType]

	var managementURL string
	if mURL, ok := m["management_url"].(string); ok && mURL != "" {
		var err error
		managementURL, err = validateURL(mURL, "管理地址")
		if err != nil {
			return current, err
		}
	} else if bURL, ok := m["base_url"].(string); ok && bURL != "" {
		var err error
		managementURL, err = validateURL(bURL, "管理地址")
		if err != nil {
			return current, err
		}
	} else {
		managementURL = activeProfile.ManagementURL
	}

	var inferenceURL string
	if iURL, ok := m["inference_url"].(string); ok && iURL != "" {
		var err error
		inferenceURL, err = validateURL(iURL, "推理 Base URL")
		if err != nil {
			return current, err
		}
	} else {
		inferenceURL = activeProfile.InferenceURL
	}

	activeAPIKey := activeProfile.APIKey
	if clearKey, _ := m["clear_key"].(bool); clearKey {
		activeAPIKey = ""
	} else if k, ok := m["api_key"].(string); ok && strings.TrimSpace(k) != "" {
		activeAPIKey = strings.TrimSpace(k)
	}

	activeManagementKey := activeProfile.ManagementKey
	if clearMgmt, _ := m["clear_management_key"].(bool); clearMgmt {
		activeManagementKey = ""
	} else if k, ok := m["management_key"].(string); ok && strings.TrimSpace(k) != "" {
		activeManagementKey = strings.TrimSpace(k)
	}

	targetOverrides := activeProfile.TargetOverrides
	if ov, ok := m["target_overrides"].(map[string]any); ok {
		targetOverrides = make(map[string]string)
		for tk, tv := range ov {
			ts, ok := tv.(string)
			if !ok || tk == "" {
				return current, errors.New("目标地址覆盖格式无效")
			}
			clean, err := validateURL(ts, "目标推理地址")
			if err != nil {
				return current, err
			}
			targetOverrides[tk] = clean
		}
	}

	statisticsURL := activeProfile.StatisticsURL
	if raw, ok := m["statistics_url"].(string); ok {
		var err error
		statisticsURL, err = validateStatisticsURL(raw)
		if err != nil {
			return current, err
		}
	}
	activeProfile.StatisticsURL = statisticsURL
	activeProfile.GatewayType = gwType
	activeProfile.ManagementURL = managementURL
	activeProfile.InferenceURL = inferenceURL
	activeProfile.APIKey = activeAPIKey
	activeProfile.ManagementKey = activeManagementKey
	activeProfile.TargetOverrides = targetOverrides
	profiles[gwType] = activeProfile
	res := GatewayConfig{
		StatisticsURL:   statisticsURL,
		GatewayType:     gwType,
		ManagementURL:   managementURL,
		InferenceURL:    inferenceURL,
		APIKey:          activeAPIKey,
		ManagementKey:   activeManagementKey,
		TargetOverrides: targetOverrides,
		GatewayProfiles: profiles,
	}
	return res, nil
}

// SaveGatewayConfig updates and stores gateway configuration.
func SaveGatewayConfig(ctx context.Context, st *store.Store, payload map[string]any) (GatewayConfig, error) {
	current, err := GetGatewayConfig(ctx, st)
	if err != nil {
		current = defaultGatewayConfig()
	}
	updated, err := parseAndMergeGatewayConfig(payload, current)
	if err != nil {
		return current, err
	}
	if err := st.SetPreference(ctx, PreferenceKey, updated, true); err != nil {
		return current, err
	}
	return updated, nil
}
