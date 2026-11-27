package pluginhost

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultHealthTimeout   = 10 * time.Second
	healthPollInterval     = 60 * time.Millisecond
	gracefulStopTimeout    = 3 * time.Second
	forceKillWaitTimeout   = 1 * time.Second
	defaultCloseTimeout    = 5 * time.Second
	maxProcessLogTailBytes = 4096
)

// allowedBaseEnvVars is the strict whitelist of non-secret OS runtime variables
// forwarded to external plugin processes. All host secrets and ASTRORDER_* vars
// from os.Environ() are excluded by default.
var allowedBaseEnvVars = map[string]struct{}{
	"PATH":                   {},
	"PATHEXT":                {},
	"SYSTEMROOT":             {},
	"SYSTEMDRIVE":            {},
	"WINDIR":                 {},
	"COMSPEC":                {},
	"TEMP":                   {},
	"TMP":                    {},
	"TMPDIR":                 {},
	"USERPROFILE":            {},
	"HOMEDRIVE":              {},
	"HOMEPATH":               {},
	"HOME":                   {},
	"APPDATA":                {},
	"LOCALAPPDATA":           {},
	"PROGRAMDATA":            {},
	"PROGRAMFILES":           {},
	"PROGRAMFILES(X86)":      {},
	"COMMONPROGRAMFILES":     {},
	"OS":                     {},
	"PROCESSOR_ARCHITECTURE": {},
	"NUMBER_OF_PROCESSORS":   {},
	"LANG":                   {},
	"LC_ALL":                 {},
	"LC_CTYPE":               {},
	"TZ":                     {},
	"TERM":                   {},
	"NO_COLOR":               {},
	"FORCE_COLOR":            {},
	"SSL_CERT_FILE":          {},
	"SSL_CERT_DIR":           {},
	"XDG_CONFIG_HOME":        {},
	"XDG_DATA_HOME":          {},
	"XDG_CACHE_HOME":         {},
	"XDG_RUNTIME_DIR":        {},
}

var proxyEnvVars = map[string]struct{}{
	"HTTP_PROXY":  {},
	"HTTPS_PROXY": {},
	"NO_PROXY":    {},
	"ALL_PROXY":   {},
}

var reservedPluginEnvKeys = map[string]struct{}{
	"ASTRORDER_PLUGIN_ID":               {},
	"ASTRORDER_PLUGIN_PORT":             {},
	"ASTRORDER_PLUGIN_ADDR":             {},
	"ASTRORDER_PLUGIN_DIR":              {},
	"ASTRORDER_PLUGIN_HOST_URL":         {},
	"ASTRORDER_PLUGIN_HOST_TOKEN":       {},
	"ASTRORDER_PLUGIN_NETWORK_OUTBOUND": {},
	"ASTRORDER_PLUGIN_NETWORK_POLICY":   {},
	"ASTRORDER_PLUGIN_PERMISSIONS":      {},
	"ASTRORDER_HOST_URL":                {},
	"ASTRORDER_HOST_TOKEN":              {},
}

type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) >= b.max {
		b.buf.Reset()
		_, _ = b.buf.Write(p[len(p)-b.max:])
		return len(p), nil
	}
	if b.buf.Len()+len(p) > b.max {
		overflow := b.buf.Len() + len(p) - b.max
		b.buf.Next(overflow)
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.buf.String())
}

// RuntimeStatus captures the live runtime status of a managed plugin process.
type RuntimeStatus struct {
	State                  string
	Error                  string
	BaseURL                string
	Port                   int
	NetworkOutboundAllowed bool
	NetworkPolicy          string
}

type managedProcess struct {
	pluginID               string
	dir                    string
	manifest               Manifest
	port                   int
	baseURL                string
	hostToken              string
	state                  string
	lastErr                string
	networkOutboundAllowed bool
	networkPolicy          string
	cmd                    *exec.Cmd
	guard                  *pluginProcessGuard
	cancel                 context.CancelFunc
	done                   chan struct{}
	stopping               bool
	logs                   *tailBuffer
}

