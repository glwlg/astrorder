package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	maxSkillFileBytes      = 512 << 10
	maxBackendResponseSize = 4 << 20
)

type externalPlugin struct {
	manifest Manifest
	dir      string
}

// Manager coordinates builtin and external plugins, their storage, permissions,
// subprocesses, UI slots, agent capabilities, signals, and skills.
type Manager struct {
	mu              sync.RWMutex
	builtins        map[string]BuiltinPlugin
	externals       map[string]*externalPlugin
	store           *store.Store
	authorizer      BrowserAuthorizer
	resolver        *targetfs.Resolver
	storage         *Storage
	supervisor      *Supervisor
	pluginsDir      string
	logger          *slog.Logger
	rpcClient       *http.Client
	onSkillsChanged func()
}

// NewManager constructs a pluginhost Manager and scans PluginsDir if non-empty.
func NewManager(opts Options) *Manager {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{
		builtins:   map[string]BuiltinPlugin{},
		externals:  map[string]*externalPlugin{},
		store:      opts.Store,
		authorizer: opts.Authorizer,
		resolver:   opts.Resolver,
		storage:    NewStorage(opts.Store, opts.PluginsDir),
		supervisor: NewSupervisor(opts.HostBaseURL, logger),
		pluginsDir: strings.TrimSpace(opts.PluginsDir),
		logger:     logger,
		rpcClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	if m.pluginsDir != "" {
		if err := m.ReloadExternal(context.Background()); err != nil {
			m.logger.Warn("initial external plugin scan failed", "dir", m.pluginsDir, "error", err)
		}
	}
	return m
}

// SetHostBaseURL updates the host loopback URL advertised to spawned plugin processes.
func (m *Manager) SetHostBaseURL(baseURL string) {
	m.supervisor.SetHostBaseURL(baseURL)
}

// Close shuts down all running external plugin subprocesses.
func (m *Manager) Close() {
	m.supervisor.Close()
}

// SetOnSkillsChanged registers a non-blocking callback invoked whenever plugin
// enabled state or installed plugin skills change.
func (m *Manager) SetOnSkillsChanged(fn func()) {
	m.mu.Lock()
	m.onSkillsChanged = fn
	m.mu.Unlock()
}

func (m *Manager) notifySkillsChanged() {
	m.mu.RLock()
	fn := m.onSkillsChanged
	m.mu.RUnlock()
	if fn != nil {
		go fn()
	}
}

// RegisterBuiltin registers an in-process official plugin with the Manager.
func (m *Manager) RegisterBuiltin(p BuiltinPlugin) error {
	if p == nil {
		return fmt.Errorf("%w: builtin plugin is nil", ErrInvalid)
	}
	manifest := p.Manifest()
	manifest.Normalize()
	if manifest.Backend.Type == BackendTypeNone {
		manifest.Backend.Type = BackendTypeBuiltin
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	m.builtins[manifest.ID] = p
	delete(m.externals, manifest.ID)
	m.mu.Unlock()
	return nil
}

func (m *Manager) isBuiltin(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.builtins[id]
	return ok
}

// ListPlugins returns sorted PluginViews for all builtin and external plugins.
func (m *Manager) ListPlugins(ctx context.Context) ([]PluginView, error) {
	enabledMap, err := m.storage.GetEnabledMap(ctx)
	if err != nil {
		return nil, err
	}
	permMap, err := m.storage.GetAllGrantedPermissions(ctx)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	builtins := make([]BuiltinPlugin, 0, len(m.builtins))
	for _, b := range m.builtins {
		builtins = append(builtins, b)
	}
	externals := make([]*externalPlugin, 0, len(m.externals))
	for _, e := range m.externals {
		cp := *e
		externals = append(externals, &cp)
	}
	m.mu.RUnlock()

	views := make([]PluginView, 0, len(builtins)+len(externals))
	for _, b := range builtins {
		views = append(views, m.buildBuiltinView(b, enabledMap))
	}
	for _, e := range externals {
		views = append(views, m.buildExternalView(e, enabledMap, permMap))
	}

	sort.Slice(views, func(i, j int) bool {
		if views[i].Source != views[j].Source {
			return views[i].Source == SourceBuiltin
		}
		return views[i].ID < views[j].ID
	})
	return views, nil
}

// GetPlugin returns the PluginView for one plugin ID.
func (m *Manager) GetPlugin(ctx context.Context, id string) (PluginView, error) {
	enabledMap, err := m.storage.GetEnabledMap(ctx)
	if err != nil {
		return PluginView{}, err
	}
	permMap, err := m.storage.GetAllGrantedPermissions(ctx)
	if err != nil {
		return PluginView{}, err
	}

	m.mu.RLock()
	b, isB := m.builtins[id]
	e, isE := m.externals[id]
	var extCopy *externalPlugin
	if isE {
		cp := *e
		extCopy = &cp
	}
	m.mu.RUnlock()

	if isB {
		return m.buildBuiltinView(b, enabledMap), nil
	}
	if extCopy != nil {
		return m.buildExternalView(extCopy, enabledMap, permMap), nil
	}
	return PluginView{}, fmt.Errorf("%w: plugin %q", ErrNotFound, id)
}

// SetEnabled enables or disables a plugin, enforcing permission grants for external plugins
// and starting/stopping process backends accordingly.
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool, grantPermissions []string) (PluginView, []string, error) {
	m.mu.RLock()
	_, isB := m.builtins[id]
	ext, isE := m.externals[id]
	var extCopy *externalPlugin
	if isE {
		cp := *ext
		extCopy = &cp
	}
	m.mu.RUnlock()

	if !isB && !isE {
		return PluginView{}, nil, fmt.Errorf("%w: plugin %q", ErrNotFound, id)
	}

	if isB {
		if err := m.storage.SetEnabled(ctx, id, enabled); err != nil {
			return PluginView{}, nil, err
		}
		m.notifySkillsChanged()
		view, err := m.GetPlugin(ctx, id)
		return view, nil, err
	}

	if !enabled {
		if err := m.storage.SetEnabled(ctx, id, false); err != nil {
			return PluginView{}, nil, err
		}
		m.supervisor.Stop(id)
		m.notifySkillsChanged()
		view, err := m.GetPlugin(ctx, id)
		return view, nil, err
	}

	// External enable path: merge any newly granted permissions first.
	currentGranted, err := m.storage.GetGrantedPermissions(ctx, id)
	if err != nil {
		return PluginView{}, nil, err
	}
	if len(grantPermissions) > 0 {
		merged := mergePermissions(currentGranted, grantPermissions)
		if err := m.storage.SetGrantedPermissions(ctx, id, merged); err != nil {
			return PluginView{}, nil, err
		}
		currentGranted = merged
	}

	_, pending := computePermissionState(extCopy.manifest.Permissions, currentGranted)
	if len(pending) > 0 {
		view, _ := m.GetPlugin(ctx, id)
		return view, pending, ErrPermissionGrantRequired
	}

	if err := m.storage.SetEnabled(ctx, id, true); err != nil {
		return PluginView{}, nil, err
	}

	if extCopy.manifest.Backend.Type == BackendTypeProcess {
		if startErr := m.supervisor.Start(ctx, extCopy.manifest, extCopy.dir); startErr != nil {
			m.logger.Warn("plugin process failed to start on enable", "plugin_id", id, "error", startErr)
		}
	}

	m.notifySkillsChanged()
	view, err := m.GetPlugin(ctx, id)
	return view, nil, err
}

// Restart restarts an enabled external process plugin.
func (m *Manager) Restart(ctx context.Context, id string) (PluginView, error) {
	m.mu.RLock()
	ext, isE := m.externals[id]
	var extCopy *externalPlugin
	if isE {
		cp := *ext
		extCopy = &cp
	}
	_, isB := m.builtins[id]
	m.mu.RUnlock()

	if isB {
		return PluginView{}, fmt.Errorf("%w: builtin plugin %q does not have a restartable process", ErrInvalid, id)
	}
	if extCopy == nil {
		return PluginView{}, fmt.Errorf("%w: plugin %q", ErrNotFound, id)
	}
	if extCopy.manifest.Backend.Type != BackendTypeProcess {
		return PluginView{}, fmt.Errorf("%w: plugin %q backend type %q cannot be restarted", ErrInvalid, id, extCopy.manifest.Backend.Type)
	}

	enabled, err := m.storage.IsEnabled(ctx, id, false)
	if err != nil {
		return PluginView{}, err
	}
	if !enabled {
		return PluginView{}, fmt.Errorf("%w: plugin %q is disabled", ErrPluginDisabled, id)
	}
	granted, err := m.storage.GetGrantedPermissions(ctx, id)
	if err != nil {
		return PluginView{}, err
	}
	_, pending := computePermissionState(extCopy.manifest.Permissions, granted)
	if len(pending) > 0 {
		return PluginView{}, ErrPermissionGrantRequired
	}

	m.supervisor.Stop(id)
	_ = m.supervisor.Start(ctx, extCopy.manifest, extCopy.dir)
	return m.GetPlugin(ctx, id)
}

// GetConfig returns non-secret KV values plus masked secret indicators safe for browser UI.
func (m *Manager) GetConfig(ctx context.Context, id string) (ExtensionConfigView, error) {
	if !m.exists(id) {
		return ExtensionConfigView{}, fmt.Errorf("%w: plugin %q", ErrNotFound, id)
	}
	values, err := m.storage.GetKV(ctx, id)
	if err != nil {
		return ExtensionConfigView{}, err
	}
	secretSet, masked, err := m.storage.GetMaskedSecrets(ctx, id)
	if err != nil {
		return ExtensionConfigView{}, err
	}
	return ExtensionConfigView{
		PluginID:      id,
		Values:        values,
		SecretsSet:    secretSet,
		MaskedSecrets: masked,
	}, nil
}

// UpdateConfig merges non-secret KV values and encrypts updated secrets for plugin id.
func (m *Manager) UpdateConfig(ctx context.Context, id string, values map[string]any, secrets map[string]string) (ExtensionConfigView, error) {
	if !m.exists(id) {
		return ExtensionConfigView{}, fmt.Errorf("%w: plugin %q", ErrNotFound, id)
	}
	if values != nil {
		if _, err := m.storage.SetKV(ctx, id, values, false); err != nil {
			return ExtensionConfigView{}, err
		}
	}
	if secrets != nil {
		if err := m.storage.SetSecrets(ctx, id, secrets); err != nil {
			return ExtensionConfigView{}, err
		}
	}
	return m.GetConfig(ctx, id)
}

// ListAgentCapabilities returns all agent-callable capabilities contributed by enabled plugins.
func (m *Manager) ListAgentCapabilities(ctx context.Context) []AgentCapabilityInfo {
	enabledMap, _ := m.storage.GetEnabledMap(ctx)
	permMap, _ := m.storage.GetAllGrantedPermissions(ctx)

	m.mu.RLock()
	builtins := make([]BuiltinPlugin, 0, len(m.builtins))
	for _, b := range m.builtins {
		builtins = append(builtins, b)
	}
	externals := make([]*externalPlugin, 0, len(m.externals))
	for _, e := range m.externals {
		cp := *e
		externals = append(externals, &cp)
	}
	m.mu.RUnlock()

	out := []AgentCapabilityInfo{}

	for _, b := range builtins {
		manifest := b.Manifest()
		manifest.Normalize()
		if !isPluginEnabledInMap(enabledMap, manifest.ID, true) {
			continue
		}
		for _, capItem := range manifest.Contributes.AgentCapabilities {
			out = append(out, AgentCapabilityInfo{
				PluginID:       manifest.ID,
				PluginName:     manifest.Name,
				CapabilityID:   capItem.ID,
				FullID:         "ext." + manifest.ID + "." + capItem.ID,
				CLIAlias:       append([]string(nil), capItem.CLIAlias...),
				Summary:        capItem.Summary,
				InputSchema:    capItem.InputSchema,
				RequiresCaller: capItem.RequiresCaller,
			})
		}
	}

	for _, ext := range externals {
		manifest := ext.manifest
		if !isPluginEnabledInMap(enabledMap, manifest.ID, false) {
			continue
		}
		granted, pending := computePermissionState(manifest.Permissions, permMap[manifest.ID])
		if len(pending) > 0 || !hasPermission(granted, PermAgentCapabilities) {
			continue
		}
		for _, capItem := range manifest.Contributes.AgentCapabilities {
			out = append(out, AgentCapabilityInfo{
				PluginID:       manifest.ID,
				PluginName:     manifest.Name,
				CapabilityID:   capItem.ID,
				FullID:         "ext." + manifest.ID + "." + capItem.ID,
				CLIAlias:       append([]string(nil), capItem.CLIAlias...),
				Summary:        capItem.Summary,
				InputSchema:    capItem.InputSchema,
				RequiresCaller: capItem.RequiresCaller,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].FullID < out[j].FullID
	})
	return out
}

// InvokeAgentCapability invokes an agent capability exposed by an enabled builtin or external plugin.
func (m *Manager) InvokeAgentCapability(ctx context.Context, pluginID, capID string, payload map[string]any) (any, error) {
	if payload == nil {
		payload = map[string]any{}
	}

	m.mu.RLock()
	builtin, isB := m.builtins[pluginID]
	ext, isE := m.externals[pluginID]
	var extCopy *externalPlugin
	if isE {
		cp := *ext
		extCopy = &cp
	}
	m.mu.RUnlock()

	if !isB && !isE {
		return nil, fmt.Errorf("%w: plugin %q", ErrNotFound, pluginID)
	}

	if isB {
		enabled, err := m.storage.IsEnabled(ctx, pluginID, true)
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, fmt.Errorf("%w: plugin %q", ErrPluginDisabled, pluginID)
		}
		return builtin.InvokeCapability(ctx, capID, payload)
	}

	// External plugin invocation.
	enabled, err := m.storage.IsEnabled(ctx, pluginID, false)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, fmt.Errorf("%w: plugin %q", ErrPluginDisabled, pluginID)
	}
	granted, err := m.storage.GetGrantedPermissions(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	_, pending := computePermissionState(extCopy.manifest.Permissions, granted)
	if len(pending) > 0 || !hasPermission(granted, PermAgentCapabilities) {
		return nil, fmt.Errorf("%w: plugin %q lacks %s permission", ErrPermissionDenied, pluginID, PermAgentCapabilities)
	}

	var matchedCap *AgentCapabilityContribution
	for i := range extCopy.manifest.Contributes.AgentCapabilities {
		if extCopy.manifest.Contributes.AgentCapabilities[i].ID == capID {
			matchedCap = &extCopy.manifest.Contributes.AgentCapabilities[i]
			break
		}
	}
	if matchedCap == nil {
		return nil, fmt.Errorf("%w: capability %q on plugin %q", ErrNotFound, capID, pluginID)
	}

	baseURL, err := m.resolveExternalBackendURL(ctx, extCopy)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(matchedCap.Endpoint)
	if endpoint == "" {
		endpoint = "/agent/" + url.PathEscape(capID)
	}
	targetURL := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal payload: %v", ErrInvalid, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Astrorder-Plugin-ID", pluginID)

	resp, err := m.rpcClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("invoke plugin %q capability %q: %w", pluginID, capID, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBackendResponseSize))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plugin %q capability %q returned HTTP %d: %s", pluginID, capID, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if len(bytes.TrimSpace(respBody)) == 0 {
		return map[string]any{"ok": true}, nil
	}
	var decoded any
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return map[string]any{"text": string(respBody)}, nil
	}
	return decoded, nil
}

