package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/audio"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/botgroups"
	"astrorder.dev/app-server/internal/browser"
	"astrorder.dev/app-server/internal/capabilities"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/httpapi"
	"astrorder.dev/app-server/internal/kernelcontrol"
	"astrorder.dev/app-server/internal/memory"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/notifications"
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/settingsapi"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
	"astrorder.dev/app-server/internal/toolchain"
)

const (
	gracefulShutdownTimeout  = 10 * time.Second
	daemonInitialDialTimeout = 5 * time.Second
	daemonReconnectMinDelay  = 1 * time.Second
	daemonReconnectMaxDelay  = 10 * time.Second
)

// bundledHelperAssetsDir is <bin>/helpers next to the running executable, where
// the per-platform astrorder-helper builds are staged.
func bundledHelperAssetsDir() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "helpers")
}

// resolveOfficialPluginsDir locates the read-only official plugins directory.
// Priority: ASTRORDER_OFFICIAL_PLUGINS_DIR env override -> installed layout
// (<ExeDir>/../plugins/official, <ExeDir>/plugins/official, <ConfigDir>/plugins/official)
// -> local development build output (plugins/official-dist, plugins/official).
// Never points into the writable external plugins directory.
func resolveOfficialPluginsDir(cfg config.Config) string {
	if override := strings.TrimSpace(os.Getenv("ASTRORDER_OFFICIAL_PLUGINS_DIR")); override != "" {
		return filepath.Clean(override)
	}
	candidates := []string{}
	add := func(p string) {
		clean := filepath.Clean(p)
		for _, existing := range candidates {
			if existing == clean {
				return
			}
		}
		candidates = append(candidates, clean)
	}

	var defaultInstalled string
	if executable, err := os.Executable(); err == nil && executable != "" {
		exeDir := filepath.Dir(executable)
		defaultInstalled = filepath.Clean(filepath.Join(exeDir, "..", "plugins", "official"))
		add(defaultInstalled)
		add(filepath.Join(exeDir, "plugins", "official"))
		add(filepath.Join(exeDir, "..", "..", "plugins", "official"))
	}
	if src := strings.TrimSpace(cfg.SourcePath); src != "" {
		cfgDir := filepath.Dir(src)
		add(filepath.Join(cfgDir, "plugins", "official"))
		add(filepath.Join(cfgDir, "..", "plugins", "official"))
	}
	if wd, err := os.Getwd(); err == nil && wd != "" {
		add(filepath.Join(wd, "plugins", "official-dist"))
		add(filepath.Join(wd, "..", "plugins", "official-dist"))
		add(filepath.Join(wd, "plugins", "official"))
		add(filepath.Join(wd, "..", "plugins", "official"))
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return defaultInstalled
}

type readyMessage struct {
	Ready   bool   `json:"ready"`
	Address string `json:"address"`
	PID     int    `json:"pid"`
}

// swappableHandler lets the HTTP server start immediately in degraded mode when
// the Session Daemon is temporarily unreachable and atomically upgrade to the
// full handler once the background reconnector establishes gRPC connectivity.
type swappableHandler struct {
	current atomic.Pointer[http.Handler]
}

func newSwappableHandler(initial http.Handler) *swappableHandler {
	h := &swappableHandler{}
	h.Swap(initial)
	return h
}

func (h *swappableHandler) Swap(next http.Handler) {
	if next == nil {
		return
	}
	h.current.Store(&next)
}

func (h *swappableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ptr := h.current.Load(); ptr != nil && *ptr != nil {
		(*ptr).ServeHTTP(w, r)
		return
	}
	http.Error(w, "Service initializing", http.StatusServiceUnavailable)
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("astrorder-app", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", "", "listen address, for example 127.0.0.1:0")
	staticDir := flags.String("static-dir", "", "compiled frontend directory")
	configPath := flags.String("config", "", "optional production.json path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	cfg.SourcePath = *configPath
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *staticDir != "" {
		cfg.StaticDir = *staticDir
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if cfg.Daemon.Target == "" {
		return errors.New("ASTRORDER_SESSION_DAEMON_GRPC_TARGET is required")
	}

	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServer(ctx, cfg, stdout, logger)
}

type daemonBinding struct {
	client          *daemonclient.Client
	eventProjection *projection.Projection
	projectionDone  chan struct{}
	projectionErr   chan error
	cancel          context.CancelFunc
	groupService    *botgroups.Service
	modelHandler    http.Handler
	connHandler     *connections.Handler
	httpHandler     http.Handler
	handlerCancel   context.CancelFunc
	closeOnce       sync.Once
}

func closeHTTPHandler(h http.Handler, logger *slog.Logger) {
	if h == nil {
		return
	}
	closer, ok := h.(interface{ Close(context.Context) error })
	if !ok {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
	defer cancel()
	if err := closer.Close(closeCtx); err != nil && logger != nil {
		logger.Warn("http handler close failed", "error", err)
	}
}

func (b *daemonBinding) close(logger *slog.Logger) {
	if b == nil {
		return
	}
	b.closeOnce.Do(func() {
		if b.handlerCancel != nil {
			b.handlerCancel()
		}
		closeHTTPHandler(b.httpHandler, logger)
		if b.cancel != nil {
			b.cancel()
		}
		if b.connHandler != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
			if err := b.connHandler.Close(closeCtx); err != nil {
				logger.Warn("close upgrade jobs failed", "error", err)
			}
			cancel()
		}
		if closer, ok := b.modelHandler.(interface{ Close(context.Context) error }); ok {
			closeCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
			if err := closer.Close(closeCtx); err != nil {
				logger.Warn("model job cleanup failed", "error", err)
			}
			cancel()
		}
		if b.groupService != nil {
			b.groupService.Close()
		}
		if b.client != nil {
			_ = b.client.Close()
		}
		if b.projectionDone != nil {
			<-b.projectionDone
		}
	})
}

func runServer(ctx context.Context, cfg config.Config, ready io.Writer, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	var st *store.Store
	if cfg.DatabasePath != "" {
		opened, err := store.Open(cfg.DatabasePath)
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		st = opened
		if err := st.RepairGrokRegistrations(ctx); err != nil {
			_ = st.Close()
			return fmt.Errorf("repair Grok registrations: %w", err)
		}
		if err := st.RepairRemovedAntigravityCatalog(ctx); err != nil {
			_ = st.Close()
			return fmt.Errorf("repair removed Antigravity catalog: %w", err)
		}
		defer func() {
			if closeErr := st.Close(); closeErr != nil {
				logger.Warn("failed to close store", "error", closeErr)
			}
		}()
	}

	if cfg.Migration.Upstream != "" || len(cfg.Migration.Routes) != 0 {
		return errors.New("Python migration upstream is no longer supported; use the Go handlers")
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	serverContext, stopServer := context.WithCancel(ctx)
	defer stopServer()

	hostAddr := listener.Addr().String()
	if h, p, splitErr := net.SplitHostPort(hostAddr); splitErr == nil {
		if h == "" || h == "0.0.0.0" || h == "::" {
			h = "127.0.0.1"
		}
		hostAddr = net.JoinHostPort(h, p)
	}
	_, listenPort, _ := net.SplitHostPort(cfg.Listen)
	portNumber, _ := strconv.Atoi(listenPort)

	var eventHub *application.EventHub
	var memoryService *memory.Service
	if st != nil {
		eventHub = application.NewEventHub(st)

		homeDir, err := os.UserHomeDir()
		if err != nil || homeDir == "" {
			return fmt.Errorf("resolve user home directory: %w", err)
		}
		fallbackConfig := filepath.Join(homeDir, ".openviking", "ovcli.conf")
		memConfigService, memErr := memory.NewConfigService(memory.ConfigServiceOptions{
			Preferences:  st,
			FallbackPath: fallbackConfig,
		})
		if memErr != nil {
			return fmt.Errorf("initialize memory config service: %w", memErr)
		}

		memoryEnv := application.MemoryEnvironment{Resolver: &envtarget.Resolver{Store: st, Remote: targetfs.NewResolver(targetfs.ResolverOptions{Store: st, HelperAssetsDir: bundledHelperAssetsDir()})}}
		targetResolver := memoryEnv
		syncer, syncerErr := memory.NewSyncer(memory.SyncerOptions{
			Resolver: targetResolver,
			FS:       memoryEnv,
			Runner:   memoryEnv,
		})
		if syncerErr != nil {
			return fmt.Errorf("initialize memory syncer: %w", syncerErr)
		}

		clientFactory := memory.NewClientFactory(memConfigService, memory.OpenVikingClientOptions{})
		memSvc, svcErr := memory.NewService(memory.ServiceOptions{
			Config:  memConfigService,
			Client:  clientFactory,
			Targets: targetResolver,
			Syncer:  syncer,
			Authorize: func(r *http.Request) error {
				// Checked by httpapi authenticator inside memory handler
				return nil
			},
		})
		if svcErr != nil {
			return fmt.Errorf("initialize memory service: %w", svcErr)
		}
		memoryService = memSvc
	}

	// Build daemon-independent services so Settings, Toolchain, Onboarding,
	// Capabilities, Office, Meetings, and Docs stay fully available even if the
	// Session Daemon is temporarily unreachable at startup.
	var audioService *audio.Service
	var notificationService *notifications.Service
	var capabilityService *capabilities.Service
	var toolchainService *toolchain.Service
	var remoteExecutable connections.RemoteExecutableResolver
	var authorizer *auth.Authenticator
	var hookResolver *targetfs.Resolver
	var envResolver *envtarget.Resolver
	var browserHandler, settingsHandler, capabilitiesHandler, toolchainHandler, notificationHandler http.Handler
	var dataDir, stateDir string
	if st != nil {
		remoteExecutable = newCachedRemoteExecutableResolver(&connections.NativeRemoteExecutableResolver{}, 30*time.Second)
		authorizer = auth.New(cfg, &httpapi.StoreOriginLookup{Store: st})
		notificationService, err = notifications.New(serverContext, st, authorizer, logger)
		if err != nil {
			return fmt.Errorf("configure notifications: %w", err)
		}
		defer notificationService.Close()
		notificationHandler = notificationService

		browserHandler, err = browser.NewHandler(browser.Options{Store: st, Authorizer: authorizer, Publisher: eventHub})
		if err != nil {
			return fmt.Errorf("configure browser: %w", err)
		}
		dataDir = filepath.Dir(cfg.DatabasePath)
		audioService, err = audio.NewService(audio.Options{Context: serverContext, Store: st, Authorizer: authorizer, Publisher: eventHub, ResourceDir: filepath.Join(dataDir, "plugins", "r2t2")})
		if err != nil {
			return fmt.Errorf("configure audio: %w", err)
		}
		defer func() {
			closeContext, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
			defer cancel()
			if closeErr := audioService.Close(closeContext); closeErr != nil {
				logger.Warn("audio cleanup failed", "error", closeErr)
			}
		}()

		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve kernel controller: %w", err)
		}
		hookResolver = targetfs.NewResolver(targetfs.ResolverOptions{Store: st, HelperAssetsDir: bundledHelperAssetsDir()})
		envResolver = &envtarget.Resolver{Store: st, Remote: hookResolver}
		capabilityService = capabilities.New(envResolver)
		capabilityService.Agents = st
		capabilityService.Logger = logger
		capabilityService.CLITransport = capabilities.TransportProvisioner(listenPort, os.Getenv("ASTRORDER_PUBLIC_URL"), cfg.BrowserSecret)
		capabilitiesHandler = capabilities.NewHandler(capabilityService, authorizer)

		stateDir = dataDir
		if daemonCfg := strings.TrimSpace(os.Getenv("ASTRORDER_SESSION_DAEMON_CONFIG")); daemonCfg != "" {
			stateDir = filepath.Dir(daemonCfg)
		}
		settingsHandler = settingsapi.NewHandler(settingsapi.Options{
			Store: st, Authorizer: authorizer, Port: portNumber, Origins: cfg.AllowedOrigins,
			PublicURL: os.Getenv("ASTRORDER_PUBLIC_URL"), BrowserSecret: cfg.BrowserSecret,
			Kernels:             kernelcontrol.New(cfg.SourcePath, executable),
			AgentInstalledProbe: toolchain.LocalInstalledProbeWithDirs(serverContext, dataDir, stateDir),
			OnPluginsChanged: func() {
				capabilityService.TriggerAutoSync(serverContext)
			},
		})
		toolchainService = toolchain.NewService(toolchain.Options{
			Resolver:           envResolver,
			Store:              st,
			StateDir:           stateDir,
			DataDir:            dataDir,
			DefaultWorkspace:   cfg.DefaultWorkspace,
			OnboardingInjector: &capabilitiesOnboardingInjector{svc: capabilityService},
		})
		defer func() {
			closeContext, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
			defer cancel()
			if err := toolchainService.Close(closeContext); err != nil {
				log.Printf("close toolchain jobs: %v", err)
			}
		}()
		toolchainHandler = toolchain.NewHandler(toolchainService, authorizer)
	}

	officialPluginsDir := resolveOfficialPluginsDir(cfg)
	buildHTTPHandler := func(handlerCtx context.Context, client *daemonclient.Client, nativeSessions interface{ SetNativeSessionRegistrar(func(agentID, nativeID string)) }, groupService *botgroups.Service, modelHandler, connHandler http.Handler) (http.Handler, error) {
		return httpapi.NewHandlerChecked(httpapi.Options{
			Context:            handlerCtx,
			NativeSessions:     nativeSessions,
			Config:             cfg,
			OfficialPluginsDir: officialPluginsDir,
			HostBaseURL:        "http://" + hostAddr,
			Store:            st,
			DaemonClient:     client,
			RemoteExecutable: remoteExecutable,
			EventHub:         eventHub,
			MemoryService:    memoryService,
			BotGroups:        groupService,
			AudioService:     audioService,
			BrowserHandler:   browserHandler,
			ModelGateway:     modelHandler,
			Settings:         settingsHandler,
			Capabilities:     capabilitiesHandler,
			Toolchain:        toolchainHandler,
			Connections:      connHandler,
			Notifications:    notificationHandler,
			DesktopShutdown:  stopServer,
			Logger:           logger,
		})
	}

	// connectDaemon establishes a gRPC connection, waits briefly for projection live-ready,
	// constructs daemon-dependent services + full HTTP handler, and triggers a non-fatal
	// hot-reload if Astrorder-managed portable runtimes are present.
	connectDaemon := func(dialTimeout, readyTimeout time.Duration) (*daemonBinding, http.Handler, error) {
		dialCtx, cancelDial := context.WithTimeout(serverContext, dialTimeout)
		client, dialErr := daemonclient.Dial(dialCtx, daemonclient.Config{
			Target:         cfg.Daemon.Target,
			Credential:     cfg.Daemon.Credential,
			RequestTimeout: dialTimeout,
		})
		cancelDial()
		if dialErr != nil {
			return nil, nil, fmt.Errorf("connect Session Daemon: %w", dialErr)
		}

		eventProjection, projErr := projection.New(client, st, eventHub)
		if projErr != nil {
			_ = client.Close()
			return nil, nil, fmt.Errorf("configure daemon projection: %w", projErr)
		}

		daemonCtx, cancelDaemon := context.WithCancel(serverContext)
		projDone := make(chan struct{})
		projErrCh := make(chan error, 1)
		projReady := make(chan struct{}, 1)
		go func() {
			defer close(projDone)
			projErrCh <- eventProjection.RunReady(daemonCtx, projReady)
		}()

		timer := time.NewTimer(readyTimeout)
		defer timer.Stop()
		select {
		case <-projReady:
		case <-projDone:
			cancelDaemon()
			_ = client.Close()
			runErr := <-projErrCh
			return nil, nil, fmt.Errorf("daemon projection startup: %w", runErr)
		case <-timer.C:
			cancelDaemon()
			_ = client.Close()
			<-projDone
			if lastErr := eventProjection.LastError(); lastErr != nil {
				return nil, nil, fmt.Errorf("daemon projection did not reach live-ready before startup timeout (last error: %w)", lastErr)
			}
			return nil, nil, errors.New("daemon projection did not reach live-ready before startup timeout")
		case <-serverContext.Done():
			cancelDaemon()
			_ = client.Close()
			<-projDone
			return nil, nil, serverContext.Err()
		}

		binding := &daemonBinding{
			client:          client,
			eventProjection: eventProjection,
			projectionDone:  projDone,
			projectionErr:   projErrCh,
			cancel:          cancelDaemon,
		}

		groupFileResolver := targetfs.NewResolver(targetfs.ResolverOptions{Store: st, DefaultWorkspace: cfg.DefaultWorkspace, AllowTempDirs: true, HelperAssetsDir: bundledHelperAssetsDir()})
		groupService, gErr := botgroups.New(daemonCtx, botgroups.Options{
			Store: st,
			Controller: &botgroups.NativeController{
				Store: st, Client: client, Publisher: eventHub,
				DefaultWorkspace: cfg.DefaultWorkspace, AttachmentsDir: cfg.AttachmentsDir,
				MaxAttachmentSize: cfg.MaxAttachmentSize, RemoteExecutable: remoteExecutable, Resolver: groupFileResolver,
			},
			Authorizer: authorizer, Publisher: eventHub, FileResolver: groupFileResolver,
		})
		if gErr != nil {
			binding.close(logger)
			return nil, nil, fmt.Errorf("configure group orchestration: %w", gErr)
		}
		binding.groupService = groupService

		modelHandler, mErr := modelgateway.NewHandler(modelgateway.Options{
			Store: st, Client: client, Authorizer: authorizer, Publisher: eventHub, RemoteExecutable: remoteExecutable,
			OnConfigSaved: func(ctx context.Context, gwCfg modelgateway.GatewayConfig) {
				_ = aiprovider.SyncFromModelGateway(ctx, st, gwCfg)
			},
		})
		if mErr != nil {
			binding.close(logger)
			return nil, nil, fmt.Errorf("configure model gateway: %w", mErr)
		}
		binding.modelHandler = modelHandler

		connHandler, cErr := connections.NewHandler(connections.Options{
			Store: st, Client: client, Authorizer: authorizer, Publisher: eventHub,
			Hooks: connections.NewNativeHookInstaller(hookResolver), RemoteExecutable: remoteExecutable, EnvResolver: envResolver,
		})
		if cErr != nil {
			binding.close(logger)
			return nil, nil, fmt.Errorf("configure connections: %w", cErr)
		}
		binding.connHandler = connHandler

		handlerCtx, handlerCancel := context.WithCancel(daemonCtx)
		binding.handlerCancel = handlerCancel
		fullHandler, hErr := buildHTTPHandler(handlerCtx, client, eventProjection, groupService, modelHandler, connHandler)
		if hErr != nil {
			binding.close(logger)
			return nil, nil, fmt.Errorf("configure http handler: %w", hErr)
		}
		binding.httpHandler = fullHandler

		if toolchainService != nil {
			toolchainService.SetDaemonClient(client)
		}
		connHandler.AutoConnect(daemonCtx)
		triggerManagedRuntimesReloadAsync(daemonCtx, client, dataDir, stateDir, logger)
		return binding, fullHandler, nil
	}

	daemonConfigured := cfg.Daemon.Target != "" || cfg.Daemon.Credential != ""
	if daemonConfigured && st == nil {
		return errors.New("daemon projection requires an App database")
	}

	var activeBinding *daemonBinding
	var degradedCancel context.CancelFunc
	var degradedHandler http.Handler
	var initialHandler http.Handler

	if daemonConfigured {
		dialTimeout := cfg.Daemon.RequestTimeout
		if dialTimeout <= 0 || dialTimeout > daemonInitialDialTimeout {
			dialTimeout = daemonInitialDialTimeout
		}
		readyTimeout := cfg.Daemon.RequestTimeout
		if readyTimeout <= 0 || readyTimeout == 10*time.Second {
			readyTimeout = 8 * time.Second
		}
		binding, fullHandler, connErr := connectDaemon(dialTimeout, readyTimeout)
		if connErr == nil {
			activeBinding = binding
			initialHandler = fullHandler
		} else {
			if serverContext.Err() != nil {
				return serverContext.Err()
			}
			logger.Warn("session daemon unreachable at startup; starting in degraded mode with background reconnect",
				"target", cfg.Daemon.Target,
				"error", connErr,
			)
			degradedCtx, cancelDegraded := context.WithCancel(serverContext)
			degradedCancel = cancelDegraded
			initialHandler, err = buildHTTPHandler(degradedCtx, nil, nil, nil, nil, nil)
			if err != nil {
				cancelDegraded()
				return fmt.Errorf("configure degraded http handler: %w", err)
			}
			degradedHandler = initialHandler
		}
	} else {
		degradedCtx, cancelDegraded := context.WithCancel(serverContext)
		degradedCancel = cancelDegraded
		initialHandler, err = buildHTTPHandler(degradedCtx, nil, nil, nil, nil, nil)
		if err != nil {
			cancelDegraded()
			return fmt.Errorf("configure http handler: %w", err)
		}
		degradedHandler = initialHandler
	}

	rootHandler := newSwappableHandler(initialHandler)

	var daemonMu sync.Mutex
	var managerDone chan struct{}
	if daemonConfigured {
		managerDone = make(chan struct{})
		go func() {
			defer close(managerDone)
			backoff := daemonReconnectMinDelay
			for {
				daemonMu.Lock()
				binding := activeBinding
				daemonMu.Unlock()

				if binding != nil {
					select {
					case <-serverContext.Done():
						daemonMu.Lock()
						toClose := activeBinding
						activeBinding = nil
						daemonMu.Unlock()
						toClose.close(logger)
						return
					case <-binding.projectionDone:
						var projErr error
						select {
						case projErr = <-binding.projectionErr:
						default:
						}
						if serverContext.Err() != nil {
							daemonMu.Lock()
							toClose := activeBinding
							activeBinding = nil
							daemonMu.Unlock()
							toClose.close(logger)
							return
						}
						logger.Warn("session daemon projection disconnected; switching to degraded mode and reconnecting", "error", projErr)
						if toolchainService != nil {
							toolchainService.SetDaemonClient(nil)
						}
						degradedCtx, cancelDegraded := context.WithCancel(serverContext)
						if degHandler, hErr := buildHTTPHandler(degradedCtx, nil, nil, nil, nil, nil); hErr == nil {
							rootHandler.Swap(degHandler)
							daemonMu.Lock()
							prevDegradedCancel := degradedCancel
							prevDegradedHandler := degradedHandler
							degradedCancel = cancelDegraded
							degradedHandler = degHandler
							daemonMu.Unlock()
							if prevDegradedCancel != nil {
								prevDegradedCancel()
							}
							closeHTTPHandler(prevDegradedHandler, logger)
						} else {
							cancelDegraded()
						}
						daemonMu.Lock()
						if activeBinding == binding {
							activeBinding = nil
						}
						daemonMu.Unlock()
						binding.close(logger)
						backoff = daemonReconnectMinDelay
					}
				}

				// Background reconnect loop while in degraded mode.
				select {
				case <-serverContext.Done():
					return
				case <-time.After(backoff):
				}

				dialTimeout := cfg.Daemon.RequestTimeout
				if dialTimeout <= 0 || dialTimeout > daemonInitialDialTimeout {
					dialTimeout = daemonInitialDialTimeout
				}
				readyTimeout := cfg.Daemon.RequestTimeout
				if readyTimeout <= 0 {
					readyTimeout = 8 * time.Second
				}
				nextBinding, fullHandler, connErr := connectDaemon(dialTimeout, readyTimeout)
				if connErr != nil {
					if serverContext.Err() != nil {
						return
					}
					logger.Warn("background session daemon reconnect attempt failed; will retry",
						"target", cfg.Daemon.Target,
						"backoff", backoff.String(),
						"error", connErr,
					)
					backoff *= 2
					if backoff > daemonReconnectMaxDelay {
						backoff = daemonReconnectMaxDelay
					}
					continue
				}

				rootHandler.Swap(fullHandler)
				daemonMu.Lock()
				prevDegradedCancel := degradedCancel
				prevDegradedHandler := degradedHandler
				degradedCancel = nil
				degradedHandler = nil
				activeBinding = nextBinding
				daemonMu.Unlock()
				if prevDegradedCancel != nil {
					prevDegradedCancel()
				}
				closeHTTPHandler(prevDegradedHandler, logger)
				logger.Info("session daemon connected in background; full services activated",
					"daemon_id", nextBinding.client.DaemonID(),
				)
				backoff = daemonReconnectMinDelay
			}
		}()
	}

	defer func() {
		stopServer()
		if managerDone != nil {
			<-managerDone
		}
		daemonMu.Lock()
		toCloseBinding := activeBinding
		activeBinding = nil
		toCancelDegraded := degradedCancel
		degradedCancel = nil
		toCloseDegraded := degradedHandler
		degradedHandler = nil
		daemonMu.Unlock()
		toCloseBinding.close(logger)
		if toCancelDegraded != nil {
			toCancelDegraded()
		}
		closeHTTPHandler(toCloseDegraded, logger)
	}()

	server := &http.Server{
		Handler:           rootHandler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          log.New(&serverErrorWriter{logger: logger}, "", 0),
		BaseContext: func(net.Listener) context.Context {
			return serverContext
		},
	}
	hijacked := newHijackedConnections()
	server.ConnState = hijacked.Observe

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()
	if capabilityService != nil {
		capabilityService.StartAutoSync(serverContext)
	}

	if err := json.NewEncoder(ready).Encode(readyMessage{
		Ready: true, Address: listener.Addr().String(), PID: os.Getpid(),
	}); err != nil {
		stopServer()
		shutdownContext, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
		<-serveResult
		return fmt.Errorf("write ready message: %w", err)
	}

	select {
	case err := <-serveResult:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-serverContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			hijacked.CloseAll()
			_ = server.Close()
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		hijacked.CloseAll()
		err := <-serveResult
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
}

// triggerManagedRuntimesReloadAsync informs a hot-reload-capable Session Daemon
// about any existing Astrorder-managed portable runtime bin directories on connect.
// Against older daemons (ErrReloadUnsupported) or transient errors, it degrades silently.
func triggerManagedRuntimesReloadAsync(ctx context.Context, client *daemonclient.Client, dataDir, stateDir string, logger *slog.Logger) {
	if client == nil || !client.SupportsReloadConfig() {
		return
	}
	runtimesRoot := toolchain.ManagedRuntimesRoot(dataDir, stateDir)
	binDirs := toolchain.ManagedRuntimeBinDirs(runtimesRoot)
	var existing []string
	for _, dir := range binDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			existing = append(existing, dir)
		}
	}
	if len(existing) == 0 {
		return
	}
	go func() {
		reloadCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		probe := true
		res, err := client.ReloadConfigWithOptions(reloadCtx, daemonclient.ReloadConfigOptions{
			ProbePath:     &probe,
			ExtraPathDirs: existing,
		})
		if errors.Is(err, daemonclient.ErrReloadUnsupported) || (res != nil && !res.Supported) {
			logger.Info("session daemon does not support hot config reload; skipping managed runtime sync")
			return
		}
		if err != nil {
			logger.Warn("managed runtime hot reload on daemon connect did not complete", "error", err)
			return
		}
		logger.Info("synced managed portable runtimes with session daemon",
			"extra_path_dirs", existing,
			"added_runtimes", res.AddedRuntimes,
		)
	}()
}

type hijackedConnections struct {
	mu          sync.Mutex
	connections map[net.Conn]struct{}
}

func newHijackedConnections() *hijackedConnections {
	return &hijackedConnections{connections: make(map[net.Conn]struct{})}
}

func (connections *hijackedConnections) Observe(connection net.Conn, state http.ConnState) {
	connections.mu.Lock()
	defer connections.mu.Unlock()
	switch state {
	case http.StateHijacked:
		connections.connections[connection] = struct{}{}
	case http.StateClosed:
		delete(connections.connections, connection)
	}
}

func (connections *hijackedConnections) CloseAll() {
	connections.mu.Lock()
	defer connections.mu.Unlock()
	for connection := range connections.connections {
		_ = connection.Close()
		delete(connections.connections, connection)
	}
}

type serverErrorWriter struct {
	logger *slog.Logger
}

func (w *serverErrorWriter) Write(message []byte) (int, error) {
	w.logger.Error("http server error", "message", string(message))
	return len(message), nil
}

type cachedRemoteExecutableEntry struct {
	path      string
	expiresAt time.Time
}

type cachedRemoteExecutableResolver struct {
	inner connections.RemoteExecutableResolver
	ttl   time.Duration
	mu    sync.Mutex
	cache map[string]cachedRemoteExecutableEntry
}

func newCachedRemoteExecutableResolver(inner connections.RemoteExecutableResolver, ttl time.Duration) *cachedRemoteExecutableResolver {
	return &cachedRemoteExecutableResolver{
		inner: inner,
		ttl:   ttl,
		cache: make(map[string]cachedRemoteExecutableEntry),
	}
}

func (r *cachedRemoteExecutableResolver) Resolve(ctx context.Context, kind string, sshConn map[string]any) (string, error) {
	if r == nil || r.inner == nil {
		return "", errors.New("remote executable resolver is not configured")
	}
	settings, _ := sshConn["settings"].(map[string]any)
	field := func(key string) any {
		if settings != nil && settings[key] != nil && settings[key] != "" {
			return settings[key]
		}
		if sshConn != nil {
			return sshConn[key]
		}
		return nil
	}
	if explicit := field(kind + "_executable"); explicit != nil && explicit != "" {
		return r.inner.Resolve(ctx, kind, sshConn)
	}
	key := fmt.Sprintf("%s\x00%v\x00%v\x00%v\x00%v\x00%v\x00%v",
		kind, field("id"), field("host"), field("port"), field("user"), field("ssh_config_alias"), field("identity_file"))
	now := time.Now()
	if r.ttl > 0 {
		r.mu.Lock()
		entry, ok := r.cache[key]
		r.mu.Unlock()
		if ok && now.Before(entry.expiresAt) {
			return entry.path, nil
		}
	}
	resolved, err := r.inner.Resolve(ctx, kind, sshConn)
	if err != nil || resolved == "" || r.ttl <= 0 {
		return resolved, err
	}
	r.mu.Lock()
	r.cache[key] = cachedRemoteExecutableEntry{path: resolved, expiresAt: time.Now().Add(r.ttl)}
	r.mu.Unlock()
	return resolved, nil
}

type capabilitiesOnboardingInjector struct {
	svc *capabilities.Service
}

func (c *capabilitiesOnboardingInjector) InjectForOnboarding(ctx context.Context, envID, agentID, agentKind string) (bool, []string, []string) {
	if c == nil || c.svc == nil {
		return false, nil, nil
	}
	var warnings []string
	cliReady := false
	if st, err := c.svc.SessionCLI(ctx, agentID, true); err != nil {
		warnings = append(warnings, "注入星序 CLI 提示："+err.Error())
	} else if st != nil {
		cliReady = st.Ready
	}
	var installed []string
	for _, skillName := range []string{"astrorder-sessions", "astrorder-workspace"} {
		res, err := c.svc.Dispatch(ctx, "skill", envID, "astrorder", skillName, []capabilities.DispatchTarget{{TargetID: envID, Agent: agentKind}})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("注入官方技能 %s 提示：%v", skillName, err))
			continue
		}
		if res != nil && res.SuccessCount > 0 {
			installed = append(installed, skillName)
		} else if res != nil && len(res.Reports) > 0 && res.Reports[0].Message != "" {
			warnings = append(warnings, fmt.Sprintf("注入官方技能 %s 提示：%s", skillName, res.Reports[0].Message))
		}
	}
	return cliReady, installed, warnings
}
