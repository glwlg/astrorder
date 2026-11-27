package configuration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"astrorder.dev/session-daemon/internal/modelconfig"
	"astrorder.dev/session-daemon/internal/process"
	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/runtime/antigravity"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"astrorder.dev/session-daemon/internal/runtime/grok"
	"astrorder.dev/session-daemon/internal/runtime/hermes"
	"astrorder.dev/session-daemon/internal/runtime/pty"
	"astrorder.dev/session-daemon/internal/runtime/remote"
)

// ManagerOptions configures a runtime configuration Manager.
type ManagerOptions struct {
	ConfigPath   string
	StateDir     string
	RefreshPath  func(extraDirs ...string) string
	LookPath     func(file string) (string, error)
	StatFile     func(path string) (os.FileInfo, error)
	UserHomeDir  func() (string, error)
	ExtraBinDirs []string
}

type observerHandle struct {
	agentID string
	cancel  context.CancelFunc
}

// Manager owns thread-safe loading, PATH refreshing, runtime probing, and
// atomic non-disruptive hot-reloading of local Agent runtimes on a Daemon.
type Manager struct {
	daemon       *protocol.Daemon
	configPath   string
	stateDir     string
	refreshPath  func(extraDirs ...string) string
	lookPath     func(file string) (string, error)
	statFile     func(path string) (os.FileInfo, error)
	userHomeDir  func() (string, error)
	extraBinDirs []string

	mu             sync.Mutex
	active         map[string]Runtime
	fromConfig     map[string]bool
	observers      map[string]observerHandle
	lastJournal    *JournalConfig
	lastFileHash   string
	lastFailedHash string
	closerAdded    bool
}

// NewManager creates a Manager bound to daemon.
func NewManager(daemon *protocol.Daemon, opts ManagerOptions) *Manager {
	stateDir := strings.TrimSpace(opts.StateDir)
	if stateDir == "" && strings.TrimSpace(opts.ConfigPath) != "" {
		stateDir = filepath.Dir(opts.ConfigPath)
	}
	m := &Manager{
		daemon:       daemon,
		configPath:   strings.TrimSpace(opts.ConfigPath),
		stateDir:     stateDir,
		refreshPath:  opts.RefreshPath,
		lookPath:     opts.LookPath,
		statFile:     opts.StatFile,
		userHomeDir:  opts.UserHomeDir,
		extraBinDirs: append([]string(nil), opts.ExtraBinDirs...),
		active:       map[string]Runtime{},
		fromConfig:   map[string]bool{},
		observers:    map[string]observerHandle{},
	}
	if m.refreshPath == nil {
		m.refreshPath = process.RefreshProcessPath
	}
	if m.lookPath == nil {
		m.lookPath = exec.LookPath
	}
	if m.statFile == nil {
		m.statFile = os.Stat
	}
	if m.userHomeDir == nil {
		m.userHomeDir = os.UserHomeDir
	}
	if daemon != nil {
		m.closerAdded = true
		daemon.AddCloser(m.stopAllObservers)
	}
	return m
}

func (m *Manager) stopAllObservers() error {
	m.mu.Lock()
	handles := make([]observerHandle, 0, len(m.observers))
	for _, h := range m.observers {
		handles = append(handles, h)
	}
	m.observers = map[string]observerHandle{}
	m.mu.Unlock()
	for _, h := range handles {
		if h.cancel != nil {
			h.cancel()
		}
	}
	return nil
}

// ReloadInitial loads and applies the configured file strictly (without PATH
// auto-probing) for daemon startup parity.
func (m *Manager) ReloadInitial() (protocol.RuntimeReloadResult, error) {
	probe := false
	return m.Reload(context.Background(), protocol.RuntimeReloadOptions{ProbePath: &probe})
}