// ResolveCLIAlias matches the longest CLI alias prefix among enabled plugin capabilities.
func (m *Manager) ResolveCLIAlias(ctx context.Context, words []string) (pluginID, capID string, rest []string, ok bool) {
	if len(words) == 0 {
		return "", "", nil, false
	}
	caps := m.ListAgentCapabilities(ctx)
	bestLen := 0
	for _, c := range caps {
		if len(c.CLIAlias) == 0 || len(c.CLIAlias) > len(words) || len(c.CLIAlias) <= bestLen {
			continue
		}
		matched := true
		for i, tok := range c.CLIAlias {
			if !strings.EqualFold(strings.TrimSpace(words[i]), strings.TrimSpace(tok)) {
				matched = false
				break
			}
		}
		if matched {
			bestLen = len(c.CLIAlias)
			pluginID = c.PluginID
			capID = c.CapabilityID
			rest = append([]string(nil), words[len(c.CLIAlias):]...)
			ok = true
		}
	}
	return pluginID, capID, rest, ok
}

// CollectSignals aggregates signal items across all enabled builtin and external plugins
// that declare a signal provider for kind.
func (m *Manager) CollectSignals(ctx context.Context, kind string, q SignalQuery) []SignalItem {
	enabledMap, _ := m.storage.GetEnabledMap(ctx)
	permMap, _ := m.storage.GetAllGrantedPermissions(ctx)

	m.mu.RLock()
	builtins := make([]BuiltinPlugin, 0, len(m.builtins))
	for _, b := range m.builtins {
		builtins = append(builtins, b)
	}
	externals := make([]*externalPlugin, 0, len(m.externals))
	for _, e := range m.externals {
		cp := *e
		externals = append(externals, &cp)
	}
	m.mu.RUnlock()

	results := []SignalItem{}

	for _, b := range builtins {
		manifest := b.Manifest()
		manifest.Normalize()
		if !isPluginEnabledInMap(enabledMap, manifest.ID, true) {
			continue
		}
		if kind != "" && !declaresSignalKind(manifest, kind) {
			continue
		}
		items, err := b.CollectSignals(ctx, kind, q)
		if err != nil {
			m.logger.Debug("builtin signal provider failed", "plugin_id", manifest.ID, "kind", kind, "error", err)
			continue
		}
		for _, item := range items {
			if item.SourcePlugin == "" {
				item.SourcePlugin = manifest.ID
			}
			if item.Kind == "" {
				item.Kind = kind
			}
			results = append(results, item)
		}
	}

	for _, ext := range externals {
		manifest := ext.manifest
		if !isPluginEnabledInMap(enabledMap, manifest.ID, false) {
			continue
		}
		granted, pending := computePermissionState(manifest.Permissions, permMap[manifest.ID])
		if len(pending) > 0 || !hasPermission(granted, PermSignalsProvide) {
			continue
		}
		for _, sp := range manifest.Contributes.SignalProviders {
			if kind != "" && sp.Kind != kind {
				continue
			}
			items := m.collectExternalSignals(ctx, ext, sp, q)
			results = append(results, items...)
		}
	}

	if q.Limit > 0 && len(results) > q.Limit {
		results = results[:q.Limit]
	}
	return results
}

