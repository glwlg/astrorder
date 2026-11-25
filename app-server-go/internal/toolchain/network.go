package toolchain

import (
	"context"
	"encoding/json"
	"strings"

	"astrorder.dev/app-server/internal/envtarget"
)

const prefToolchainNetwork = "toolchain:network"

type NetworkProfile struct {
	ProxyURL string `json:"proxy_url"`
	Mirror   string `json:"mirror"`
}

type NetworkSettings struct {
	Default       NetworkProfile            `json:"default"`
	ByEnvironment map[string]NetworkProfile `json:"by_environment,omitempty"`
}

func (s *Service) GetNetworkSettings(ctx context.Context) (NetworkSettings, error) {
	settings := NetworkSettings{
		ByEnvironment: map[string]NetworkProfile{},
	}
	if s.store == nil {
		return settings, nil
	}
	pref, err := s.store.GetPreference(ctx, prefToolchainNetwork)
	if err != nil || !pref.Exists || pref.Value == nil {
		return settings, err
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return settings, nil
	}
	var parsed NetworkSettings
	if json.Unmarshal(raw, &parsed) != nil {
		return settings, nil
	}
	if parsed.ByEnvironment == nil {
		parsed.ByEnvironment = map[string]NetworkProfile{}
	}
	return parsed, nil
}

func (s *Service) SaveNetworkSettings(ctx context.Context, envID string, profile NetworkProfile) (NetworkSettings, error) {
	profile.ProxyURL = strings.TrimSpace(profile.ProxyURL)
	profile.Mirror = strings.TrimSpace(profile.Mirror)
	if _, err := buildInstallEnv(profile.ProxyURL, profile.Mirror); err != nil {
		return NetworkSettings{}, envtarget.Errorf(422, "%s", err.Error())
	}
	current, err := s.GetNetworkSettings(ctx)
	if err != nil {
		return NetworkSettings{}, envtarget.Errorf(500, "读取网络配置失败")
	}
	target := strings.TrimSpace(envID)
	if target == "" || target == "default" {
		current.Default = profile
	} else {
		if current.ByEnvironment == nil {
			current.ByEnvironment = map[string]NetworkProfile{}
		}
		if profile.ProxyURL == "" && profile.Mirror == "" {
			delete(current.ByEnvironment, target)
		} else {
			current.ByEnvironment[target] = profile
		}
	}
	if s.store != nil {
		if err := s.store.SetPreference(ctx, prefToolchainNetwork, current, true); err != nil {
			return NetworkSettings{}, envtarget.Errorf(500, "保存网络配置失败")
		}
	}
	return current, nil
}

// ResolveEffectiveNetwork resolves the proxy & mirror for one environment by
// merging request overrides > per-environment saved profile > global default.
func (s *Service) ResolveEffectiveNetwork(ctx context.Context, envID, reqProxy, reqMirror string) NetworkProfile {
	settings, err := s.GetNetworkSettings(ctx)
	out := NetworkProfile{}
	if err == nil {
		out = settings.Default
		if envProfile, ok := settings.ByEnvironment[strings.TrimSpace(envID)]; ok {
			if envProfile.ProxyURL != "" {
				out.ProxyURL = envProfile.ProxyURL
			}
			if envProfile.Mirror != "" {
				out.Mirror = envProfile.Mirror
			}
		}
	}
	if strings.TrimSpace(reqProxy) != "" {
		out.ProxyURL = strings.TrimSpace(reqProxy)
	}
	if strings.TrimSpace(reqMirror) != "" {
		out.Mirror = strings.TrimSpace(reqMirror)
	}
	return out
}