// Reload re-reads ConfigPath (when configured), refreshes process PATH with
// managed runtime directories and fresh OS PATH entries, optionally probes
// PATH for newly installed local Agents not yet listed in config, and
// atomically reconciles the daemon's runtime registry without interrupting
// any running sessions.
func (m *Manager) Reload(ctx context.Context, opts protocol.RuntimeReloadOptions) (protocol.RuntimeReloadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return protocol.RuntimeReloadResult{}, err
	}
	if m.daemon == nil {
		return protocol.RuntimeReloadResult{}, fmt.Errorf("daemon is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return protocol.RuntimeReloadResult{}, err
	}

	// Step 1: Refresh PATH so newly installed portable runtimes (node/uv) and
	// Agents on User/Machine PATH are immediately discoverable and inherited by
	// newly launched Agent subprocesses.
	candidateDirs := m.collectCandidatePathDirs(opts.ExtraPathDirs)
	if m.refreshPath != nil {
		m.refreshPath(candidateDirs...)
	}

	// Step 2: Load configuration file if configured.
	var cfg Config
	var fileHash string
	if m.configPath != "" {
		raw, err := os.ReadFile(m.configPath)
		if err != nil {
			return protocol.RuntimeReloadResult{}, fmt.Errorf("read daemon configuration %q: %w", m.configPath, err)
		}
		parsed, err := Parse(bytes.NewReader(raw))
		if err != nil {
			return protocol.RuntimeReloadResult{}, fmt.Errorf("parse daemon configuration %q: %w", m.configPath, err)
		}
		cfg = parsed
		sum := sha256.Sum256(raw)
		fileHash = hex.EncodeToString(sum[:])
	}

	explicitTypes := make(map[string]bool, len(cfg.Runtimes))
	for _, r := range cfg.Runtimes {
		explicitTypes[r.Type] = true
	}

	// Step 3: Optionally probe PATH / managed runtimes for local Agents not
	// explicitly listed in the config file. Explicit config entries always win.
	probePath := true
	if opts.ProbePath != nil {
		probePath = *opts.ProbePath
	}
	var warnings []string
	if probePath {
		discovered, probeWarnings := m.probeMissingRuntimes(cfg)
		warnings = append(warnings, probeWarnings...)
		cfg.Runtimes = append(cfg.Runtimes, discovered...)
	}

	result, err := m.applyLocked(ctx, cfg, explicitTypes, probePath)
	if err != nil {
		return protocol.RuntimeReloadResult{}, err
	}
	if fileHash != "" {
		m.lastFileHash = fileHash
		m.lastFailedHash = ""
	}
	result.ConfigPath = m.configPath
	result.Warnings = append(result.Warnings, warnings...)
	return result, nil
}

