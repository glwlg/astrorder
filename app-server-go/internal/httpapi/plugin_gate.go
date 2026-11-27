package httpapi

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/pluginhost"
	"astrorder.dev/app-server/internal/store"
)

const (
	// Built-in Category-3 workspace module plugin IDs gated at the Go HTTP layer.
	PluginLearningRadar    = "workspace-learning-radar"
	PluginBackups          = "workspace-backups"
	PluginDigest           = "workspace-digest"
	PluginMeetings         = "workspace-meetings"
	PluginGroups           = "workspace-groups"
	PluginAnalyticsGateway = "workspace-analytics-gateway"
	PluginAgentAccounts    = "workspace-agent-accounts"
	PluginIntegrations     = "sidebar-integrations"

	defaultPluginGateCacheTTL = 2 * time.Second
)

// PluginGate evaluates whether a built-in workspace plugin is enabled according to
// the persisted plugins:enabled preference, backed by a short-TTL cache that is
// invalidated immediately on plugin toggle mutations.
type PluginGate struct {
	store *store.Store
	ttl   time.Duration

	mu        sync.RWMutex
	cached    map[string]bool
	expiresAt time.Time
}

// NewPluginGate creates a PluginGate for st with the given cache TTL.
func NewPluginGate(st *store.Store, ttl time.Duration) *PluginGate {
	if ttl <= 0 {
		ttl = defaultPluginGateCacheTTL
	}
	return &PluginGate{
		store: st,
		ttl:   ttl,
	}
}

// Invalidate drops the cached plugins:enabled snapshot so the next check reads
// directly from SQLite.
func (g *PluginGate) Invalidate() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.cached = nil
	g.expiresAt = time.Time{}
	g.mu.Unlock()
}

// IsEnabled reports whether pluginID is currently enabled. Missing keys or a nil
// store default to true (built-in workspace modules are enabled by default).
func (g *PluginGate) IsEnabled(ctx context.Context, pluginID string) bool {
	if g == nil || g.store == nil {
		return true
	}
	id := strings.TrimSpace(pluginID)
	if id == "" {
		return true
	}

	now := time.Now()
	g.mu.RLock()
	if g.cached != nil && now.Before(g.expiresAt) {
		flag, exists := g.cached[id]
		g.mu.RUnlock()
		if exists {
			return flag
		}
		return true
	}
	g.mu.RUnlock()

	snapshot, err := loadPluginEnabledSnapshot(ctx, g.store)
	if err != nil {
		return true
	}

	g.mu.Lock()
	g.cached = snapshot
	g.expiresAt = time.Now().Add(g.ttl)
	flag, exists := g.cached[id]
	g.mu.Unlock()
	if exists {
		return flag
	}
	return true
}

// Middleware returns an http.Handler middleware that rejects requests with HTTP 403
// when pluginID is disabled in plugins:enabled.
func (g *PluginGate) Middleware(pluginID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !g.IsEnabled(r.Context(), pluginID) {
				writePluginDisabledError(w, pluginID)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PluginGateMiddleware returns a standalone middleware backed by st that gates
// requests for pluginID, returning 403 {"detail":"插件已停用","plugin_id":...}
// when the plugin is disabled in plugins:enabled.
func PluginGateMiddleware(st *store.Store, pluginID string) func(http.Handler) http.Handler {
	gate := NewPluginGate(st, 0)
	// Standalone helper performs live store reads so external toggles take effect immediately.
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if st != nil {
				snapshot, err := loadPluginEnabledSnapshot(r.Context(), st)
				if err == nil {
					if enabled, exists := snapshot[strings.TrimSpace(pluginID)]; exists && !enabled {
						writePluginDisabledError(w, pluginID)
						return
					}
				}
			} else if !gate.IsEnabled(r.Context(), pluginID) {
				writePluginDisabledError(w, pluginID)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func loadPluginEnabledSnapshot(ctx context.Context, st *store.Store) (map[string]bool, error) {
	if st == nil {
		return map[string]bool{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pref, err := st.GetPreference(ctx, store.PrefPluginsEnabled)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	if !pref.Exists {
		return out, nil
	}
	if saved, ok := pref.Value.(map[string]any); ok {
		for id, raw := range saved {
			key := strings.TrimSpace(id)
			if key == "" {
				continue
			}
			if flag, ok := raw.(bool); ok {
				out[key] = flag
			}
		}
	}
	return out, nil
}

func writePluginDisabledError(w http.ResponseWriter, pluginID string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusForbidden, map[string]any{
		"detail":    "插件已停用",
		"plugin_id": pluginID,
	})
}

func isAgentAccountsRoute(reqPath string) bool {
	clean := strings.TrimRight(reqPath, "/")
	return clean == "/api/v1/agy-accounts" ||
		strings.HasPrefix(clean, "/api/v1/agy-accounts/") ||
		clean == "/api/v1/agent-accounts" ||
		strings.HasPrefix(clean, "/api/v1/agent-accounts/")
}

func isAnalyticsGatewayRoute(reqPath string) bool {
	clean := strings.TrimRight(reqPath, "/")
	return clean == "/api/v1/analytics" ||
		strings.HasPrefix(clean, "/api/v1/analytics/") ||
		clean == "/api/v1/model-sync" ||
		strings.HasPrefix(clean, "/api/v1/model-sync/")
}

func isPluginSettingsMutation(method, reqPath string) bool {
	if method != http.MethodPut && method != http.MethodPost {
		return false
	}
	clean := strings.TrimRight(reqPath, "/")
	return clean == "/api/v1/plugins/settings" || clean == "/api/v1/settings/plugins"
}

func publishExtensionChange(hub *application.EventHub, ev pluginhost.ExtensionChangeEvent) {
	if hub == nil {
		return
	}
	payload := map[string]any{
		"action":    ev.Action,
		"timestamp": ev.Timestamp,
	}
	if ev.PluginID != "" {
		payload["plugin_id"] = ev.PluginID
	}
	if ev.Source != "" {
		payload["source"] = ev.Source
	}
	if ev.Enabled != nil {
		payload["enabled"] = *ev.Enabled
	}
	if ev.ContentDigest != "" {
		payload["content_digest"] = ev.ContentDigest
	}
	if len(ev.ChangedIDs) > 0 {
		payload["changed_ids"] = ev.ChangedIDs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = hub.Publish(ctx, "extension.changed", payload)
}
