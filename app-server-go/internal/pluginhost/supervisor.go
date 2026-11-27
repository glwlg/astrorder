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
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultHealthTimeout   = 10 * time.Second
	healthPollInterval     = 60 * time.Millisecond
	gracefulStopTimeout    = 3 * time.Second
	maxProcessLogTailBytes = 4096
)

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
	State   string
	Error   string
	BaseURL string
	Port    int
}

type managedProcess struct {
	pluginID  string
	dir       string
	manifest  Manifest
	port      int
	baseURL   string
	hostToken string
	state     string
	lastErr   string
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	done      chan struct{}
	stopping  bool
	logs      *tailBuffer
}

// Supervisor manages external process-backed plugins, ephemeral loopback ports,
// per-plugin host tokens, health checks, lifecycle teardown, and reverse proxying.
type Supervisor struct {
	mu            sync.RWMutex
	procs         map[string]*managedProcess
	hostBaseURL   string
	healthTimeout time.Duration
	logger        *slog.Logger
	healthClient  *http.Client
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
		},
		proxyTransport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          64,
			IdleConnTimeout:       60 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// SetHostBaseURL updates the ASTRORDER_HOST_URL passed to newly started plugin processes.
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
		State:   p.state,
		Error:   p.lastErr,
		BaseURL: p.baseURL,
		Port:    p.port,
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

// Start ensures the external process plugin is running and healthy.
func (s *Supervisor) Start(ctx context.Context, manifest Manifest, pluginDir string) error {
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

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd.Env = buildPluginEnv(manifest, pluginDir, port, hostURL, hostToken)

	logs := &tailBuffer{max: maxProcessLogTailBytes}
	cmd.Stdout = logs
	cmd.Stderr = logs

	proc := &managedProcess{
		pluginID:  manifest.ID,
		dir:       pluginDir,
		manifest:  manifest,
		port:      port,
		baseURL:   baseURL,
		hostToken: hostToken,
		state:     RuntimeStateStarting,
		cmd:       cmd,
		cancel:    cancel,
		done:      make(chan struct{}),
		logs:      logs,
	}

	if err := cmd.Start(); err != nil {
		cancel()
		errMsg := fmt.Sprintf("failed to start process: %v", err)
		s.recordError(manifest.ID, errMsg)
		return fmt.Errorf("%s", errMsg)
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
	s.mu.Lock()
	proc, ok := s.procs[pluginID]
	if !ok {
		s.mu.Unlock()
		return
	}
	proc.stopping = true
	proc.hostToken = ""
	proc.state = RuntimeStateStopped
	proc.lastErr = ""
	delete(s.procs, pluginID)
	s.mu.Unlock()

	if proc.cmd == nil || proc.done == nil {
		if proc.cancel != nil {
			proc.cancel()
		}
		return
	}

	terminatePluginProcess(proc.cmd)
	timer := time.NewTimer(gracefulStopTimeout)
	defer timer.Stop()
	select {
	case <-proc.done:
	case <-timer.C:
		forceKillPluginProcess(proc.cmd)
		if proc.cancel != nil {
			proc.cancel()
		}
		select {
		case <-proc.done:
		case <-time.After(1 * time.Second):
		}
	}
	if proc.cancel != nil {
		proc.cancel()
	}
}

// Close terminates all managed plugin processes.
func (s *Supervisor) Close() {
	s.mu.RLock()
	ids := make([]string, 0, len(s.procs))
	for id := range s.procs {
		ids = append(ids, id)
	}
	s.mu.RUnlock()

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			s.Stop(pid)
		}(id)
	}
	wg.Wait()
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

func buildPluginEnv(manifest Manifest, pluginDir string, port int, hostURL, hostToken string) []string {
	envMap := map[string]string{}
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(k, "ASTRORDER_HOST_TOKEN") {
			continue
		}
		envMap[k] = v
	}
	for k, v := range manifest.Backend.Env {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		envMap[key] = v
	}
	envMap["ASTRORDER_PLUGIN_ID"] = manifest.ID
	envMap["ASTRORDER_PLUGIN_PORT"] = strconv.Itoa(port)
	envMap["ASTRORDER_PLUGIN_ADDR"] = fmt.Sprintf("127.0.0.1:%d", port)
	envMap["ASTRORDER_HOST_URL"] = hostURL
	envMap["ASTRORDER_HOST_TOKEN"] = hostToken
	envMap["ASTRORDER_PLUGIN_DIR"] = pluginDir

	out := make([]string, 0, len(envMap))
	for k, v := range envMap {
		out = append(out, k+"="+v)
	}
	return out
}