// Apply validates and atomically applies an explicit Config without reading
// from disk or probing PATH.
func (m *Manager) Apply(ctx context.Context, cfg Config) (protocol.RuntimeReloadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return protocol.RuntimeReloadResult{}, err
	}
	if m.daemon == nil {
		return protocol.RuntimeReloadResult{}, fmt.Errorf("daemon is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	explicitTypes := make(map[string]bool, len(cfg.Runtimes))
	for _, r := range cfg.Runtimes {
		explicitTypes[r.Type] = true
	}
	return m.applyLocked(ctx, cfg, explicitTypes, false)
}

func (m *Manager) applyLocked(ctx context.Context, cfg Config, explicitTypes map[string]bool, probed bool) (protocol.RuntimeReloadResult, error) {
	validated, err := validateConfig(cfg)
	if err != nil {
		return protocol.RuntimeReloadResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return protocol.RuntimeReloadResult{}, err
	}

	// Apply journal retention before mutating runtimes so invalid retention fails fast.
	if validated.journal != nil && (m.lastJournal == nil || *m.lastJournal != *validated.journal) {
		if err := m.daemon.ConfigureRetention(validated.journal.TotalBytes, validated.journal.SessionBytes, validated.journal.MaxAgeSeconds); err != nil {
			return protocol.RuntimeReloadResult{}, err
		}
		jCopy := *validated.journal
		m.lastJournal = &jCopy
	}

	codexBatchEmit := m.codexBatchEmitter()

	// Build candidate adapters only for runtimes that are new or changed.
	type plannedChange struct {
		kind     string
		spec     Runtime
		adapter  core.Adapter
		isUpdate bool
	}
	desired := make(map[string]Runtime, len(validated.runtimes))
	changes := make([]plannedChange, 0, len(validated.runtimes))
	var unchanged []string

	for _, r := range validated.runtimes {
		desired[r.Type] = r
		prev, existed := m.active[r.Type]
		if existed && runtimeEqual(prev, r) && m.daemon.HasRuntime(r.Type) {
			unchanged = append(unchanged, r.Type)
			continue
		}
		adapter, err := m.buildAdapter(r, codexBatchEmit)
		if err != nil {
			// Clean up any adapters already built in this batch before returning.
			for _, planned := range changes {
				if closer, ok := planned.adapter.(interface{ Close() error }); ok {
					_ = closer.Close()
				}
			}
			return protocol.RuntimeReloadResult{}, err
		}
		changes = append(changes, plannedChange{
			kind:     r.Type,
			spec:     r,
			adapter:  adapter,
			isUpdate: existed && m.daemon.HasRuntime(r.Type),
		})
	}

	// Ensure multiplexed SSH child registries exist without replacing active ones.
	m.ensureSSHRegistries(codexBatchEmit, validated.helperAssets)

	// Configure model_config if provided and not yet configured.
	var warnings []string
	if validated.modelHome != "" {
		if !m.daemon.HasModelConfig() {
			if err := m.daemon.ConfigureModelConfig(modelconfig.Config{
				AllowedRoots:    []string{validated.modelHome},
				Credentials:     modelconfig.NativeCredentialStore(),
				HelperAssetsDir: validated.helperAssets,
			}); err != nil {
				for _, planned := range changes {
					if closer, ok := planned.adapter.(interface{ Close() error }); ok {
						_ = closer.Close()
					}
				}
				return protocol.RuntimeReloadResult{}, err
			}
		}
	}

	var added, updated, removed []string

	// Atomically swap new/updated local runtimes into the registry.
	for i, planned := range changes {
		if _, err := m.daemon.SwapRuntime(planned.kind, planned.adapter, nil); err != nil {
			for j := i; j < len(changes); j++ {
				if closer, ok := changes[j].adapter.(interface{ Close() error }); ok {
					_ = closer.Close()
				}
			}
			return protocol.RuntimeReloadResult{}, err
		}
		m.reconcileObserverLocked(planned.spec, planned.adapter)
		m.active[planned.kind] = planned.spec
		m.fromConfig[planned.kind] = explicitTypes[planned.kind]
		if planned.isUpdate {
			updated = append(updated, planned.kind)
		} else {
			added = append(added, planned.kind)
		}
	}

	// Update provenance for unchanged runtimes (e.g., a PATH-discovered runtime
	// later written explicitly into session-daemon.json).
	for _, kind := range unchanged {
		if explicitTypes[kind] {
			m.fromConfig[kind] = true
		}
	}

	// Unregister local runtimes that were removed from config (or removed from
	// PATH when probing), without disrupting any live sessions. When probed is
	// false (e.g. file watcher), preserve previously PATH-discovered runtimes
	// that were never listed in session-daemon.json.
	for kind := range m.active {
		if _, keep := desired[kind]; keep {
			continue
		}
		if !probed && !m.fromConfig[kind] {
			unchanged = append(unchanged, kind)
			continue
		}
		preserved, didRemove, err := m.daemon.UnregisterRuntime(kind)
		if err != nil {
			return protocol.RuntimeReloadResult{}, err
		}
		if didRemove {
			removed = append(removed, kind)
		}
		if preserved == 0 || kind == "antigravity" {
			m.stopObserverLocked(kind)
		}
		delete(m.active, kind)
		delete(m.fromConfig, kind)
	}

	sort.Strings(added)
	sort.Strings(updated)
	sort.Strings(removed)
	sort.Strings(unchanged)

	return protocol.RuntimeReloadResult{
		Supported:         true,
		DaemonID:          m.daemon.DaemonID(),
		ConfigPath:        m.configPath,
		ReloadedAt:        time.Now().UTC().Format(time.RFC3339),
		AddedRuntimes:     added,
		UpdatedRuntimes:   updated,
		RemovedRuntimes:   removed,
		UnchangedRuntimes: unchanged,
		Registered:        m.daemon.RegisteredRuntimes(),
		PreservedSessions: m.daemon.TotalOwnedSessions(),
		Warnings:          warnings,
	}, nil
}

// WatchConfigFile periodically checks ConfigPath for content changes and
// applies safe hot reloads without interrupting active sessions. Invalid or
// partially written files are ignored without disrupting existing runtimes.
func (m *Manager) WatchConfigFile(ctx context.Context, interval time.Duration) {
	if m.configPath == "" {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkConfigFileOnce(ctx)
		}
	}
}

