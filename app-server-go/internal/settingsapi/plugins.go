package settingsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"astrorder.dev/app-server/internal/store"
)

const (
	prefPluginsEnabled = store.PrefPluginsEnabled
	prefPluginsConfig  = store.PrefPluginsConfig
	maxPluginBody      = 256 * 1024
)

type pluginSettingsView struct {
	Enabled map[string]bool           `json:"enabled"`
	Configs map[string]map[string]any `json:"configs"`
}

type pluginSettingsPatch struct {
	Enabled    map[string]bool           `json:"enabled"`
	Configs    map[string]map[string]any `json:"configs"`
	ImportOnly bool                      `json:"import_only"`
}

func (h *handler) loadPluginSettings(ctx context.Context) (pluginSettingsView, error) {
	view := pluginSettingsView{
		Enabled: map[string]bool{},
		Configs: map[string]map[string]any{},
	}
	if h.Store == nil {
		return view, nil
	}
	if pref, err := h.Store.GetPreference(ctx, prefPluginsEnabled); err != nil {
		return view, err
	} else if pref.Exists {
		if saved, ok := pref.Value.(map[string]any); ok {
			for id, raw := range saved {
				if key := strings.TrimSpace(id); key != "" {
					if flag, ok := raw.(bool); ok {
						view.Enabled[key] = flag
					}
				}
			}
		}
	}
	if pref, err := h.Store.GetPreference(ctx, prefPluginsConfig); err != nil {
		return view, err
	} else if pref.Exists {
		if saved, ok := pref.Value.(map[string]any); ok {
			for id, raw := range saved {
				key := strings.TrimSpace(id)
				if key == "" {
					continue
				}
				if cfg, ok := raw.(map[string]any); ok {
					cloned := make(map[string]any, len(cfg))
					for k, v := range cfg {
						cloned[k] = v
					}
					view.Configs[key] = cloned
				}
			}
		}
	}
	return view, nil
}

func (h *handler) getPluginSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Store != nil && h.AgentInstalledProbe != nil {
		_, _ = h.Store.BootstrapAgentPluginDefaults(r.Context(), h.AgentInstalledProbe)
	}
	view, err := h.loadPluginSettings(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "Preference storage is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) setPluginSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var patch pluginSettingsPatch
	dec := json.NewDecoder(io.LimitReader(r.Body, maxPluginBody))
	if err := dec.Decode(&patch); err != nil {
		fail(w, http.StatusUnprocessableEntity, "Invalid request")
		return
	}
	if len(patch.Enabled) > 200 || len(patch.Configs) > 200 {
		fail(w, http.StatusUnprocessableEntity, "Too many plugin settings entries")
		return
	}
	current, err := h.loadPluginSettings(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "Preference storage is unavailable")
		return
	}
	enabledChanged := false
	for id, flag := range patch.Enabled {
		key := strings.TrimSpace(id)
		if key == "" || len(key) > 128 {
			fail(w, http.StatusUnprocessableEntity, "Invalid plugin id")
			return
		}
		if patch.ImportOnly {
			if _, exists := current.Enabled[key]; exists {
				continue
			}
		}
		current.Enabled[key] = flag
		enabledChanged = true
	}
	configChanged := false
	for id, cfg := range patch.Configs {
		key := strings.TrimSpace(id)
		if key == "" || len(key) > 128 {
			fail(w, http.StatusUnprocessableEntity, "Invalid plugin id")
			return
		}
		if len(cfg) > 100 {
			fail(w, http.StatusUnprocessableEntity, "Too many plugin config options")
			return
		}
		existing := current.Configs[key]
		if existing == nil {
			existing = map[string]any{}
		}
		for optKey, optVal := range cfg {
			if strings.TrimSpace(optKey) == "" || len(optKey) > 128 {
				fail(w, http.StatusUnprocessableEntity, "Invalid plugin config key")
				return
			}
			if patch.ImportOnly {
				if _, exists := existing[optKey]; exists {
					continue
				}
			}
			existing[optKey] = optVal
			configChanged = true
		}
		current.Configs[key] = existing
	}
	if enabledChanged {
		if err := h.Store.SetPreference(r.Context(), prefPluginsEnabled, current.Enabled, true); err != nil {
			fail(w, http.StatusInternalServerError, "Preference storage is unavailable")
			return
		}
	}
	if configChanged {
		if err := h.Store.SetPreference(r.Context(), prefPluginsConfig, current.Configs, true); err != nil {
			fail(w, http.StatusInternalServerError, "Preference storage is unavailable")
			return
		}
	}
	writeJSON(w, http.StatusOK, current)
}
