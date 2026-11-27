package toolchain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/lifecycle"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

var semverRE = regexp.MustCompile(`v?[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:[A-Za-z0-9._+-]*)`)

type Options struct {
	Resolver           *envtarget.Resolver
	Store              *store.Store
	DaemonClient       *daemonclient.Client
	DaemonReloader     DaemonReloader
	StateDir           string
	DataDir            string
	DefaultWorkspace   string
	OnboardingInjector OnboardingCapabilitiesInjector
	HTTPClient         *http.Client
	BrowserOpener      func(string) error
}

type Service struct {
	resolver           *envtarget.Resolver
	store              *store.Store
	daemonMu           sync.RWMutex
	daemon             *daemonclient.Client
	daemonReloader     DaemonReloader
	stateDir           string
	dataDir            string
	defaultWorkspace   string
	onboardingInjector OnboardingCapabilitiesInjector
	httpClient         *http.Client
	browserOpener      func(string) error

	jobMu         sync.Mutex
	jobs          map[string]*InstallJob
	oauthMu       sync.Mutex
	oauthSessions map[string]*oauthSession
	jobContext    context.Context
	jobCancel     context.CancelFunc
	jobWG         sync.WaitGroup
}

func NewService(opts Options) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	resolver := opts.Resolver
	if resolver == nil {
		resolver = &envtarget.Resolver{Store: opts.Store}
	}
	return &Service{
		resolver:           resolver,
		store:              opts.Store,
		daemon:             opts.DaemonClient,
		daemonReloader:     opts.DaemonReloader,
		stateDir:           opts.StateDir,
		dataDir:            opts.DataDir,
		defaultWorkspace:   opts.DefaultWorkspace,
		onboardingInjector: opts.OnboardingInjector,
		httpClient:         opts.HTTPClient,
		browserOpener:      opts.BrowserOpener,
		jobs:               map[string]*InstallJob{},
		oauthSessions:      map[string]*oauthSession{},
		jobContext:         ctx,
		jobCancel:          cancel,
	}
}

