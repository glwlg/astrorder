package pluginhost

import (
	"bytes"
	"context"
	"crypto/ed25519"
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
	manifest          Manifest
	dir               string
	source            string // SourceOfficial or SourceExternal
	contentDigest     string
	signatureVerified bool
}

// Manager coordinates builtin, official, and external plugins, their storage, permissions,
// subprocesses, UI slots, agent capabilities, signals, and skills.
type Manager struct {
	mu                 sync.RWMutex
	builtins           map[string]BuiltinPlugin
	builtinFrontends   map[string]*externalPlugin
	officials          map[string]*externalPlugin
	externals          map[string]*externalPlugin
	store              *store.Store
	authorizer         BrowserAuthorizer
	resolver           *targetfs.Resolver
	storage            *Storage
	supervisor         *Supervisor
	pluginsDir         string
	officialPluginsDir string
	legacyPluginsDir   string
	trustedPublicKeys  map[string]ed25519.PublicKey
	appProfileOverride string
	renameFn           func(oldPath, newPath string) error
	logger             *slog.Logger
	rpcClient          *http.Client
	onSkillsChanged    func()
	onChange           func(ExtensionChangeEvent)
}

// NewManager constructs a pluginhost Manager and scans OfficialPluginsDir / PluginsDir if non-empty.
func NewManager(opts Options) *Manager {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	var trusted map[string]ed25519.PublicKey
	if len(opts.TrustedPublicKeys) > 0 {
		trusted = make(map[string]ed25519.PublicKey, len(opts.TrustedPublicKeys))
		for k, v := range opts.TrustedPublicKeys {
			cp := make(ed25519.PublicKey, len(v))
			copy(cp, v)
			trusted[k] = cp
		}
	}
	m := &Manager{
		builtins:           map[string]BuiltinPlugin{},
		builtinFrontends:   map[string]*externalPlugin{},
		officials:          map[string]*externalPlugin{},
		externals:          map[string]*externalPlugin{},
		store:              opts.Store,
		authorizer:         opts.Authorizer,
		resolver:           opts.Resolver,
		storage:            NewStorage(opts.Store, opts.PluginsDir),
		supervisor:         NewSupervisor(opts.HostBaseURL, logger),
		pluginsDir:         strings.TrimSpace(opts.PluginsDir),
		officialPluginsDir: strings.TrimSpace(opts.OfficialPluginsDir),
		legacyPluginsDir:   strings.TrimSpace(opts.LegacyPluginsDir),
		trustedPublicKeys:  trusted,
		appProfileOverride: strings.TrimSpace(opts.AppProfileOverride),
		logger:             logger,
		onChange:           opts.OnChange,
		rpcClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	if m.officialPluginsDir != "" || m.pluginsDir != "" || m.legacyPluginsDir != "" {
		if err := m.reloadDiskPlugins(context.Background(), false); err != nil {
			m.logger.Warn("initial plugin directory scan failed", "official_dir", m.officialPluginsDir, "dir", m.pluginsDir, "error", err)
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
	_ = m.CloseContext(context.Background())
}

// CloseContext shuts down all running external plugin subprocesses and builtin
// lifecycle drivers, blocking until shutdown finishes or ctx expires.
func (m *Manager) CloseContext(ctx context.Context) error {
	m.mu.RLock()
	builtins := make([]BuiltinPlugin, 0, len(m.builtins))
	for _, b := range m.builtins {
		builtins = append(builtins, b)
	}
	m.mu.RUnlock()
	for _, b := range builtins {
		if lc, ok := b.(BuiltinLifecycle); ok {
			_ = lc.Close(ctx)
		}
	}
	return m.supervisor.CloseContext(ctx)
}

// SetOnSkillsChanged registers a non-blocking callback invoked whenever plugin
// enabled state or installed plugin skills change.
func (m *Manager) SetOnSkillsChanged(fn func()) {
	m.mu.Lock()
	m.onSkillsChanged = fn
	m.mu.Unlock()
}

// SetOnChange registers a callback invoked whenever plugins are installed,
// updated, uninstalled, enabled, disabled, or reloaded.
func (m *Manager) SetOnChange(fn func(ExtensionChangeEvent)) {
	m.mu.Lock()
	m.onChange = fn
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

func (m *Manager) notifyChanged(ev ExtensionChangeEvent) {
	if ev.Timestamp == "" {
		ev.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	m.mu.RLock()
	fn := m.onChange
	m.mu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}

// RegisterBuiltin registers an in-process official plugin with the Manager.
// If OfficialPluginsDir already discovered a matching official ESM frontend package for
// the same plugin ID, it is preserved and bound as the builtin's hybrid ESM frontend.
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
	if off, ok := m.officials[manifest.ID]; ok {
		if off.source == SourceOfficial && IsFrontendESM(off.manifest) {
			m.builtinFrontends[manifest.ID] = off
		}
		delete(m.officials, manifest.ID)
	}
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

func (m *Manager) lookupBuiltinFrontend(id string) (*externalPlugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if bf, ok := m.builtinFrontends[id]; ok {
		cp := *bf
		return &cp, true
	}
	return nil, false
}

func (m *Manager) lookupBuiltinWithFrontend(id string) (BuiltinPlugin, *externalPlugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.builtins[id]
	if !ok {
		return nil, nil, false
	}
	if bf, hasBF := m.builtinFrontends[id]; hasBF {
		cp := *bf
		return b, &cp, true
	}
	return b, nil, true
}

func (m *Manager) isOfficial(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.officials[id]
	return ok
}

func (m *Manager) lookupDiskPlugin(id string) (*externalPlugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if off, ok := m.officials[id]; ok {
		cp := *off
		return &cp, true
	}
	if ext, ok := m.externals[id]; ok {
		cp := *ext
		return &cp, true
	}
	return nil, false
}

func (m *Manager) snapshotDiskPlugins() []*externalPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*externalPlugin, 0, len(m.officials)+len(m.externals))
	for _, o := range m.officials {
		cp := *o
		out = append(out, &cp)
	}
	for _, e := range m.externals {
		cp := *e
		out = append(out, &cp)
	}
	return out
}

func sourceSortRank(src string) int {
	switch src {
	case SourceBuiltin:
		return 0
	case SourceOfficial:
		return 1
	default:
		return 2
	}
}

// ListPlugins returns sorted PluginViews for all builtin, official, and external plugins.
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
	builtinFrontends := make(map[string]*externalPlugin, len(m.builtinFrontends))
	for id, b := range m.builtins {
		builtins = append(builtins, b)
		if bf, ok := m.builtinFrontends[id]; ok {
			cp := *bf
			builtinFrontends[id] = &cp
		}
	}
	diskPlugins := make([]*externalPlugin, 0, len(m.officials)+len(m.externals))
	for _, o := range m.officials {
		cp := *o
		diskPlugins = append(diskPlugins, &cp)
	}
	for _, e := range m.externals {
		cp := *e
		diskPlugins = append(diskPlugins, &cp)
	}
	m.mu.RUnlock()

	views := make([]PluginView, 0, len(builtins)+len(diskPlugins))
	for _, b := range builtins {
		bm := b.Manifest()
		bm.Normalize()
		views = append(views, m.buildBuiltinView(b, builtinFrontends[bm.ID], enabledMap))
	}
	for _, p := range diskPlugins {
		views = append(views, m.buildDiskPluginView(p, enabledMap, permMap))
	}

	sort.Slice(views, func(i, j int) bool {
		ri, rj := sourceSortRank(views[i].Source), sourceSortRank(views[j].Source)
		if ri != rj {
			return ri < rj
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

	if b, bf, isB := m.lookupBuiltinWithFrontend(id); isB {
		return m.buildBuiltinView(b, bf, enabledMap), nil
	}
	if plug, ok := m.lookupDiskPlugin(id); ok {
		return m.buildDiskPluginView(plug, enabledMap, permMap), nil
	}
	return PluginView{}, fmt.Errorf("%w: plugin %q", ErrNotFound, id)
}

// SetEnabled enables or disables a plugin, enforcing permission grants and trust policies
// for official/external plugins and starting/stopping process backends accordingly.
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool, grantPermissions []string) (PluginView, []string, error) {
	_, bf, isB := m.lookupBuiltinWithFrontend(id)

	plug, isDisk := m.lookupDiskPlugin(id)

	if !isB && !isDisk {
		return PluginView{}, nil, fmt.Errorf("%w: plugin %q", ErrNotFound, id)
	}

	if isB {
		if err := m.storage.SetEnabled(ctx, id, enabled); err != nil {
			return PluginView{}, nil, err
		}
		if b, _, ok := m.lookupBuiltinWithFrontend(id); ok {
			if lc, ok := b.(BuiltinLifecycle); ok {
				lc.OnEnabledChanged(ctx, enabled)
			}
		}
		m.notifySkillsChanged()
		action := "disabled"
		if enabled {
			action = "enabled"
		}
		enabledCopy := enabled
		digest := ""
		if bf != nil {
			digest = bf.contentDigest
		}
		m.notifyChanged(ExtensionChangeEvent{
			Action:        action,
			PluginID:      id,
			Source:        SourceBuiltin,
			Enabled:       &enabledCopy,
			ContentDigest: digest,
		})
		view, err := m.GetPlugin(ctx, id)
		return view, nil, err
	}

	if !enabled {
		if err := m.storage.SetEnabled(ctx, id, false); err != nil {
			return PluginView{}, nil, err
		}
		m.supervisor.Stop(id)
		m.notifySkillsChanged()
		enabledCopy := false
		m.notifyChanged(ExtensionChangeEvent{
			Action:        "disabled",
			PluginID:      id,
			Source:        plug.source,
			Enabled:       &enabledCopy,
			ContentDigest: plug.contentDigest,
		})
		view, err := m.GetPlugin(ctx, id)
		return view, nil, err
	}

	// Disk plugin (official or external) enable path: enforce trust/security policies
	// BEFORE persisting any granted permissions so a 403 policy rejection has zero side effects.
	if plug.manifest.Backend.Type == BackendTypeProcess && plug.source == SourceExternal {
		if policyErr := m.checkExternalProcessPolicy(ctx, plug.manifest, plug.dir); policyErr != nil {
			view, _ := m.GetPlugin(ctx, id)
			return view, nil, policyErr
		}
	}
	if IsFrontendESM(plug.manifest) {
		if policyErr := m.checkFrontendESMPolicy(ctx, plug.source, plug.manifest, plug.dir); policyErr != nil {
			view, _ := m.GetPlugin(ctx, id)
			return view, nil, policyErr
		}
	}

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

	sigVerified := verifyPluginSignature(plug.manifest, plug.dir, m.trustedPublicKeys)
	reqPerms := effectiveRequiredPermissions(plug.source, plug.manifest, sigVerified)
	granted, pending := computePermissionState(reqPerms, currentGranted)
	if len(pending) > 0 {
		view, _ := m.GetPlugin(ctx, id)
		return view, pending, ErrPermissionGrantRequired
	}

	if err := m.storage.SetEnabled(ctx, id, true); err != nil {
		return PluginView{}, nil, err
	}

	if plug.manifest.Backend.Type == BackendTypeProcess {
		if startErr := m.supervisor.Start(ctx, plug.manifest, plug.dir, granted); startErr != nil {
			m.logger.Warn("plugin process failed to start on enable", "plugin_id", id, "error", startErr)
		}
	}

	m.notifySkillsChanged()
	enabledCopy := true
	m.notifyChanged(ExtensionChangeEvent{
		Action:        "enabled",
		PluginID:      id,
		Source:        plug.source,
		Enabled:       &enabledCopy,
		ContentDigest: plug.contentDigest,
	})
	view, err := m.GetPlugin(ctx, id)
	return view, nil, err
}

// Restart restarts an enabled official or external process plugin.
func (m *Manager) Restart(ctx context.Context, id string) (PluginView, error) {
	m.mu.RLock()
	_, isB := m.builtins[id]
	m.mu.RUnlock()

	if isB {
		return PluginView{}, fmt.Errorf("%w: builtin plugin %q does not have a restartable process", ErrInvalid, id)
	}
	plug, ok := m.lookupDiskPlugin(id)
	if !ok {
		return PluginView{}, fmt.Errorf("%w: plugin %q", ErrNotFound, id)
	}
	if plug.manifest.Backend.Type != BackendTypeProcess {
		return PluginView{}, fmt.Errorf("%w: plugin %q backend type %q cannot be restarted", ErrInvalid, id, plug.manifest.Backend.Type)
	}
	if plug.source == SourceExternal {
		if policyErr := m.checkExternalProcessPolicy(ctx, plug.manifest, plug.dir); policyErr != nil {
			return PluginView{}, policyErr
		}
	}

	enabled, err := m.storage.IsEnabled(ctx, id, EffectiveDefaultEnabled(plug.source, plug.manifest))
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
	effectiveGranted, pending := computePermissionState(plug.manifest.Permissions, granted)
	if len(pending) > 0 {
		return PluginView{}, ErrPermissionGrantRequired
	}

	m.supervisor.Stop(id)
	_ = m.supervisor.Start(ctx, plug.manifest, plug.dir, effectiveGranted)
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
	m.mu.RUnlock()
	externals := m.snapshotDiskPlugins()

	out := []AgentCapabilityInfo{}

	for _, b := range builtins {
		manifest := b.Manifest()
		manifest.Normalize()
		if !isPluginEnabledInMap(enabledMap, manifest.ID, EffectiveDefaultEnabled(SourceBuiltin, manifest)) {
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
		if !isPluginEnabledInMap(enabledMap, manifest.ID, EffectiveDefaultEnabled(ext.source, manifest)) {
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
	m.mu.RUnlock()

	extCopy, isE := m.lookupDiskPlugin(pluginID)

	if !isB && !isE {
		return nil, fmt.Errorf("%w: plugin %q", ErrNotFound, pluginID)
	}

	if isB {
		bm := builtin.Manifest()
		bm.Normalize()
		enabled, err := m.storage.IsEnabled(ctx, pluginID, EffectiveDefaultEnabled(SourceBuiltin, bm))
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, fmt.Errorf("%w: plugin %q", ErrPluginDisabled, pluginID)
		}
		return builtin.InvokeCapability(ctx, capID, payload)
	}

	// Official / External disk plugin invocation.
	enabled, err := m.storage.IsEnabled(ctx, pluginID, EffectiveDefaultEnabled(extCopy.source, extCopy.manifest))
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
	m.mu.RUnlock()
	externals := m.snapshotDiskPlugins()

	results := []SignalItem{}

	for _, b := range builtins {
		manifest := b.Manifest()
		manifest.Normalize()
		if !isPluginEnabledInMap(enabledMap, manifest.ID, EffectiveDefaultEnabled(SourceBuiltin, manifest)) {
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
		if !isPluginEnabledInMap(enabledMap, manifest.ID, EffectiveDefaultEnabled(ext.source, manifest)) {
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
	m.mu.RUnlock()
	externals := m.snapshotDiskPlugins()

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
		if !isPluginEnabledInMap(enabledMap, manifest.ID, EffectiveDefaultEnabled(SourceBuiltin, manifest)) {
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
		if !isPluginEnabledInMap(enabledMap, ext.manifest.ID, EffectiveDefaultEnabled(ext.source, ext.manifest)) {
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
		if parsed, err := url.Parse(ext.manifest.Backend.URL); err == nil && !isLoopbackHost(parsed.Hostname()) {
			granted, _ := m.storage.GetGrantedPermissions(ctx, ext.manifest.ID)
			if !hasEffectiveNetworkOutbound(ext.manifest, granted) {
				m.logger.Warn("blocked non-loopback http plugin backend without network:outbound permission", "plugin_id", ext.manifest.ID, "host", parsed.Hostname())
				return "", fmt.Errorf("%w: plugin %q requires %s permission for non-loopback backend", ErrPermissionDenied, ext.manifest.ID, PermNetworkOutbound)
			}
		}
		return ext.manifest.Backend.URL, nil
	case BackendTypeProcess:
		if ext.source == SourceExternal {
			if policyErr := m.checkExternalProcessPolicy(ctx, ext.manifest, ext.dir); policyErr != nil {
				m.supervisor.Stop(ext.manifest.ID)
				return "", policyErr
			}
		}
		st := m.supervisor.Status(ext.manifest.ID)
		if st.State == RuntimeStateReady && st.BaseURL != "" {
			return st.BaseURL, nil
		}
		granted, err := m.storage.GetGrantedPermissions(ctx, ext.manifest.ID)
		if err != nil {
			return "", err
		}
		if err := m.supervisor.Start(ctx, ext.manifest, ext.dir, granted); err != nil {
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
	if _, ok := m.officials[id]; ok {
		return true
	}
	_, ok := m.externals[id]
	return ok
}

func cloneFrontendSpec(spec *FrontendSpec) *FrontendSpec {
	if spec == nil {
		return nil
	}
	cp := *spec
	if len(spec.Styles) > 0 {
		cp.Styles = append([]string(nil), spec.Styles...)
	}
	return &cp
}

func mergeHybridContributes(backend, frontend ContributesSpec) ContributesSpec {
	merged := ContributesSpec{
		UISlots:           append([]UISlotContribution(nil), backend.UISlots...),
		Pages:             append([]PageContribution(nil), backend.Pages...),
		Viewers:           append([]ViewerContribution(nil), backend.Viewers...),
		SidecarPanels:     append([]SidecarPanelContribution(nil), backend.SidecarPanels...),
		AgentCapabilities: append([]AgentCapabilityContribution(nil), backend.AgentCapabilities...),
		AgentSkills:       append([]string(nil), backend.AgentSkills...),
		SignalProviders:   append([]SignalProviderContribution(nil), backend.SignalProviders...),
	}

	if len(frontend.Viewers) > 0 {
		indexByID := make(map[string]int, len(merged.Viewers))
		for i, v := range merged.Viewers {
			indexByID[v.ID] = i
		}
		for _, fv := range frontend.Viewers {
			cp := fv
			if len(fv.Extensions) > 0 {
				cp.Extensions = append([]string(nil), fv.Extensions...)
			}
			if len(fv.MimeTypes) > 0 {
				cp.MimeTypes = append([]string(nil), fv.MimeTypes...)
			}
			if idx, exists := indexByID[cp.ID]; exists {
				merged.Viewers[idx] = cp
			} else {
				indexByID[cp.ID] = len(merged.Viewers)
				merged.Viewers = append(merged.Viewers, cp)
			}
		}
	}

	if len(frontend.Pages) > 0 {
		indexByID := make(map[string]int, len(merged.Pages))
		for i, p := range merged.Pages {
			indexByID[p.ID] = i
		}
		for _, fp := range frontend.Pages {
			if idx, exists := indexByID[fp.ID]; exists {
				merged.Pages[idx] = fp
			} else {
				indexByID[fp.ID] = len(merged.Pages)
				merged.Pages = append(merged.Pages, fp)
			}
		}
	}

	if len(frontend.UISlots) > 0 {
		seen := make(map[string]struct{}, len(merged.UISlots))
		for _, s := range merged.UISlots {
			seen[s.Slot+"\x00"+s.ID] = struct{}{}
		}
		for _, fs := range frontend.UISlots {
			key := fs.Slot + "\x00" + fs.ID
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				merged.UISlots = append(merged.UISlots, fs)
			}
		}
	}

	if len(frontend.SidecarPanels) > 0 {
		seen := make(map[string]struct{}, len(merged.SidecarPanels))
		for _, p := range merged.SidecarPanels {
			seen[p.ID] = struct{}{}
		}
		for _, fp := range frontend.SidecarPanels {
			if _, exists := seen[fp.ID]; !exists {
				seen[fp.ID] = struct{}{}
				merged.SidecarPanels = append(merged.SidecarPanels, fp)
			}
		}
	}

	return merged
}

func (m *Manager) buildBuiltinView(b BuiltinPlugin, frontend *externalPlugin, enabledMap map[string]bool) PluginView {
	manifest := b.Manifest()
	manifest.Normalize()
	perms := nonNilStrings(manifest.Permissions)
	defaultEnabled := EffectiveDefaultEnabled(SourceBuiltin, manifest)

	description := manifest.Description
	author := manifest.Author
	homepage := manifest.Homepage
	icon := manifest.Icon
	frontendSpec := cloneFrontendSpec(manifest.Frontend)
	settings := manifest.Settings
	contributes := manifest.Contributes
	contentDigest := ""
	esmBaseURL := ""

	if frontend != nil && frontend.source == SourceOfficial {
		fm := frontend.manifest
		if fm.Frontend != nil {
			frontendSpec = cloneFrontendSpec(fm.Frontend)
		}
		contentDigest = frontend.contentDigest
		if IsFrontendESM(fm) {
			esmBaseURL = BaseExtPath + "/" + url.PathEscape(manifest.ID) + "/_esm"
		}
		contributes = mergeHybridContributes(manifest.Contributes, fm.Contributes)
		if settings.Mode == SettingsModeNone && fm.Settings.Mode != SettingsModeNone {
			settings = fm.Settings
		}
		if description == "" {
			description = fm.Description
		}
		if author == "" {
			author = fm.Author
		}
		if homepage == "" {
			homepage = fm.Homepage
		}
		if icon == "" {
			icon = fm.Icon
		}
	}

	return PluginView{
		ID:                 manifest.ID,
		Name:               manifest.Name,
		Version:            manifest.Version,
		Category:           manifest.Category,
		Description:        description,
		Author:             author,
		Homepage:           homepage,
		Icon:               icon,
		Source:             SourceBuiltin,
		Enabled:            isPluginEnabledInMap(enabledMap, manifest.ID, defaultEnabled),
		DefaultEnabled:     defaultEnabled,
		DependsOn:          append([]string(nil), manifest.DependsOn...),
		Permissions:        perms,
		GrantedPermissions: append([]string(nil), perms...),
		PendingPermissions: []string{},
		PermissionsGranted: true,
		RuntimeState:       RuntimeStateBuiltin,
		BackendType:        BackendTypeBuiltin,
		Frontend:           frontendSpec,
		ContentDigest:      contentDigest,
		SignatureVerified:  true,
		Settings:           settings,
		Contributes:        contributes,
		ESMBaseURL:         esmBaseURL,
		APIBaseURL:         BaseExtPath + "/" + url.PathEscape(manifest.ID),
	}
}

func (m *Manager) buildDiskPluginView(ext *externalPlugin, enabledMap map[string]bool, permMap map[string][]string) PluginView {
	manifest := ext.manifest
	source := ext.source
	if source == "" {
		source = SourceExternal
	}
	defaultEnabled := EffectiveDefaultEnabled(source, manifest)
	enabled := isPluginEnabledInMap(enabledMap, manifest.ID, defaultEnabled)
	reqPerms := effectiveRequiredPermissions(source, manifest, ext.signatureVerified)
	granted, pending := computePermissionState(reqPerms, permMap[manifest.ID])

	runtimeState := RuntimeStateStopped
	runtimeErr := ""
	switch manifest.Backend.Type {
	case BackendTypeProcess:
		st := m.supervisor.Status(manifest.ID)
		runtimeState = st.State
		runtimeErr = st.Error
	case BackendTypeHTTP, BackendTypeNone, BackendTypeBuiltin:
		if enabled && len(pending) == 0 {
			runtimeState = RuntimeStateReady
		} else {
			runtimeState = RuntimeStateStopped
		}
	}

	esmBaseURL := ""
	if IsFrontendESM(manifest) {
		esmBaseURL = BaseExtPath + "/" + url.PathEscape(manifest.ID) + "/_esm"
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
		Source:             source,
		Enabled:            enabled,
		DefaultEnabled:     defaultEnabled,
		DependsOn:          append([]string(nil), manifest.DependsOn...),
		Permissions:        reqPerms,
		GrantedPermissions: granted,
		PendingPermissions: pending,
		PermissionsGranted: len(pending) == 0,
		RuntimeState:       runtimeState,
		RuntimeError:       runtimeErr,
		BackendType:        manifest.Backend.Type,
		Frontend:           manifest.Frontend,
		ContentDigest:      ext.contentDigest,
		SignatureVerified:  ext.signatureVerified,
		Settings:           manifest.Settings,
		Contributes:        manifest.Contributes,
		UIBaseURL:          BaseExtPath + "/" + url.PathEscape(manifest.ID) + "/_ui/",
		ESMBaseURL:         esmBaseURL,
		APIBaseURL:         BaseExtPath + "/" + url.PathEscape(manifest.ID),
	}
}

func isPluginEnabledInMap(enabledMap map[string]bool, id string, defaultEnabled bool) bool {
	if flag, exists := enabledMap[id]; exists {
		return flag
	}
	return defaultEnabled
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