// Supervisor manages external process-backed plugins, ephemeral loopback ports,
// per-plugin host tokens, health checks, lifecycle teardown, and reverse proxying.
type Supervisor struct {
	mu             sync.RWMutex
	procs          map[string]*managedProcess
	hostBaseURL    string
	healthTimeout  time.Duration
	logger         *slog.Logger
	healthClient   *http.Client
	proxyTransport http.RoundTripper
}

func NewSupervisor(hostBaseURL string, logger *slog.Logger) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Supervisor{
		procs:         map[string]*managedProcess{},
		hostBaseURL:   strings.TrimRight(strings.TrimSpace(hostBaseURL), "/"),
		healthTimeout: defaultHealthTimeout,
		logger:        logger,
		healthClient: &http.Client{
			Timeout: 1 * time.Second,
			Transport: &http.Transport{
				Proxy: nil,
			},
		},
		proxyTransport: &http.Transport{
			Proxy:                 proxyExceptLoopback,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          64,
			IdleConnTimeout:       60 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

func proxyExceptLoopback(req *http.Request) (*url.URL, error) {
	if req != nil && req.URL != nil && isLoopbackHost(req.URL.Hostname()) {
		return nil, nil
	}
	return http.ProxyFromEnvironment(req)
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// SetHostBaseURL updates the host loopback URL passed to newly started plugin processes.
func (s *Supervisor) SetHostBaseURL(baseURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hostBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// Status returns the current runtime status of pluginID.
func (s *Supervisor) Status(pluginID string) RuntimeStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.procs[pluginID]
	if !ok {
		return RuntimeStatus{State: RuntimeStateStopped}
	}
	return RuntimeStatus{
		State:                  p.state,
		Error:                  p.lastErr,
		BaseURL:                p.baseURL,
		Port:                   p.port,
		NetworkOutboundAllowed: p.networkOutboundAllowed,
		NetworkPolicy:          p.networkPolicy,
	}
}

// ResolveHostToken maps a presented host token to its active owning pluginID
// using constant-time comparison to avoid timing side-channels.
func (s *Supervisor) ResolveHostToken(presented string) (string, bool) {
	token := strings.TrimSpace(presented)
	if token == "" {
		return "", false
	}
	presentedBytes := []byte(token)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, p := range s.procs {
		if p == nil || p.hostToken == "" {
			continue
		}
		if p.state != RuntimeStateReady && p.state != RuntimeStateStarting {
			continue
		}
		if subtle.ConstantTimeCompare(presentedBytes, []byte(p.hostToken)) == 1 {
			return id, true
		}
	}
	return "", false
}

// Start ensures the external process plugin is running and healthy using a strict
// environment whitelist and permission-scoped network boundary.
func (s *Supervisor) Start(ctx context.Context, manifest Manifest, pluginDir string, grantedPermissions []string) error {
	if manifest.Backend.Type != BackendTypeProcess {
		return nil
	}
	if len(manifest.Backend.Command) == 0 {
		return fmt.Errorf("%w: process plugin %q has empty command", ErrInvalid, manifest.ID)
	}

	s.mu.Lock()
	if existing, ok := s.procs[manifest.ID]; ok && existing.state == RuntimeStateReady {
		s.mu.Unlock()
		return nil
	}
	hostURL := s.hostBaseURL
	healthTimeout := s.healthTimeout
	s.mu.Unlock()

	// Stop any previous instance cleanly before relaunching.
	s.Stop(manifest.ID)

	port, err := allocateLoopbackPort()
	if err != nil {
		s.recordError(manifest.ID, fmt.Sprintf("allocate port: %v", err))
		return err
	}
	hostToken, err := generateHostToken()
	if err != nil {
		s.recordError(manifest.ID, fmt.Sprintf("generate host token: %v", err))
		return err
	}

	binPath := resolvePluginExecutable(pluginDir, manifest.Backend.Command[0])
	args := make([]string, 0, len(manifest.Backend.Command)-1+len(manifest.Backend.Args))
	if len(manifest.Backend.Command) > 1 {
		args = append(args, manifest.Backend.Command[1:]...)
	}
	args = append(args, manifest.Backend.Args...)

	procCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, binPath, args...)
	cmd.Dir = pluginDir
	configurePluginProcess(cmd)

	allowNet := hasEffectiveNetworkOutbound(manifest, grantedPermissions)
	netPolicy := NetworkPolicyBlocked
	if allowNet {
		netPolicy = NetworkPolicyAllowed
	}
	effectiveGranted, _ := computePermissionState(manifest.Permissions, grantedPermissions)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd.Env = buildPluginEnv(manifest, pluginDir, port, hostURL, hostToken, grantedPermissions)

	logs := &tailBuffer{max: maxProcessLogTailBytes}
	cmd.Stdout = logs
	cmd.Stderr = logs

	guard, err := startPluginProcess(cmd)
	if err != nil {
		cancel()
		errMsg := fmt.Sprintf("failed to start process: %v", err)
		s.recordError(manifest.ID, errMsg)
		return fmt.Errorf("%s", errMsg)
	}

	s.logger.Info(
		"external plugin process started with runtime permission boundary",
		"plugin_id", manifest.ID,
		"pid", cmd.Process.Pid,
		"network_outbound", allowNet,
		"network_policy", netPolicy,
		"proxy_env_passthrough", allowNet,
		"granted_permissions", effectiveGranted,
	)

	proc := &managedProcess{
		pluginID:               manifest.ID,
		dir:                    pluginDir,
		manifest:               manifest,
		port:                   port,
		baseURL:                baseURL,
		hostToken:              hostToken,
		state:                  RuntimeStateStarting,
		networkOutboundAllowed: allowNet,
		networkPolicy:          netPolicy,
		cmd:                    cmd,
		guard:                  guard,
		cancel:                 cancel,
		done:                   make(chan struct{}),
		logs:                   logs,
	}

	s.mu.Lock()
	s.procs[manifest.ID] = proc
	s.mu.Unlock()

	go s.watchProcess(proc)

	// Poll health endpoint until ready, exited, or timed out.
	healthPath := manifest.Backend.Health
	if healthPath == "" {
		healthPath = "/health"
	}
	healthURL := baseURL + healthPath
	deadline := time.Now().Add(healthTimeout)
	var lastHealthErr error

	for {
		select {
		case <-proc.done:
			detail := strings.TrimSpace(proc.logs.String())
			if detail != "" {
				errMsg := fmt.Sprintf("plugin process exited before becoming healthy: %s", detail)
				s.setProcessError(proc, errMsg)
				return errors.New(errMsg)
			}
			errMsg := "plugin process exited before becoming healthy"
			s.setProcessError(proc, errMsg)
			return errors.New(errMsg)
		case <-ctx.Done():
			s.Stop(manifest.ID)
			s.recordError(manifest.ID, fmt.Sprintf("startup cancelled: %v", ctx.Err()))
			return ctx.Err()
		default:
		}

		if err := s.checkHealth(ctx, healthURL); err == nil {
			s.mu.Lock()
			if s.procs[manifest.ID] == proc && !proc.stopping {
				proc.state = RuntimeStateReady
				proc.lastErr = ""
			}
			s.mu.Unlock()
			return nil
		} else {
			lastHealthErr = err
		}

		if time.Now().After(deadline) {
			tail := strings.TrimSpace(proc.logs.String())
			errMsg := fmt.Sprintf("health check %s timed out after %s: %v", healthPath, healthTimeout, lastHealthErr)
			if tail != "" {
				errMsg = errMsg + " (logs: " + tail + ")"
			}
			s.Stop(manifest.ID)
			s.recordError(manifest.ID, errMsg)
			return errors.New(errMsg)
		}

		timer := time.NewTimer(healthPollInterval)
		select {
		case <-proc.done:
			timer.Stop()
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (s *Supervisor) watchProcess(proc *managedProcess) {
	waitErr := proc.cmd.Wait()
	if proc.guard != nil {
		_ = proc.guard.Release()
	}
	close(proc.done)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.procs[proc.pluginID] != proc {
		return
	}
	proc.hostToken = ""
	if proc.stopping {
		proc.state = RuntimeStateStopped
		return
	}
	proc.state = RuntimeStateError
	if waitErr != nil {
		tail := strings.TrimSpace(proc.logs.String())
		if tail != "" {
			proc.lastErr = fmt.Sprintf("process exited unexpectedly: %v (%s)", waitErr, tail)
		} else {
			proc.lastErr = fmt.Sprintf("process exited unexpectedly: %v", waitErr)
		}
	} else {
		proc.lastErr = "process exited unexpectedly with status 0"
	}
	s.logger.Warn("external plugin process exited unexpectedly", "plugin_id", proc.pluginID, "error", proc.lastErr)
}

func (s *Supervisor) checkHealth(ctx context.Context, healthURL string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.healthClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}

func (s *Supervisor) recordError(pluginID, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.procs[pluginID]; ok {
		existing.state = RuntimeStateError
		existing.lastErr = msg
		existing.hostToken = ""
		return
	}
	s.procs[pluginID] = &managedProcess{
		pluginID: pluginID,
		state:    RuntimeStateError,
		lastErr:  msg,
	}
}

func (s *Supervisor) setProcessError(proc *managedProcess, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.procs[proc.pluginID] == proc {
		proc.state = RuntimeStateError
		proc.lastErr = msg
		proc.hostToken = ""
	}
}

// Stop gracefully terminates the managed process for pluginID and revokes its host token.
func (s *Supervisor) Stop(pluginID string) {
	ctx, cancel := context.WithTimeout(context.Background(), gracefulStopTimeout+forceKillWaitTimeout+time.Second)
	defer cancel()
	_ = s.StopContext(ctx, pluginID)
}

// StopContext gracefully terminates pluginID, escalating to a hard kill of its
// process group / Windows Job Object if gracefulStopTimeout or ctx expires.
func (s *Supervisor) StopContext(ctx context.Context, pluginID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	proc, ok := s.procs[pluginID]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	proc.stopping = true
	proc.hostToken = ""
	proc.state = RuntimeStateStopped
	proc.lastErr = ""
	delete(s.procs, pluginID)
	s.mu.Unlock()

	if proc.cmd == nil || proc.done == nil {
		if proc.guard != nil {
			_ = proc.guard.Release()
		}
		if proc.cancel != nil {
			proc.cancel()
		}
		return nil
	}

	proc.guard.Terminate(proc.cmd)
	timer := time.NewTimer(gracefulStopTimeout)
	defer timer.Stop()

	var timedOutErr error
	select {
	case <-proc.done:
	case <-timer.C:
		proc.guard.ForceKill(proc.cmd)
		if proc.cancel != nil {
			proc.cancel()
		}
		forceTimer := time.NewTimer(forceKillWaitTimeout)
		select {
		case <-proc.done:
		case <-forceTimer.C:
			timedOutErr = fmt.Errorf("plugin %q did not exit after force kill", pluginID)
		case <-ctx.Done():
			timedOutErr = ctx.Err()
		}
		forceTimer.Stop()
	case <-ctx.Done():
		proc.guard.ForceKill(proc.cmd)
		if proc.cancel != nil {
			proc.cancel()
		}
		forceTimer := time.NewTimer(forceKillWaitTimeout)
		select {
		case <-proc.done:
		case <-forceTimer.C:
			timedOutErr = ctx.Err()
		}
		forceTimer.Stop()
	}

	if proc.cancel != nil {
		proc.cancel()
	}
	if proc.guard != nil {
		_ = proc.guard.Release()
	}
	return timedOutErr
}

// Close terminates all managed plugin processes using defaultCloseTimeout.
func (s *Supervisor) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCloseTimeout)
	defer cancel()
	_ = s.CloseContext(ctx)
}

// CloseContext terminates all managed plugin processes in parallel and blocks
// until every process exits or ctx expires.
func (s *Supervisor) CloseContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.RLock()
	ids := make([]string, 0, len(s.procs))
	for id := range s.procs {
		ids = append(ids, id)
	}
	s.mu.RUnlock()

	if len(ids) == 0 {
		return nil
	}

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
	)
	for _, id := range ids {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			if err := s.StopContext(ctx, pid); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(id)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return firstErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ProxyHTTP reverse-proxies an extension request to targetBaseURL + subpath,
// stripping sensitive browser credentials (Authorization, Cookie, Host Token).
func (s *Supervisor) ProxyHTTP(w http.ResponseWriter, r *http.Request, pluginID, targetBaseURL, subpath string) {
	parsedBase, err := url.Parse(strings.TrimRight(targetBaseURL, "/"))
	if err != nil || parsedBase.Host == "" {
		writeJSONError(w, http.StatusBadGateway, "Invalid plugin upstream URL")
		return
	}
	upstreamPath := "/" + strings.TrimLeft(subpath, "/")
	if parsedBase.Path != "" && parsedBase.Path != "/" {
		upstreamPath = strings.TrimRight(parsedBase.Path, "/") + upstreamPath
	}

	proxy := &httputil.ReverseProxy{
		Transport: s.proxyTransport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(parsedBase)
			pr.Out.URL.Path = upstreamPath
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = parsedBase.Host

			// Strip sensitive browser/host auth headers before forwarding to external plugin.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Proxy-Authorization")
			pr.Out.Header.Del("X-Astrorder-Host-Token")
			pr.Out.Header.Set("X-Astrorder-Plugin-ID", pluginID)
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, proxyErr error) {
			writeJSONError(rw, http.StatusBadGateway, fmt.Sprintf("Plugin backend unavailable: %v", proxyErr))
		},
	}
	proxy.ServeHTTP(w, r)
}

func allocateLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return 0, errors.New("unexpected listener address type")
	}
	port := addr.Port
	_ = ln.Close()
	return port, nil
}

func generateHostToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func resolvePluginExecutable(pluginDir, bin string) string {
	trimmed := strings.TrimSpace(bin)
	if strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, `.\\`) {
		return filepath.Join(pluginDir, trimmed)
	}
	if !filepath.IsAbs(trimmed) && strings.ContainsAny(trimmed, `/\`) {
		return filepath.Join(pluginDir, trimmed)
	}
	return trimmed
}

func hasEffectiveNetworkOutbound(manifest Manifest, grantedPermissions []string) bool {
	return hasPermission(manifest.Permissions, PermNetworkOutbound) &&
		hasPermission(grantedPermissions, PermNetworkOutbound)
}

func isProxyEnvVar(key string) bool {
	_, ok := proxyEnvVars[strings.ToUpper(strings.TrimSpace(key))]
	return ok
}

func isForbiddenManifestEnvVar(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return true
	}
	if _, reserved := reservedPluginEnvKeys[upper]; reserved {
		return true
	}
	if strings.HasPrefix(upper, "ASTRORDER_BROWSER_") ||
		strings.HasPrefix(upper, "ASTRORDER_CONNECTOR_") ||
		strings.HasPrefix(upper, "ASTRORDER_SESSION_DAEMON_") ||
		strings.HasPrefix(upper, "ASTRORDER_PLUGIN_") {
		return true
	}
	return false
}

// buildPluginEnv constructs an isolated subprocess environment using a strict
// whitelist of base OS runtime variables plus explicit safe plugin-scoped variables.
// It NEVER copies full os.Environ() and NEVER leaks host secrets such as
// ASTRORDER_HOST_TOKEN, ASTRORDER_BROWSER_SECRET, or ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL.
// Proxy variables (HTTP_PROXY/HTTPS_PROXY/NO_PROXY/ALL_PROXY) are forwarded ONLY
// when network:outbound is both declared in manifest.Permissions and granted.
func buildPluginEnv(manifest Manifest, pluginDir string, port int, hostURL, hostToken string, grantedPermissions []string) []string {
	allowNetworkOutbound := hasEffectiveNetworkOutbound(manifest, grantedPermissions)

	// Deduplicate case-insensitively (important on Windows) while preserving canonical names.
	type envEntry struct {
		key string
		val string
	}
	envByUpper := map[string]envEntry{}
	setEnv := func(k, v string) {
		trimmed := strings.TrimSpace(k)
		if trimmed == "" {
			return
		}
		envByUpper[strings.ToUpper(trimmed)] = envEntry{key: trimmed, val: v}
	}

	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(k))
		if upper == "" {
			continue
		}
		if _, allowedBase := allowedBaseEnvVars[upper]; allowedBase {
			setEnv(k, v)
			continue
		}
		if allowNetworkOutbound && isProxyEnvVar(upper) {
			setEnv(k, v)
		}
	}

	// Apply manifest-declared environment variables while blocking reserved host
	// secrets, reserved plugin runtime keys, and unauthorized proxy overrides.
	for k, v := range manifest.Backend.Env {
		key := strings.TrimSpace(k)
		if key == "" || isForbiddenManifestEnvVar(key) {
			continue
		}
		if !allowNetworkOutbound && isProxyEnvVar(key) {
			continue
		}
		setEnv(key, v)
	}

	// Explicitly clear proxy variables when network:outbound is not granted.
	if !allowNetworkOutbound {
		for proxyKey := range proxyEnvVars {
			delete(envByUpper, proxyKey)
		}
	}

	effectiveGranted, _ := computePermissionState(manifest.Permissions, grantedPermissions)
	sort.Strings(effectiveGranted)

	// Explicitly inject safe plugin-scoped runtime variables.
	// Note: hostToken (when non-empty) is a freshly generated per-plugin ephemeral
	// token bound solely to manifest.ID via Supervisor.ResolveHostToken, never any
	// host secret inherited from os.Environ().
	setEnv("ASTRORDER_PLUGIN_ID", manifest.ID)
	setEnv("ASTRORDER_PLUGIN_PORT", strconv.Itoa(port))
	setEnv("ASTRORDER_PLUGIN_ADDR", fmt.Sprintf("127.0.0.1:%d", port))
	setEnv("ASTRORDER_PLUGIN_DIR", pluginDir)
	setEnv("ASTRORDER_PLUGIN_PERMISSIONS", strings.Join(effectiveGranted, ","))
	if allowNetworkOutbound {
		setEnv("ASTRORDER_PLUGIN_NETWORK_OUTBOUND", "1")
		setEnv("ASTRORDER_PLUGIN_NETWORK_POLICY", NetworkPolicyAllowed)
	} else {
		setEnv("ASTRORDER_PLUGIN_NETWORK_OUTBOUND", "0")
		setEnv("ASTRORDER_PLUGIN_NETWORK_POLICY", NetworkPolicyBlocked)
	}
	if strings.TrimSpace(hostURL) != "" {
		setEnv("ASTRORDER_PLUGIN_HOST_URL", hostURL)
		setEnv("ASTRORDER_HOST_URL", hostURL)
	}
	if strings.TrimSpace(hostToken) != "" {
		setEnv("ASTRORDER_PLUGIN_HOST_TOKEN", hostToken)
		setEnv("ASTRORDER_HOST_TOKEN", hostToken)
	}

	keys := make([]string, 0, len(envByUpper))
	for upper := range envByUpper {
		keys = append(keys, upper)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, upper := range keys {
		item := envByUpper[upper]
		out = append(out, item.key+"="+item.val)
	}
	return out
}
