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
	"strconv"
	"sync"
	"syscall"
	"time"

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
	"astrorder.dev/app-server/internal/memory"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/projection"
	"astrorder.dev/app-server/internal/settingsapi"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
	"path/filepath"
)

const gracefulShutdownTimeout = 10 * time.Second

// bundledHelperAssetsDir is <bin>/helpers next to the running executable, where
// the per-platform astrorder-helper builds are staged.
func bundledHelperAssetsDir() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "helpers")
}

type readyMessage struct {
	Ready   bool   `json:"ready"`
	Address string `json:"address"`
	PID     int    `json:"pid"`
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

func runServer(ctx context.Context, cfg config.Config, ready io.Writer, logger *slog.Logger) error {
	var st *store.Store
	if cfg.DatabasePath != "" {
		opened, err := store.Open(cfg.DatabasePath)
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		st = opened
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

	var client *daemonclient.Client
	var eventProjection *projection.Projection
	if cfg.Daemon.Target != "" || cfg.Daemon.Credential != "" {
		if cfg.Migration.Upstream != "" {
			return errors.New("daemon event projection cannot run alongside a Python migration upstream")
		}
		if st == nil {
			return errors.New("daemon projection requires an App database")
		}
		client, err = daemonclient.Dial(serverContext, daemonclient.Config{
			Target: cfg.Daemon.Target, Credential: cfg.Daemon.Credential,
			RequestTimeout: cfg.Daemon.RequestTimeout,
		})
		if err != nil {
			return fmt.Errorf("connect Session Daemon: %w", err)
		}
		defer client.Close()
		eventProjection, err = projection.New(client, st, eventHub)
		if err != nil {
			return fmt.Errorf("configure daemon projection: %w", err)
		}
	}
	projectionResult := make(chan error, 1)
	projectionReady := make(chan struct{}, 1)
	if eventProjection != nil {
		projectionContext, cancelProjection := context.WithCancel(serverContext)
		projectionStopped := make(chan struct{})
		go func() {
			defer close(projectionStopped)
			projectionResult <- eventProjection.RunReady(projectionContext, projectionReady)
		}()
		defer func() {
			cancelProjection()
			_ = client.Close()
			<-projectionStopped
		}()
		startupTimeout := cfg.Daemon.RequestTimeout
		if startupTimeout <= 0 {
			startupTimeout = 10 * time.Second
		}
		timer := time.NewTimer(startupTimeout)
		defer timer.Stop()
		select {
		case <-projectionReady:
		case projectionErr := <-projectionResult:
			return fmt.Errorf("daemon projection startup: %w", projectionErr)
		case <-timer.C:
			return errors.New("daemon projection did not reach live-ready before startup timeout")
		case <-serverContext.Done():
			return serverContext.Err()
		}
	}

	var groupService *botgroups.Service
	var audioService *audio.Service
	var browserHandler, modelHandler, connectionHandler, settingsHandler, capabilitiesHandler http.Handler
	if st != nil && client != nil {
		authorizer := auth.New(cfg, &httpapi.StoreOriginLookup{Store: st})
		groupService, err = botgroups.New(serverContext, botgroups.Options{Store: st, Controller: &botgroups.NativeController{Store: st, Client: client, Publisher: eventHub, DefaultWorkspace: cfg.DefaultWorkspace}, Authorizer: authorizer, Publisher: eventHub})
		if err != nil {
			return fmt.Errorf("configure group orchestration: %w", err)
		}
		defer groupService.Close()
		browserHandler, err = browser.NewHandler(browser.Options{Store: st, Authorizer: authorizer, Publisher: eventHub})
		if err != nil {
			return fmt.Errorf("configure browser: %w", err)
		}
		audioService, err = audio.NewService(audio.Options{Context: serverContext, Store: st, Authorizer: authorizer, Publisher: eventHub, ResourceDir: filepath.Join(filepath.Dir(cfg.DatabasePath), "plugins", "r2t2")})
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
		modelHandler, err = modelgateway.NewHandler(modelgateway.Options{Store: st, Client: client, Authorizer: authorizer, Publisher: eventHub})
		if err != nil {
			return fmt.Errorf("configure model gateway: %w", err)
		}
		defer func() {
			if closer, ok := modelHandler.(interface{ Close(context.Context) error }); ok {
				closeContext, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
				defer cancel()
				if closeErr := closer.Close(closeContext); closeErr != nil {
					logger.Warn("model job cleanup failed", "error", closeErr)
				}
			}
		}()
		_, listenPort, _ := net.SplitHostPort(cfg.Listen)
		portNumber, _ := strconv.Atoi(listenPort)
		settingsHandler = settingsapi.NewHandler(settingsapi.Options{
			Store: st, Authorizer: authorizer, Port: portNumber, Origins: cfg.AllowedOrigins,
			PublicURL: os.Getenv("ASTRORDER_PUBLIC_URL"), BrowserSecret: cfg.BrowserSecret,
			RestartDaemon: daemonRestart(cfg.SourcePath),
		})
		hookResolver := targetfs.NewResolver(targetfs.ResolverOptions{Store: st, HelperAssetsDir: bundledHelperAssetsDir()})
		capabilitiesHandler = capabilities.NewHandler(capabilities.New(&envtarget.Resolver{Store: st, Remote: hookResolver}), authorizer)
		connHandler, err := connections.NewHandler(connections.Options{Store: st, Client: client, Authorizer: authorizer, Publisher: eventHub, Hooks: connections.NewNativeHookInstaller(hookResolver)})
		if err != nil {
			return fmt.Errorf("configure connections: %w", err)
		}
		connectionHandler = connHandler
		connHandler.AutoConnect(serverContext)
	}
	var nativeSessions interface {
		SetNativeSessionRegistrar(func(agentID, nativeID string))
	}
	if eventProjection != nil {
		nativeSessions = eventProjection
	}
	handler, err := httpapi.NewHandlerChecked(httpapi.Options{
		NativeSessions:  nativeSessions,
		Config:          cfg,
		Store:           st,
		DaemonClient:    client,
		EventHub:        eventHub,
		MemoryService:   memoryService,
		BotGroups:       groupService,
		AudioService:    audioService,
		BrowserHandler:  browserHandler,
		ModelGateway:    modelHandler,
		Settings:        settingsHandler,
		Capabilities:    capabilitiesHandler,
		Connections:     connectionHandler,
		DesktopShutdown: stopServer,
		Logger:          logger,
	})
	if err != nil {
		return fmt.Errorf("configure http handler: %w", err)
	}
	server := &http.Server{
		Handler:           handler,
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
	case projectionErr := <-projectionResult:
		wasStopping := serverContext.Err() != nil
		stopServer()
		shutdownContext, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		hijacked.CloseAll()
		_ = server.Shutdown(shutdownContext)
		<-serveResult
		if wasStopping && (projectionErr == nil || errors.Is(projectionErr, context.Canceled)) {
			return nil
		}
		if projectionErr == nil {
			return errors.New("daemon event projection stopped unexpectedly")
		}
		return fmt.Errorf("daemon event projection: %w", projectionErr)
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