func (m *Manager) collectExternalSignals(ctx context.Context, ext *externalPlugin, sp SignalProviderContribution, q SignalQuery) []SignalItem {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	baseURL, err := m.resolveExternalBackendURL(reqCtx, ext)
	if err != nil {
		return nil
	}
	endpoint := strings.TrimSpace(sp.Endpoint)
	if endpoint == "" {
		endpoint = "/signals/" + url.PathEscape(sp.Kind)
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/"))
	if err != nil {
		return nil
	}
	qs := u.Query()
	if q.Workspace != "" {
		qs.Set("workspace", q.Workspace)
	}
	if q.ConnectionID != "" {
		qs.Set("connection_id", q.ConnectionID)
	}
	if q.SessionID != "" {
		qs.Set("session_id", q.SessionID)
	}
	if q.Limit > 0 {
		qs.Set("limit", strconv.Itoa(q.Limit))
	}
	if sp.Kind != "" {
		qs.Set("kind", sp.Kind)
	}
	u.RawQuery = qs.Encode()

	bodyPayload := map[string]any{
		"kind":          sp.Kind,
		"workspace":     q.Workspace,
		"connection_id": q.ConnectionID,
		"session_id":    q.SessionID,
		"limit":         q.Limit,
	}
	rawBody, _ := json.Marshal(bodyPayload)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, u.String(), bytes.NewReader(rawBody))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Astrorder-Plugin-ID", ext.manifest.ID)

	resp, err := m.rpcClient.Do(req)
	if err == nil && resp.StatusCode == http.StatusMethodNotAllowed {
		_ = resp.Body.Close()
		getReq, getErr := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
		if getErr == nil {
			getReq.Header.Set("X-Astrorder-Plugin-ID", ext.manifest.ID)
			resp, err = m.rpcClient.Do(getReq)
		}
	}
	if err != nil {
		m.logger.Debug("external signal provider request failed", "plugin_id", ext.manifest.ID, "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBackendResponseSize))
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	items := parseSignalItems(raw)
	for i := range items {
		if items[i].SourcePlugin == "" {
			items[i].SourcePlugin = ext.manifest.ID
		}
		if items[i].Kind == "" {
			items[i].Kind = sp.Kind
		}
	}
	return items
}

func parseSignalItems(raw []byte) []SignalItem {
	var direct []SignalItem
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct
	}
	var wrapped struct {
		Items   []SignalItem `json:"items"`
		Signals []SignalItem `json:"signals"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		if len(wrapped.Items) > 0 {
			return wrapped.Items
		}
		return wrapped.Signals
	}
	return nil
}

// ListEnabledSkills reads SKILL.md entries contributed by all enabled builtin
// and external plugins.
func (m *Manager) ListEnabledSkills(ctx context.Context) []PluginSkillEntry {
	enabledMap, _ := m.storage.GetEnabledMap(ctx)
	permMap, _ := m.storage.GetAllGrantedPermissions(ctx)

	m.mu.RLock()
	builtins := make([]BuiltinPlugin, 0, len(m.builtins))
	for _, b := range m.builtins {
		builtins = append(builtins, b)
	}
	externals := make([]*externalPlugin, 0, len(m.externals))
	for _, e := range m.externals {
		cp := *e
		externals = append(externals, &cp)
	}
	m.mu.RUnlock()

	sort.Slice(builtins, func(i, j int) bool {
		return builtins[i].Manifest().ID < builtins[j].Manifest().ID
	})
	sort.Slice(externals, func(i, j int) bool {
		return externals[i].manifest.ID < externals[j].manifest.ID
	})

	entries := []PluginSkillEntry{}
	for _, b := range builtins {
		manifest := b.Manifest()
		manifest.Normalize()
		if !isPluginEnabledInMap(enabledMap, manifest.ID, true) {
			continue
		}
		provider, ok := b.(BuiltinSkillProvider)
		if !ok {
			continue
		}
		for _, item := range provider.Skills() {
			if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Content) == "" {
				continue
			}
			if strings.TrimSpace(item.PluginID) == "" {
				item.PluginID = manifest.ID
			}
			if strings.TrimSpace(item.DisplayName) == "" {
				item.DisplayName = item.Name
			}
			entries = append(entries, item)
		}
	}
	for _, ext := range externals {
		if !isPluginEnabledInMap(enabledMap, ext.manifest.ID, false) {
			continue
		}
		_, pending := computePermissionState(ext.manifest.Permissions, permMap[ext.manifest.ID])
		if len(pending) > 0 {
			continue
		}
		for _, skillRel := range ext.manifest.Contributes.AgentSkills {
			cleanRel, err := ValidateRelativePluginPath(skillRel)
			if err != nil {
				continue
			}
			fullPath := filepath.Join(ext.dir, filepath.FromSlash(cleanRel))
			if !isPathStrictlyInside(ext.dir, fullPath) {
				continue
			}
			info, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			skillFile := fullPath
			fallbackName := strings.TrimSuffix(filepath.Base(cleanRel), filepath.Ext(cleanRel))
			if info.IsDir() {
				skillFile = filepath.Join(fullPath, "SKILL.md")
				fallbackName = filepath.Base(cleanRel)
			} else if strings.EqualFold(filepath.Base(cleanRel), "SKILL.md") {
				parent := filepath.Base(filepath.Dir(fullPath))
				if parent != "" && parent != "." && parent != filepath.Base(ext.dir) {
					fallbackName = parent
				}
			}
			if !isPathStrictlyInside(ext.dir, skillFile) {
				continue
			}
			data, err := readBoundedFile(skillFile, maxSkillFileBytes)
			if err != nil {
				continue
			}
			content := string(data)
			name, displayName, description := parseSkillFrontmatter(content)
			if name == "" {
				name = fallbackName
			}
			if displayName == "" {
				displayName = name
			}
			entries = append(entries, PluginSkillEntry{
				PluginID:    ext.manifest.ID,
				Name:        name,
				DisplayName: displayName,
				Description: description,
				Content:     content,
			})
		}
	}
	return entries
}

func (m *Manager) resolveExternalBackendURL(ctx context.Context, ext *externalPlugin) (string, error) {
	switch ext.manifest.Backend.Type {
	case BackendTypeHTTP:
		if ext.manifest.Backend.URL == "" {
			return "", fmt.Errorf("plugin %q has empty http backend url", ext.manifest.ID)
		}
		return ext.manifest.Backend.URL, nil
	case BackendTypeProcess:
		st := m.supervisor.Status(ext.manifest.ID)
		if st.State == RuntimeStateReady && st.BaseURL != "" {
			return st.BaseURL, nil
		}
		if err := m.supervisor.Start(ctx, ext.manifest, ext.dir); err != nil {
			return "", err
		}
		st = m.supervisor.Status(ext.manifest.ID)
		if st.State != RuntimeStateReady || st.BaseURL == "" {
			return "", fmt.Errorf("plugin %q process is not ready (%s)", ext.manifest.ID, st.State)
		}
		return st.BaseURL, nil
	default:
		return "", fmt.Errorf("plugin %q has no backend (%s)", ext.manifest.ID, ext.manifest.Backend.Type)
	}
}

func (m *Manager) exists(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.builtins[id]; ok {
		return true
	}
	_, ok := m.externals[id]
	return ok
}

func (m *Manager) buildBuiltinView(b BuiltinPlugin, enabledMap map[string]bool) PluginView {
	manifest := b.Manifest()
	manifest.Normalize()
	perms := nonNilStrings(manifest.Permissions)
	return PluginView{
		ID:                 manifest.ID,
		Name:               manifest.Name,
		Version:            manifest.Version,
		Category:           manifest.Category,
		Description:        manifest.Description,
		Author:             manifest.Author,
		Homepage:           manifest.Homepage,
		Icon:               manifest.Icon,
		Source:             SourceBuiltin,
		Enabled:            isPluginEnabledInMap(enabledMap, manifest.ID, true),
		Permissions:        perms,
		GrantedPermissions: append([]string(nil), perms...),
		PendingPermissions: []string{},
		PermissionsGranted: true,
		RuntimeState:       RuntimeStateBuiltin,
		BackendType:        BackendTypeBuiltin,
		Settings:           manifest.Settings,
		Contributes:        manifest.Contributes,
		APIBaseURL:         BaseExtPath + "/" + url.PathEscape(manifest.ID),
	}
}

func (m *Manager) buildExternalView(ext *externalPlugin, enabledMap map[string]bool, permMap map[string][]string) PluginView {
	manifest := ext.manifest
	enabled := isPluginEnabledInMap(enabledMap, manifest.ID, false)
	granted, pending := computePermissionState(manifest.Permissions, permMap[manifest.ID])

	runtimeState := RuntimeStateStopped
	runtimeErr := ""
	switch manifest.Backend.Type {
	case BackendTypeProcess:
		st := m.supervisor.Status(manifest.ID)
		runtimeState = st.State
		runtimeErr = st.Error
	case BackendTypeHTTP, BackendTypeNone:
		if enabled && len(pending) == 0 {
			runtimeState = RuntimeStateReady
		} else {
			runtimeState = RuntimeStateStopped
		}
	}

	return PluginView{
		ID:                 manifest.ID,
		Name:               manifest.Name,
		Version:            manifest.Version,
		Category:           manifest.Category,
		Description:        manifest.Description,
		Author:             manifest.Author,
		Homepage:           manifest.Homepage,
		Icon:               manifest.Icon,
		Source:             SourceExternal,
		Enabled:            enabled,
		Permissions:        nonNilStrings(manifest.Permissions),
		GrantedPermissions: granted,
		PendingPermissions: pending,
		PermissionsGranted: len(pending) == 0,
		RuntimeState:       runtimeState,
		RuntimeError:       runtimeErr,
		BackendType:        manifest.Backend.Type,
		Settings:           manifest.Settings,
		Contributes:        manifest.Contributes,
		UIBaseURL:          BaseExtPath + "/" + url.PathEscape(manifest.ID) + "/_ui/",
		APIBaseURL:         BaseExtPath + "/" + url.PathEscape(manifest.ID),
	}
}

func isPluginEnabledInMap(enabledMap map[string]bool, id string, isBuiltin bool) bool {
	if flag, exists := enabledMap[id]; exists {
		return flag
	}
	return isBuiltin
}

func computePermissionState(required, storedGranted []string) (granted []string, pending []string) {
	grantedSet := make(map[string]struct{}, len(storedGranted))
	for _, p := range storedGranted {
		grantedSet[p] = struct{}{}
	}
	granted = []string{}
	pending = []string{}
	for _, req := range required {
		if _, ok := grantedSet[req]; ok {
			granted = append(granted, req)
		} else {
			pending = append(pending, req)
		}
	}
	return granted, pending
}

func mergePermissions(existing, added []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(existing)+len(added))
	for _, list := range [][]string{existing, added} {
		for _, p := range list {
			t := strings.TrimSpace(p)
			if t == "" || !IsKnownPermission(t) {
				continue
			}
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

func nonNilStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func declaresSignalKind(m Manifest, kind string) bool {
	for _, sp := range m.Contributes.SignalProviders {
		if sp.Kind == kind {
			return true
		}
	}
	return false
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

func parseSkillFrontmatter(content string) (name, displayName, description string) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	trimmed := strings.TrimLeft(normalized, "\ufeff")
	if !strings.HasPrefix(trimmed, "---\n") {
		return "", "", ""
	}
	rest := trimmed[4:]
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return "", "", ""
	}
	header := rest[:endIdx]
	for _, line := range strings.Split(header, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		val = strings.Trim(val, `"'`)
		switch key {
		case "name":
			name = val
		case "display_name", "displayname", "title":
			displayName = val
		case "description":
			description = val
		}
	}
	return name, displayName, description
}