func (s *Service) Close(ctx context.Context) error {
	s.jobCancel()
	done := make(chan struct{})
	go func() {
		s.jobWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type ToolStatus struct {
	ID                    string          `json:"id"`
	Kind                  ItemKind        `json:"kind"`
	Group                 ToolGroup       `json:"group,omitempty"`
	PluginID              string          `json:"plugin_id,omitempty"`
	Name                  string          `json:"name"`
	Description           string          `json:"description"`
	Installed             bool            `json:"installed"`
	InPath                bool            `json:"in_path"`
	PathHint              string          `json:"path_hint,omitempty"`
	Readiness             string          `json:"readiness"` // "missing" | "needs_setup" | "ready"
	ReadinessDetail       string          `json:"readiness_detail,omitempty"`
	AuthConfigured        *bool           `json:"auth_configured,omitempty"`
	Version               string          `json:"version,omitempty"`
	Executable            string          `json:"executable,omitempty"`
	CustomExecutable      string          `json:"custom_executable,omitempty"`
	ConfiguredExecutable  string          `json:"configured_executable,omitempty"`
	RegisteredInDaemon    *bool           `json:"registered_in_daemon,omitempty"`
	RequiresDaemonRestart bool            `json:"requires_daemon_restart,omitempty"`
	Dependencies          []string        `json:"dependencies,omitempty"`
	MissingDependencies   []string        `json:"missing_dependencies,omitempty"`
	AuthCommand           string          `json:"auth_command,omitempty"`
	AuthHint              string          `json:"auth_hint,omitempty"`
	DocsURL               string          `json:"docs_url,omitempty"`
	Recipes               []InstallRecipe `json:"recipes"`
	Detail                string          `json:"detail,omitempty"`
}

type EnvironmentReport struct {
	EnvironmentID string       `json:"environment_id"`
	TargetKind    string       `json:"target_kind"`
	TargetName    string       `json:"target_name"`
	OS            string       `json:"os"`
	Arch          string       `json:"arch,omitempty"`
	CheckedAt     string       `json:"checked_at"`
	Items         []ToolStatus `json:"items"`
}

type ValidateResult struct {
	Valid        bool   `json:"valid"`
	ResolvedPath string `json:"resolved_path,omitempty"`
	Version      string `json:"version,omitempty"`
	Output       string `json:"output,omitempty"`
	Error        string `json:"error,omitempty"`
}

type SaveExecutableResult struct {
	Saved                 bool       `json:"saved"`
	DaemonReloaded        bool       `json:"daemon_reloaded,omitempty"`
	RequiresDaemonRestart bool       `json:"requires_daemon_restart"`
	Message               string     `json:"message"`
	Item                  ToolStatus `json:"item"`
}

func (s *Service) ListTargets(ctx context.Context) ([]envtarget.Target, error) {
	return s.resolver.List(ctx)
}

func (s *Service) Inspect(ctx context.Context, envID string) (EnvironmentReport, error) {
	env, err := s.resolver.Open(ctx, envID)
	if err != nil {
		return EnvironmentReport{}, err
	}
	target := env.Target()
	targetOS, targetArch := s.detectPlatform(ctx, env)
	daemonRegistered := s.readDaemonRegistered(ctx, target.Kind == "local")

	var sshConn map[string]any
	if target.Kind == "ssh" && s.store != nil && target.ConnectionID != "" {
		sshConn, _ = s.store.GetSSHConnection(ctx, target.ConnectionID)
	}

	envPaths, pathsErr := env.Paths(ctx)
	authFiles := map[string][]byte{}
	if pathsErr == nil {
		var wanted []string
		for _, engine := range RegisteredAgentEngines() {
			base := agentHomeForEngine(envPaths, engine.HomeField)
			if base == "" {
				continue
			}
			for _, rel := range engine.AuthFiles {
				wanted = append(wanted, path.Clean(base+"/"+rel))
			}
		}
		if len(wanted) > 0 {
			if loaded, err := env.ReadMany(ctx, wanted); err == nil {
				authFiles = loaded
			}
		}
	}

	localPath := ""
	if env.IsLocal() {
		localPath = s.refreshedLocalPath()
	}

	specs := Catalog()
	items := make([]ToolStatus, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func(idx int, sp ToolSpec) {
			defer wg.Done()
			items[idx] = s.inspectOne(ctx, env, target, targetOS, sp, sshConn, daemonRegistered, envPaths, pathsErr == nil, authFiles, localPath)
		}(i, spec)
	}
	wg.Wait()

	installedByID := make(map[string]bool, len(items))
	for _, item := range items {
		installedByID[item.ID] = item.Installed
	}
	for i := range items {
		if len(items[i].Dependencies) == 0 {
			continue
		}
		var missing []string
		for _, dep := range items[i].Dependencies {
			if !installedByID[dep] {
				missing = append(missing, dep)
			}
		}
		items[i].MissingDependencies = missing
	}

	return EnvironmentReport{
		EnvironmentID: target.ID,
		TargetKind:    target.Kind,
		TargetName:    target.Name,
		OS:            targetOS,
		Arch:          targetArch,
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
		Items:         items,
	}, nil
}

// inspectSingleTool refreshes status for one specific tool/agent without running
// --version probes across every unrelated tool in the catalog.
func (s *Service) inspectSingleTool(ctx context.Context, env envtarget.Env, spec ToolSpec) ToolStatus {
	target := env.Target()
	targetOS, _ := s.detectPlatform(ctx, env)
	daemonRegistered := s.readDaemonRegistered(ctx, target.Kind == "local")

	var sshConn map[string]any
	if target.Kind == "ssh" && s.store != nil && target.ConnectionID != "" {
		sshConn, _ = s.store.GetSSHConnection(ctx, target.ConnectionID)
	}

	envPaths, pathsErr := env.Paths(ctx)
	authFiles := map[string][]byte{}
	if pathsErr == nil && spec.Kind == ItemKindAgent {
		if engine, ok := LookupAgentEngine(spec.ID); ok {
			if base := agentHomeForEngine(envPaths, engine.HomeField); base != "" {
				var wanted []string
				for _, rel := range engine.AuthFiles {
					wanted = append(wanted, path.Clean(base+"/"+rel))
				}
				if len(wanted) > 0 {
					if loaded, err := env.ReadMany(ctx, wanted); err == nil {
						authFiles = loaded
					}
				}
			}
		}
	}

	localPath := ""
	if env.IsLocal() {
		localPath = s.refreshedLocalPath()
	}
	status := s.inspectOne(ctx, env, target, targetOS, spec, sshConn, daemonRegistered, envPaths, pathsErr == nil, authFiles, localPath)
	if len(status.Dependencies) > 0 {
		var missing []string
		for _, depID := range status.Dependencies {
			if depSpec, ok := LookupSpec(depID); ok {
				if _, found := s.locateBinary(ctx, env, depSpec); !found {
					missing = append(missing, depID)
				}
			}
		}
		status.MissingDependencies = missing
	}
	return status
}

func (s *Service) inspectOne(
	ctx context.Context,
	env envtarget.Env,
	target envtarget.Target,
	targetOS string,
	spec ToolSpec,
	sshConn map[string]any,
	daemonRegistered map[string]bool,
	envPaths envtarget.Paths,
	hasEnvPaths bool,
	authFiles map[string][]byte,
	localPath string,
) ToolStatus {
	status := ToolStatus{
		ID:           spec.ID,
		Kind:         spec.Kind,
		Group:        spec.Group,
		PluginID:     spec.PluginID,
		Name:         spec.Name,
		Description:  spec.Description,
		Readiness:    "missing",
		Dependencies: append([]string(nil), spec.Dependencies...),
		AuthCommand:  spec.AuthCommand,
		AuthHint:     spec.AuthHint,
		DocsURL:      spec.DocsURL,
		Recipes:      RecipesFor(spec.ID, targetOS),
	}

	customPath := s.readCustomExecutable(ctx, target, spec, sshConn)
	status.CustomExecutable = customPath

	var resolved string
	var version string
	inPath := false

	// Check whether any declared binary is already reachable on the live refreshed PATH.
	for _, bin := range spec.Binaries {
		found := ""
		ok := false
		if env.IsLocal() && localPath != "" {
			found, ok = LookPathInPath(bin, localPath)
		}
		if !ok {
			found, ok = env.Which(ctx, bin)
		}
		if ok && strings.TrimSpace(found) != "" {
			inPath = true
			if customPath == "" {
				res := s.probeCandidate(ctx, env, spec, strings.TrimSpace(found))
				if res.Valid {
					resolved = res.ResolvedPath
					version = res.Version
				} else {
					resolved = strings.TrimSpace(found)
					status.Detail = res.Error
				}
			}
			break
		}
	}

	if customPath != "" {
		res := s.probeCandidate(ctx, env, spec, customPath)
		if res.Valid {
			resolved = res.ResolvedPath
			version = res.Version
		} else {
			status.Detail = "自定义路径不可用：" + res.Error
		}
	}
	if resolved == "" {
		if found, ok := s.locateBinary(ctx, env, spec); ok {
			res := s.probeCandidate(ctx, env, spec, found)
			if res.Valid {
				resolved = res.ResolvedPath
				version = res.Version
			} else {
				resolved = found
				status.Detail = res.Error
			}
		}
	}

	if resolved != "" {
		status.Installed = true
		status.InPath = inPath
		status.Executable = resolved
		status.Version = version
		if status.Detail == "" {
			status.Detail = "已检测到可执行文件"
		}
		if !inPath {
			dir := filepath.Dir(resolved)
			if !env.IsLocal() {
				dir = path.Dir(resolved)
			}
			status.PathHint = fmt.Sprintf("可执行文件位于 %s，尚未加入当前会话 PATH", dir)
		}
		if spec.Kind == ItemKindAgent {
			if engine, ok := LookupAgentEngine(spec.ID); ok {
				authOK, authDetail := s.checkAgentAuth(ctx, env, engine, envPaths, hasEnvPaths, authFiles)
				status.AuthConfigured = &authOK
				if authOK {
					status.Readiness = "ready"
					status.ReadinessDetail = authDetail
				} else {
					status.Readiness = "needs_setup"
					status.ReadinessDetail = authDetail
				}
			} else {
				status.Readiness = "ready"
			}
		} else if !inPath {
			status.Readiness = "needs_setup"
			status.ReadinessDetail = status.PathHint
		} else {
			status.Readiness = "ready"
			status.ReadinessDetail = "已在 PATH 中就绪"
		}
	} else if status.Detail == "" {
		status.Detail = "未在当前环境中检测到"
		status.ReadinessDetail = status.Detail
	}

	if target.Kind == "local" && spec.Kind == ItemKindAgent {
		reg := daemonRegistered[spec.ID]
		status.RegisteredInDaemon = &reg
		if s.stateDir != "" {
			status.ConfiguredExecutable = lifecycle.ReadLocalRuntimeExecutable(s.stateDir, spec.ID)
		}
		if status.Installed && !reg {
			status.RequiresDaemonRestart = true
		} else if status.CustomExecutable != "" && status.ConfiguredExecutable != "" && !samePath(status.CustomExecutable, status.ConfiguredExecutable) {
			status.RequiresDaemonRestart = true
		}
	}
	return status
}

func agentHomeForEngine(p envtarget.Paths, homeField string) string {
	switch homeField {
	case "antigravity":
		return p.Antigravity
	case "codex":
		return p.Codex
	case "hermes":
		return p.Hermes
	case "grok":
		return p.Grok
	default:
		return ""
	}
}

func (s *Service) checkAgentAuth(
	ctx context.Context,
	env envtarget.Env,
	engine AgentEngineSpec,
	envPaths envtarget.Paths,
	hasEnvPaths bool,
	authFiles map[string][]byte,
) (bool, string) {
	if hasEnvPaths {
		base := agentHomeForEngine(envPaths, engine.HomeField)
		if base != "" {
			for _, rel := range engine.AuthFiles {
				full := path.Clean(base + "/" + rel)
				if data, ok := authFiles[full]; ok && len(strings.TrimSpace(string(data))) > 0 {
					return true, "已检测到登录凭据或配置文件"
				}
			}
		}
	}
	if env.IsLocal() {
		for _, envVar := range engine.AuthEnvVars {
			if strings.TrimSpace(os.Getenv(envVar)) != "" {
				return true, "已检测到环境变量 " + envVar
			}
		}
	} else if len(engine.AuthEnvVars) > 0 {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var checks []string
		for _, envVar := range engine.AuthEnvVars {
			checks = append(checks, fmt.Sprintf(`[ -n "${%s:-}" ] && { printf '%s\n'; exit 0; }`, envVar, envVar))
		}
		res, err := env.Shell(probeCtx, strings.Join(checks, "; ")+"; exit 1", nil)
		if err == nil && res.Code == 0 {
			if matched := strings.TrimSpace(res.Stdout); matched != "" {
				return true, "远端已检测到环境变量 " + matched
			}
		}
	}
	if engine.AuthHint != "" {
		return false, engine.AuthHint
	}
	return false, "已安装二进制，尚未检测到登录凭据或配置"
}

func samePath(a, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (s *Service) detectPlatform(ctx context.Context, env envtarget.Env) (string, string) {
	if env.IsLocal() {
		return runtime.GOOS, runtime.GOARCH
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := env.Shell(probeCtx, `printf '%s %s\n' "$(uname -s 2>/dev/null || echo linux)" "$(uname -m 2>/dev/null || echo unknown)"`, nil)
	if err != nil {
		return "linux", ""
	}
	fields := strings.Fields(strings.TrimSpace(res.Stdout))
	osName := "linux"
	arch := ""
	if len(fields) > 0 {
		switch strings.ToLower(fields[0]) {
		case "darwin":
			osName = "darwin"
		case "linux":
			osName = "linux"
		default:
			osName = strings.ToLower(fields[0])
		}
	}
	if len(fields) > 1 {
		arch = fields[1]
	}
	return osName, arch
}

func (s *Service) readDaemonRegistered(ctx context.Context, isLocal bool) map[string]bool {
	registered := map[string]bool{}
	daemon := s.getDaemonClient()
	if !isLocal || daemon == nil {
		return registered
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	status, err := daemon.Daemon.GetStatus(probeCtx, &sessiondv1.GetStatusRequest{})
	if err != nil || status == nil {
		return registered
	}
	for _, rt := range status.Runtimes {
		if !rt.GetRegistered() {
			continue
		}
		switch rt.GetRuntime() {
		case sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX:
			registered["codex"] = true
		case sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES:
			registered["hermes"] = true
		case sessiondv1.RuntimeKind_RUNTIME_KIND_GROK:
			registered["grok"] = true
		case sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY:
			registered["antigravity"] = true
		}
	}
	return registered
}

func (s *Service) readCustomExecutable(ctx context.Context, target envtarget.Target, spec ToolSpec, sshConn map[string]any) string {
	if target.Kind == "ssh" && spec.Kind == ItemKindAgent && sshConn != nil {
		settings, _ := sshConn["settings"].(map[string]any)
		if settings == nil {
			settings = sshConn
		}
		key := spec.ID + "_executable"
		if value, _ := settings[key].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		if spec.ID == "hermes" {
			if value, _ := settings["hermes_path"].(string); strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	if target.Kind == "local" && s.store != nil && spec.PluginID != "" {
		return s.store.ReadLocalExecutableOverride(ctx, spec.PluginID)
	}
	return ""
}

func (s *Service) locateBinary(ctx context.Context, env envtarget.Env, spec ToolSpec) (string, bool) {
	if len(spec.Binaries) == 0 {
		return "", false
	}
	if env.IsLocal() {
		refreshedPath := s.refreshedLocalPath()
		for _, bin := range spec.Binaries {
			if found, ok := LookPathInPath(bin, refreshedPath); ok && strings.TrimSpace(found) != "" {
				return strings.TrimSpace(found), true
			}
		}
		for _, bin := range spec.Binaries {
			if found, ok := env.Which(ctx, bin); ok && strings.TrimSpace(found) != "" {
				return strings.TrimSpace(found), true
			}
		}
		for _, bin := range spec.Binaries {
			for _, candidate := range localExtraCandidates(bin, s.runtimesRoot()) {
				if isExecutableFile(candidate) {
					return candidate, true
				}
			}
		}
		return "", false
	}
	for _, bin := range spec.Binaries {
		if found, ok := env.Which(ctx, bin); ok && strings.TrimSpace(found) != "" {
			return strings.TrimSpace(found), true
		}
	}
	// Remote Linux/WSL: search common user-space bin directories too.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	script := `for name in "$@"; do
  for dir in "$HOME/.local/bin" "$HOME/.cargo/bin" "$HOME/.vite-plus/bin" "$HOME/.antigravity/bin" "$HOME/.hermes/hermes-agent/venv/bin" "$HOME/.grok/bin" "$HOME/.local/share/fnm/aliases/default/bin" "$HOME/.nvm/current/bin" /usr/local/bin /opt/homebrew/bin; do
    if [ -x "$dir/$name" ]; then
      printf '%s\n' "$dir/$name"
      exit 0
    fi
  done
done
exit 1`
	res, err := env.Shell(probeCtx, script, nil, spec.Binaries...)
	if err == nil && res.Code == 0 {
		if found := strings.TrimSpace(res.Stdout); found != "" {
			return found, true
		}
	}
	return "", false
}

func localExtraCandidates(binary, runtimesRoot string) []string {
	var out []string
	home, _ := os.UserHomeDir()
	localAppData := os.Getenv("LOCALAPPDATA")
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		userProfile = home
	}
	names := []string{binary}
	if runtime.GOOS == "windows" {
		names = []string{binary + ".exe", binary + ".cmd", binary + ".bat"}
	}
	var dirs []string
	if managed := ManagedRuntimeBinDirs(runtimesRoot); len(managed) > 0 {
		dirs = append(dirs, managed...)
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".vite-plus", "bin"),
			filepath.Join(home, ".antigravity", "bin"),
			filepath.Join(home, ".grok", "bin"),
			filepath.Join(home, ".hermes", "hermes-agent", "venv", "bin"),
		)
	}
	if runtime.GOOS == "windows" {
		if localAppData != "" {
			dirs = append(dirs,
				filepath.Join(localAppData, "agy", "bin"),
				filepath.Join(localAppData, "hermes", "bin"),
				filepath.Join(localAppData, "hermes", "hermes-agent", "venv", "Scripts"),
				filepath.Join(localAppData, "Programs", "nodejs"),
				filepath.Join(localAppData, "Programs", "Git", "cmd"),
			)
		}
		if appData := os.Getenv("APPDATA"); appData != "" {
			dirs = append(dirs, filepath.Join(appData, "npm"))
		}
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			dirs = append(dirs,
				filepath.Join(pf, "nodejs"),
				filepath.Join(pf, "Git", "cmd"),
			)
		}
	}
	for _, dir := range dirs {
		for _, name := range names {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

func (s *Service) ValidateExecutable(ctx context.Context, envID, toolID, rawPath string) (ValidateResult, error) {
	spec, ok := LookupSpec(toolID)
	if !ok {
		return ValidateResult{}, envtarget.Errorf(422, "未知工具或 Agent：%s", toolID)
	}
	candidate := strings.TrimSpace(rawPath)
	if candidate == "" || strings.ContainsRune(candidate, 0) {
		return ValidateResult{Valid: false, Error: "请输入有效的可执行文件路径"}, nil
	}
	env, err := s.resolver.Open(ctx, envID)
	if err != nil {
		return ValidateResult{}, err
	}
	return s.probeCandidate(ctx, env, spec, candidate), nil
}

func (s *Service) probeCandidate(ctx context.Context, env envtarget.Env, spec ToolSpec, candidate string) ValidateResult {
	probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	if env.IsLocal() {
		resolved := candidate
		if !filepath.IsAbs(resolved) && !strings.ContainsAny(resolved, `/\`) {
			if found, ok := LookPathInPath(resolved, s.refreshedLocalPath()); ok {
				resolved = found
			} else if found, err := exec.LookPath(resolved); err == nil {
				resolved = found
			}
		}
		if abs, err := filepath.Abs(resolved); err == nil {
			resolved = abs
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return ValidateResult{Valid: false, Error: "文件不存在或无法访问"}
		}
		if info.IsDir() {
			return ValidateResult{Valid: false, Error: "所选路径是目录，请选择具体可执行文件"}
		}
		cmd := exec.CommandContext(probeCtx, resolved, spec.VersionArgs...)
		configureHiddenCmd(cmd)
		cmd.Env = s.localChildEnv(nil)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		output := strings.TrimSpace(stdout.String())
		if output == "" {
			output = strings.TrimSpace(stderr.String())
		}
		exitCode := 0
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
			runErr = nil
		}
		if runErr != nil && output == "" {
			return ValidateResult{Valid: false, ResolvedPath: resolved, Error: "无法执行该文件：" + runErr.Error()}
		}
		if exitCode != 0 && output == "" {
			return ValidateResult{Valid: false, ResolvedPath: resolved, Error: fmt.Sprintf("执行校验退出码 %d", exitCode)}
		}
		return ValidateResult{
			Valid:        true,
			ResolvedPath: resolved,
			Version:      extractVersion(output),
			Output:       firstLine(output),
		}
	}

	// Remote WSL / SSH validation
	script := `target="$1"; shift
if [ ! -e "$target" ]; then
  if resolved="$(command -v "$target" 2>/dev/null)"; then
    target="$resolved"
  else
    printf '%s\n' "文件不存在：$target" >&2
    exit 127
  fi
fi
if [ -d "$target" ]; then
  printf '%s\n' "所选路径是目录" >&2
  exit 126
fi
if [ ! -x "$target" ]; then
  printf '%s\n' "文件缺少可执行权限" >&2
  exit 126
fi
printf 'RESOLVED:%s\n' "$target"
"$target" "$@" 2>&1`
	args := append([]string{candidate}, spec.VersionArgs...)
	res, err := env.Shell(probeCtx, script, nil, args...)
	if err != nil {
		return ValidateResult{Valid: false, Error: err.Error()}
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	resolved := candidate
	var outLines []string
	for _, line := range lines {
		if strings.HasPrefix(line, "RESOLVED:") {
			resolved = strings.TrimSpace(strings.TrimPrefix(line, "RESOLVED:"))
			continue
		}
		outLines = append(outLines, line)
	}
	output := strings.TrimSpace(strings.Join(outLines, "\n"))
	if output == "" {
		output = strings.TrimSpace(res.Stderr)
	}
	if res.Code != 0 && output == "" {
		return ValidateResult{Valid: false, Error: fmt.Sprintf("远端校验退出码 %d", res.Code)}
	}
	if res.Code == 126 || res.Code == 127 {
		return ValidateResult{Valid: false, Error: firstLine(output)}
	}
	return ValidateResult{
		Valid:        true,
		ResolvedPath: resolved,
		Version:      extractVersion(output),
		Output:       firstLine(output),
	}
}

func (s *Service) SaveExecutable(ctx context.Context, envID, toolID, rawPath string) (SaveExecutableResult, error) {
	spec, ok := LookupSpec(toolID)
	if !ok {
		return SaveExecutableResult{}, envtarget.Errorf(422, "未知工具或 Agent：%s", toolID)
	}
	env, err := s.resolver.Open(ctx, envID)
	if err != nil {
		return SaveExecutableResult{}, err
	}
	target := env.Target()
	trimmed := strings.TrimSpace(rawPath)
	resolved := ""
	if trimmed != "" {
		val := s.probeCandidate(ctx, env, spec, trimmed)
		if !val.Valid {
			return SaveExecutableResult{}, envtarget.Errorf(422, "路径校验失败：%s", val.Error)
		}
		resolved = val.ResolvedPath
	}

	requiresRestart := false
	daemonReloaded := false
	message := "已保存可执行文件路径"

	if target.Kind == "ssh" && spec.Kind == ItemKindAgent {
		if s.store == nil {
			return SaveExecutableResult{}, envtarget.Errorf(500, "连接存储不可用")
		}
		conn, err := s.store.GetSSHConnection(ctx, target.ConnectionID)
		if err != nil {
			return SaveExecutableResult{}, envtarget.Errorf(500, "读取 SSH 连接失败：%v", err)
		}
		if conn == nil {
			return SaveExecutableResult{}, envtarget.Errorf(404, "SSH 连接不存在")
		}
		settings, _ := conn["settings"].(map[string]any)
		if settings == nil {
			settings = map[string]any{}
		}
		key := spec.ID + "_executable"
		if resolved == "" {
			delete(settings, key)
		} else {
			settings[key] = resolved
		}
		conn["settings"] = settings
		if _, err := s.store.SaveSSHConnection(ctx, conn); err != nil {
			return SaveExecutableResult{}, envtarget.Errorf(500, "保存 SSH 路径配置失败：%v", err)
		}
		message = "已保存远端 Agent 路径，连接时立即生效"
	} else if target.Kind == "local" {
		if s.store != nil && spec.PluginID != "" {
			if err := s.store.SavePluginConfigValue(ctx, spec.PluginID, "executable", resolved); err != nil {
				return SaveExecutableResult{}, envtarget.Errorf(500, "保存插件路径配置失败：%v", err)
			}
		}
		if spec.Kind == ItemKindAgent && s.stateDir != "" && resolved != "" {
			changed, err := lifecycle.UpsertLocalRuntime(s.stateDir, s.defaultWorkspace, spec.ID, resolved)
			if err != nil {
				return SaveExecutableResult{}, envtarget.Errorf(500, "更新小内核配置失败：%v", err)
			}
			daemonReg := s.readDaemonRegistered(ctx, true)
			if changed || !daemonReg[spec.ID] {
				reloadRes := s.tryReloadDaemonConfig(ctx)
				if reloadRes.Reloaded {
					daemonReloaded = true
					requiresRestart = false
					message = "已保存路径并自动热重载小内核，新路径立即生效"
				} else {
					requiresRestart = true
					if reloadRes.Detail != "" {
						message = "已保存路径并同步写入小内核配置；" + reloadRes.Detail
					} else {
						message = "已保存路径并同步写入小内核启动配置；重载小内核后新路径生效"
					}
				}
			} else {
				message = "已保存本机 Agent 路径"
			}
		}
	} else {
		// WSL or non-agent remote tool path stored per environment in plugin config
		if s.store != nil && spec.PluginID != "" {
			if err := s.store.SavePluginConfigValue(ctx, spec.PluginID, "executable:"+target.ID, resolved); err != nil {
				return SaveExecutableResult{}, envtarget.Errorf(500, "保存路径配置失败：%v", err)
			}
		}
	}

	updated := s.inspectSingleTool(ctx, env, spec)
	if requiresRestart {
		updated.RequiresDaemonRestart = true
	} else if daemonReloaded {
		updated.RequiresDaemonRestart = false
	}
	return SaveExecutableResult{
		Saved:                 true,
		DaemonReloaded:        daemonReloaded,
		RequiresDaemonRestart: requiresRestart,
		Message:               message,
		Item:                  updated,
	}, nil
}

// LocalInstalledProbe returns a quick check function for agent plugin defaults bootstrap.
func LocalInstalledProbe(ctx context.Context) func(kind string) bool {
	return LocalInstalledProbeWithDirs(ctx, "", "")
}

// LocalInstalledProbeWithDirs returns a quick check function that also includes
// Astrorder-managed portable runtimes under dataDir/stateDir and live PATH.
func LocalInstalledProbeWithDirs(ctx context.Context, dataDir, stateDir string) func(kind string) bool {
	svc := NewService(Options{DataDir: dataDir, StateDir: stateDir})
	env, err := svc.resolver.Open(ctx, "local")
	if err != nil {
		return func(string) bool { return false }
	}
	return func(kind string) bool {
		spec, ok := LookupSpec(kind)
		if !ok {
			return false
		}
		_, found := svc.locateBinary(ctx, env, spec)
		return found
	}
}

func extractVersion(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return ""
	}
	if match := semverRE.FindString(trimmed); match != "" {
		return strings.TrimPrefix(match, "v")
	}
	return firstLine(trimmed)
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	text = strings.TrimSpace(text)
	if len(text) > 160 {
		return text[:160] + "…"
	}
	return text
}

var ErrJobNotFound = errors.New("安装任务不存在")