func (m *Manager) checkConfigFileOnce(ctx context.Context) {
	raw, err := os.ReadFile(m.configPath)
	if err != nil {
		return
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])

	m.mu.Lock()
	unchanged := hash == m.lastFileHash || hash == m.lastFailedHash
	m.mu.Unlock()
	if unchanged {
		return
	}

	probe := false
	if _, err := m.Reload(ctx, protocol.RuntimeReloadOptions{ProbePath: &probe}); err != nil {
		m.mu.Lock()
		m.lastFailedHash = hash
		m.mu.Unlock()
		log.Printf("hot-reload of %s skipped (existing runtimes preserved): %v", m.configPath, err)
	}
}

func (m *Manager) codexBatchEmitter() codex.BatchEmitter {
	return func(entries []codex.EmitEntry) error {
		items := make([]protocol.EventInput, len(entries))
		for i, entry := range entries {
			items[i] = protocol.EventInput{
				SessionID: entry.SessionID,
				Event:     entry.Event,
				Status:    entry.Status,
				Payload:   entry.Payload,
			}
		}
		return m.daemon.EmitBatch(items)
	}
}

func (m *Manager) ensureSSHRegistries(codexBatchEmit codex.BatchEmitter, helperAssets string) {
	if !m.daemon.HasRuntime("codex-ssh") {
		codexSSH := core.NewMultiplexRegistry(remote.NewRemoteCodexFactoryWithBatch(m.daemon.Emit, codexBatchEmit, m.daemon.HasSession))
		m.daemon.RegisterRuntime("codex-ssh", codexSSH)
	}
	if !m.daemon.HasRuntime("grok-ssh") {
		grokSSH := core.NewMultiplexRegistry(remote.NewRemoteGrokFactory(m.daemon.Emit))
		m.daemon.RegisterRuntime("grok-ssh", grokSSH)
	}
	if !m.daemon.HasRuntime("antigravity-ssh") {
		antigravitySSH := core.NewMultiplexRegistry(remote.NewRemoteAntigravityFactory(m.daemon.Emit, helperAssets))
		m.daemon.RegisterRuntime("antigravity-ssh", antigravitySSH)
	}
	if !m.daemon.HasRuntime("ssh") {
		sshHermes := core.NewMultiplexRegistry(remote.NewRemoteHermesFactory(m.daemon.Emit))
		m.daemon.RegisterRuntime("ssh", sshHermes)
	}
}

func (m *Manager) buildAdapter(r Runtime, codexBatchEmit codex.BatchEmitter) (core.Adapter, error) {
	switch r.Type {
	case "codex":
		cfg := codex.Config{Executable: r.Executable, Arguments: r.Arguments, Workspace: r.Workspace, Allowed: r.Allowed, AgentID: r.AgentID}
		codexAdapter := codex.NewAdapter(cfg, m.daemon.Emit)
		codexAdapter.SetBatchEmitter(codexBatchEmit)
		return codexAdapter, nil
	case "antigravity":
		return antigravity.New(antigravity.Config{Executable: r.Executable, Arguments: r.Arguments, Workspace: r.Workspace, Allowed: r.Allowed, AgentID: r.AgentID, Emit: m.daemon.Emit}), nil
	case "grok":
		return grok.New(grok.Config{Executable: r.Executable, Arguments: r.Arguments, Workspace: r.Workspace, Allowed: r.Allowed, AgentID: r.AgentID, Emit: m.daemon.Emit}), nil
	case "hermes":
		return hermes.NewIsolated(hermes.Config{Executable: r.Executable, Arguments: r.Arguments, Workspace: r.Workspace, Allowed: r.Allowed, AgentID: r.AgentID, Emit: m.daemon.Emit}), nil
	case "pty":
		return pty.New(pty.Config{Allowed: r.Allowed, Shell: r.Shell, Emit: m.daemon.Emit})
	default:
		return nil, fmt.Errorf("runtime is not implemented: %s", r.Type)
	}
}

