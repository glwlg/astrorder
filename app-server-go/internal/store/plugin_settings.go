package store

import (
	"context"
	"strings"
)

const (
	PrefPluginsEnabled = "plugins:enabled"
	PrefPluginsConfig  = "plugins:config"
)

var AgentKinds = []string{"antigravity", "codex", "hermes", "grok"}

func CanonicalAgentKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "antigravity", "antigravity-ssh", "agy":
		return "antigravity"
	case "codex", "codex-ssh":
		return "codex"
	case "hermes", "ssh-hermes", "hermes-ssh":
		return "hermes"
	case "grok", "grok-ssh":
		return "grok"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func AgentPluginID(kind string) string {
	c := CanonicalAgentKind(kind)
	switch c {
	case "antigravity", "codex", "hermes", "grok":
		return "agent-" + c
	default:
		return ""
	}
}

// ReadEnabledAgentKinds returns the enabled flag for each canonical Agent kind.
// Kinds without an explicit saved preference default to true until bootstrap runs.
func (s *Store) ReadEnabledAgentKinds(ctx context.Context) map[string]bool {
	result := map[string]bool{
		"antigravity": true,
		"codex":       true,
		"hermes":      true,
		"grok":        true,
	}
	if s == nil {
		return result
	}
	pref, err := s.GetPreference(ctx, PrefPluginsEnabled)
	if err != nil || !pref.Exists {
		return result
	}
	saved, ok := pref.Value.(map[string]any)
	if !ok {
		return result
	}
	for _, kind := range AgentKinds {
		if raw, exists := saved[AgentPluginID(kind)]; exists {
			if flag, ok := raw.(bool); ok {
				result[kind] = flag
			}
		}
	}
	return result
}

// ReadLocalExecutableOverride returns the saved custom local executable path
// for a plugin ID (e.g. "agent-codex" or "toolchain-uv"), or "" if unset.
func (s *Store) ReadLocalExecutableOverride(ctx context.Context, pluginID string) string {
	if s == nil || strings.TrimSpace(pluginID) == "" {
		return ""
	}
	pref, err := s.GetPreference(ctx, PrefPluginsConfig)
	if err != nil || !pref.Exists {
		return ""
	}
	saved, ok := pref.Value.(map[string]any)
	if !ok {
		return ""
	}
	cfg, ok := saved[pluginID].(map[string]any)
	if !ok {
		return ""
	}
	val, _ := cfg["executable"].(string)
	return strings.TrimSpace(val)
}

// SavePluginConfigValue persists one config key under plugins:config[pluginID].
func (s *Store) SavePluginConfigValue(ctx context.Context, pluginID, key string, value any) error {
	if s == nil {
		return nil
	}
	all := map[string]any{}
	if pref, err := s.GetPreference(ctx, PrefPluginsConfig); err != nil {
		return err
	} else if pref.Exists {
		if saved, ok := pref.Value.(map[string]any); ok {
			for k, v := range saved {
				all[k] = v
			}
		}
	}
	entry, ok := all[pluginID].(map[string]any)
	if !ok || entry == nil {
		entry = map[string]any{}
	}
	cloned := make(map[string]any, len(entry)+1)
	for k, v := range entry {
		cloned[k] = v
	}
	if strVal, ok := value.(string); ok && strings.TrimSpace(strVal) == "" {
		delete(cloned, key)
	} else {
		cloned[key] = value
	}
	all[pluginID] = cloned
	return s.SetPreference(ctx, PrefPluginsConfig, all, true)
}

// BootstrapAgentPluginDefaults initializes missing agent-* plugin flags once
// using existing agents/connection choices plus an optional local binary probe.
// Existing saved flags are never overwritten.
func (s *Store) BootstrapAgentPluginDefaults(ctx context.Context, probeInstalled func(kind string) bool) (map[string]bool, error) {
	if s == nil {
		return map[string]bool{}, nil
	}
	enabled := map[string]bool{}
	if pref, err := s.GetPreference(ctx, PrefPluginsEnabled); err != nil {
		return nil, err
	} else if pref.Exists {
		if saved, ok := pref.Value.(map[string]any); ok {
			for id, raw := range saved {
				if key := strings.TrimSpace(id); key != "" {
					if flag, ok := raw.(bool); ok {
						enabled[key] = flag
					}
				}
			}
		}
	}

	missing := make([]string, 0, len(AgentKinds))
	for _, kind := range AgentKinds {
		if _, exists := enabled[AgentPluginID(kind)]; !exists {
			missing = append(missing, kind)
		}
	}
	if len(missing) == 0 {
		return enabled, nil
	}

	knownActive := map[string]bool{}
	if agents, err := s.ListCurrentAgents(ctx); err == nil {
		for _, a := range agents {
			k, _ := a["kind"].(string)
			if ck := CanonicalAgentKind(k); ck != "" {
				knownActive[ck] = true
			}
		}
	}
	for _, kind := range missing {
		if chosen, present, err := s.GetAgentConnectionChoice(ctx, "local:"+kind); err == nil && present && chosen {
			knownActive[kind] = true
		}
	}

	for _, kind := range missing {
		active := knownActive[kind]
		if !active && probeInstalled != nil {
			active = probeInstalled(kind)
		}
		enabled[AgentPluginID(kind)] = active
	}

	if err := s.SetPreference(ctx, PrefPluginsEnabled, enabled, true); err != nil {
		return nil, err
	}
	return enabled, nil
}