func (m *Manager) reconcileObserverLocked(r Runtime, adapter core.Adapter) {
	switch r.Type {
	case "codex":
		if existing, ok := m.observers["codex"]; ok && existing.agentID == r.AgentID {
			return
		}
		m.stopObserverLocked("codex")
		obsCtx, obsCancel := context.WithCancel(context.Background())
		cfg := codex.Config{Executable: r.Executable, Arguments: r.Arguments, Workspace: r.Workspace, Allowed: r.Allowed, AgentID: r.AgentID}
		go codex.StartDesktopObserverWithOwner(obsCtx, cfg, m.daemon.Emit, m.daemon.HasSession, func(id string) bool {
			return m.daemon.OwnsRuntimeSession("codex", id)
		}, 500*time.Millisecond)
		m.observers["codex"] = observerHandle{agentID: r.AgentID, cancel: obsCancel}
	case "hermes":
		if existing, ok := m.observers["hermes"]; ok && existing.agentID == r.AgentID {
			return
		}
		m.stopObserverLocked("hermes")
		hermesCtx, hermesCancel := context.WithCancel(context.Background())
		go hermes.StartActivityObserver(hermesCtx, r.AgentID, m.daemon.Emit, m.daemon.HasSession, 500*time.Millisecond)
		m.observers["hermes"] = observerHandle{agentID: r.AgentID, cancel: hermesCancel}
	case "antigravity":
		m.stopObserverLocked("antigravity")
		if instance, ok := adapter.(*antigravity.Adapter); ok {
			observeCtx, observeCancel := context.WithCancel(context.Background())
			go instance.Observe(observeCtx)
			m.observers["antigravity"] = observerHandle{agentID: r.AgentID, cancel: observeCancel}
		}
	}
}

func (m *Manager) stopObserverLocked(kind string) {
	if existing, ok := m.observers[kind]; ok {
		if existing.cancel != nil {
			existing.cancel()
		}
		delete(m.observers, kind)
	}
}

func (m *Manager) collectCandidatePathDirs(extraDirs []string) []string {
	var dirs []string
	addIfDir := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || strings.ContainsRune(p, 0) {
			return
		}
		if info, err := m.statFile(p); err == nil && info.IsDir() {
			dirs = append(dirs, p)
		}
	}

	for _, dir := range extraDirs {
		addIfDir(dir)
	}
	for _, dir := range m.extraBinDirs {
		addIfDir(dir)
	}

	// Discover managed portable runtimes under <StateDir>/runtimes/* and <StateDir>/bin.
	if m.stateDir != "" {
		addIfDir(filepath.Join(m.stateDir, "bin"))
		runtimesRoot := filepath.Join(m.stateDir, "runtimes")
		if entries, err := os.ReadDir(runtimesRoot); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				sub := filepath.Join(runtimesRoot, entry.Name())
				addIfDir(sub)
				addIfDir(filepath.Join(sub, "bin"))
				addIfDir(filepath.Join(sub, "Scripts"))
				addIfDir(filepath.Join(sub, "cmd"))
			}
		}
	}

	// Standard user-level CLI installation directories.
	if home, err := m.userHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		addIfDir(filepath.Join(home, ".local", "bin"))
		addIfDir(filepath.Join(home, ".antigravity", "bin"))
		addIfDir(filepath.Join(home, ".cargo", "bin"))
		addIfDir(filepath.Join(home, ".npm-global", "bin"))
		if goruntime.GOOS == "windows" {
			addIfDir(filepath.Join(home, "AppData", "Roaming", "npm"))
			addIfDir(filepath.Join(home, "AppData", "Local", "agy", "bin"))
			addIfDir(filepath.Join(home, "AppData", "Local", "bin"))
		}
	}
	if goruntime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			addIfDir(filepath.Join(appData, "npm"))
		}
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			addIfDir(filepath.Join(localAppData, "agy", "bin"))
			addIfDir(filepath.Join(localAppData, "Programs", "nodejs"))
		}
	}

	return dirs
}

func (m *Manager) probeMissingRuntimes(cfg Config) ([]Runtime, []string) {
	configured := make(map[string]bool, len(cfg.Runtimes))
	for _, r := range cfg.Runtimes {
		configured[r.Type] = true
	}

	allowed, workspace := m.fallbackWorkspaceScope(cfg)
	if len(allowed) == 0 || workspace == "" {
		return nil, nil
	}

	specs := []struct {
		kind    string
		agentID string
	}{
		{kind: "codex", agentID: "local-codex"},
		{kind: "hermes", agentID: "local-hermes-default"},
		{kind: "grok", agentID: "local-grok"},
		{kind: "antigravity", agentID: "local-antigravity"},
	}

	var discovered []Runtime
	var warnings []string
	for _, spec := range specs {
		if configured[spec.kind] {
			continue
		}
		exe := m.discoverExecutable(spec.kind)
		if exe == "" {
			continue
		}
		candidate := Runtime{
			Type:       spec.kind,
			Executable: exe,
			Arguments:  []string{},
			Workspace:  workspace,
			Allowed:    append([]string(nil), allowed...),
			AgentID:    spec.agentID,
		}
		if _, err := validateRuntime(candidate); err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped auto-discovered runtime %s (%s): %v", spec.kind, exe, err))
			continue
		}
		discovered = append(discovered, candidate)
	}
	return discovered, warnings
}

func (m *Manager) fallbackWorkspaceScope(cfg Config) ([]string, string) {
	var allowed []string
	var workspace string
	for _, r := range cfg.Runtimes {
		if len(allowed) == 0 && len(r.Allowed) > 0 {
			allowed = append([]string(nil), r.Allowed...)
		}
		if workspace == "" && r.Workspace != "" {
			workspace = r.Workspace
		}
	}
	if len(allowed) == 0 {
		for _, r := range m.active {
			if len(allowed) == 0 && len(r.Allowed) > 0 {
				allowed = append([]string(nil), r.Allowed...)
			}
			if workspace == "" && r.Workspace != "" {
				workspace = r.Workspace
			}
		}
	}
	if len(allowed) == 0 && cfg.ModelConfig != nil && cfg.ModelConfig.Home != "" {
		if resolved, err := canonicalDirectory(cfg.ModelConfig.Home); err == nil {
			allowed = []string{resolved}
		}
	}
	if len(allowed) == 0 {
		if home, err := m.userHomeDir(); err == nil && home != "" {
			if resolved, err := canonicalDirectory(home); err == nil {
				allowed = []string{resolved}
			}
		}
	}
	if workspace == "" && len(allowed) > 0 {
		workspace = allowed[0]
	}
	return allowed, workspace
}

func (m *Manager) discoverExecutable(kind string) string {
	lookup := func(name string) string {
		if m.lookPath == nil {
			return ""
		}
		p, err := m.lookPath(name)
		if err != nil || strings.TrimSpace(p) == "" {
			return ""
		}
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	switch kind {
	case "codex":
		return lookup("codex")
	case "hermes":
		return lookup("hermes")
	case "grok":
		return lookup("grok")
	case "antigravity":
		if p := lookup("agy"); p != "" {
			return p
		}
		if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
			p := filepath.Join(base, "agy", "bin", "agy.exe")
			if info, err := m.statFile(p); err == nil && !info.IsDir() {
				return p
			}
		}
		if home, err := m.userHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			binName := "agy"
			if goruntime.GOOS == "windows" {
				binName = "agy.exe"
			}
			for _, candidate := range []string{
				filepath.Join(home, ".local", "bin", binName),
				filepath.Join(home, ".antigravity", "bin", binName),
			} {
				if info, err := m.statFile(candidate); err == nil && !info.IsDir() {
					return candidate
				}
			}
		}
		return ""
	default:
		return ""
	}
}

func runtimeEqual(a, b Runtime) bool {
	if a.Type != b.Type || a.Executable != b.Executable || a.Workspace != b.Workspace || a.AgentID != b.AgentID || a.Shell != b.Shell {
		return false
	}
	if len(a.Arguments) != len(b.Arguments) || len(a.Allowed) != len(b.Allowed) {
		return false
	}
	for i := range a.Arguments {
		if a.Arguments[i] != b.Arguments[i] {
			return false
		}
	}
	for i := range a.Allowed {
		if a.Allowed[i] != b.Allowed[i] {
			return false
		}
	}
	return true
}
