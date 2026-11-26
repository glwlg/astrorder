// Package httpapi exposes the P4 Go App foundation.
//
// The construction contract is NewHandler(Options). Options.Config contains
// immutable process configuration. DynamicOrigins is the injected live
// network-preference reader;
// DesktopShutdown requests App shutdown without touching the Session Daemon.
package httpapi

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/agentapi"
	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/attachments"
	"astrorder.dev/app-server/internal/audio"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/backups"
	"astrorder.dev/app-server/internal/botgroups"
	"astrorder.dev/app-server/internal/bots"
	"astrorder.dev/app-server/internal/browser"
	"astrorder.dev/app-server/internal/checkpoints"
	"astrorder.dev/app-server/internal/search"
	"astrorder.dev/app-server/internal/capabilities"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/connector"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/dbxplugin"
	"astrorder.dev/app-server/internal/digest"
	"astrorder.dev/app-server/internal/files"
	"astrorder.dev/app-server/internal/git"
	"astrorder.dev/app-server/internal/gitlabci"
	"astrorder.dev/app-server/internal/integrations"
	"astrorder.dev/app-server/internal/lifecycle"
	"astrorder.dev/app-server/internal/meetings"
	"astrorder.dev/app-server/internal/memory"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/notifications"
	"astrorder.dev/app-server/internal/office"
	"astrorder.dev/app-server/internal/preferences"
	"astrorder.dev/app-server/internal/reviewrelay"
	"astrorder.dev/app-server/internal/sessions"
	"astrorder.dev/app-server/internal/settingsapi"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
	"astrorder.dev/app-server/internal/telemetry"
	"astrorder.dev/app-server/internal/terminal"
	"astrorder.dev/app-server/internal/toolchain"
)

const (
	protocolVersion = 1
	gzipMinimumSize = 1000
	maxAuthBody     = 8192
)

type Kind string

type AuthPolicy string

type Ownership string

const (
	KindHTTP          Kind       = "http"
	KindWebSocket     Kind       = "websocket"
	AuthPublic        AuthPolicy = "public"
	AuthBrowser       AuthPolicy = "browser"
	AuthDesktopBearer AuthPolicy = "desktop_bearer"
	OwnershipGo       Ownership  = "go"
)

// Registration is the package-local static contract for one constructed Go
// endpoint. The same table drives dispatch and introspection.
type Registration struct {
	Kind      Kind
	Method    string
	Path      string
	Auth      AuthPolicy
	Ownership Ownership
}

type routeHandler func(*api, http.ResponseWriter, *http.Request)

type registeredRoute struct {
	Registration
	serve routeHandler
}

var registeredRoutes = []registeredRoute{
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodGet, Path: "/health", Auth: AuthPublic, Ownership: OwnershipGo}, serve: serveHealth},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodGet, Path: "/api/v1/draft-shelf", Auth: AuthBrowser, Ownership: OwnershipGo}, serve: serveDraftShelf},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodPut, Path: "/api/v1/draft-shelf", Auth: AuthBrowser, Ownership: OwnershipGo}, serve: serveDraftShelf},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodPost, Path: "/api/v1/auth/session", Auth: AuthPublic, Ownership: OwnershipGo}, serve: serveAuthSession},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodGet, Path: "/api/v1/auth/session", Auth: AuthPublic, Ownership: OwnershipGo}, serve: serveAuthSession},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodDelete, Path: "/api/v1/auth/session", Auth: AuthPublic, Ownership: OwnershipGo}, serve: serveAuthSession},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodPost, Path: "/api/v1/auth/qr/create", Auth: AuthPublic, Ownership: OwnershipGo}, serve: serveQRLogin},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodPost, Path: "/api/v1/auth/qr/poll", Auth: AuthPublic, Ownership: OwnershipGo}, serve: serveQRLogin},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodPost, Path: "/api/v1/auth/qr/inspect", Auth: AuthBrowser, Ownership: OwnershipGo}, serve: serveQRLogin},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodPost, Path: "/api/v1/auth/qr/approve", Auth: AuthBrowser, Ownership: OwnershipGo}, serve: serveQRLogin},
	{Registration: Registration{Kind: KindHTTP, Method: http.MethodPost, Path: "/_desktop/shutdown", Auth: AuthDesktopBearer, Ownership: OwnershipGo}, serve: serveDesktopShutdown},
}

func Registrations() []Registration {
	registrations := make([]Registration, 0, len(registeredRoutes))
	for _, route := range registeredRoutes {
		registrations = append(registrations, route.Registration)
	}
	return registrations
}

type Options struct {
	Context          context.Context
	Config           config.Config
	Store            *store.Store
	DaemonClient     *daemonclient.Client
	Resolver         *targetfs.Resolver
	RemoteExecutable connections.RemoteExecutableResolver
	EventHub         *application.EventHub
	MemoryService    *memory.Service
	BotGroups        *botgroups.Service
	AudioService     *audio.Service
	BrowserHandler   http.Handler
	ModelGateway     http.Handler
	Settings         http.Handler
	Capabilities     http.Handler
	Toolchain        http.Handler
	Connections      http.Handler
	Notifications    http.Handler
	Sessions         http.Handler
	AgentAPI         http.Handler
	Backups          http.Handler
	Integrations     http.Handler
	DbxPlugin        http.Handler
	Office           http.Handler
	Bots             http.Handler
	GitLab           http.Handler
	Digest           http.Handler
	Meetings         http.Handler
	Telemetry        http.Handler
	DynamicOrigins   auth.DynamicOriginLookup
	DesktopShutdown  func()
	Logger           *slog.Logger
	// NativeSessions receives the callback that lists a Codex session started
	// outside Astrorder when a hook reports activity for it.
	NativeSessions interface {
		SetNativeSessionRegistrar(func(agentID, nativeID string))
	}
}

type api struct {
	draftStore      *store.Store
	qr              *qrLogins
	config          config.Config
	auth            *auth.Authenticator
	desktopShutdown func()
	logger          *slog.Logger
	static          *staticHandler
	eventHub        *application.EventHub
	preferences     http.Handler
	files           http.Handler
	git             http.Handler
	attachments     http.Handler
	memory          http.Handler
	terminal        http.Handler
	connector       http.Handler
	botGroups       http.Handler
	audio           http.Handler
	browser         http.Handler
	modelGateway    http.Handler
	settings        http.Handler
	capabilities    http.Handler
	toolchain       http.Handler
	connections     http.Handler
	notifications   http.Handler
	sessions        http.Handler
	agentAPI        http.Handler
	reviewRelay     http.Handler
	checkpoints     http.Handler
	search          http.Handler
	backups         http.Handler
	integrations    http.Handler
	dbxPlugin       http.Handler
	office          http.Handler
	bots            http.Handler
	gitlab          http.Handler
	digest          http.Handler
	meetings        http.Handler
	telemetry       http.Handler
}

// StoreOriginLookup implements auth.DynamicOriginLookup by querying store.Store for
// dynamic network preferences (network:allowed_origins and network:public_url).
type StoreOriginLookup struct {
	Store *store.Store
}

func (l *StoreOriginLookup) LookupOriginPreferences(ctx context.Context) (auth.OriginPreferences, error) {
	if l == nil || l.Store == nil {
		return auth.OriginPreferences{}, nil
	}
	originsPref, err := l.Store.GetPreference(ctx, "network:allowed_origins")
	if err != nil {
		return auth.OriginPreferences{}, fmt.Errorf("lookup allowed origins: %w", err)
	}
	publicURLPref, err := l.Store.GetPreference(ctx, "network:public_url")
	if err != nil {
		return auth.OriginPreferences{}, fmt.Errorf("lookup public url: %w", err)
	}

	var origins []string
	if originsPref.Exists && originsPref.Value != nil {
		if list, ok := originsPref.Value.([]any); ok {
			for _, item := range list {
				if s, ok := item.(string); ok && s != "" {
					origins = append(origins, s)
				}
			}
		} else if list, ok := originsPref.Value.([]string); ok {
			origins = append(origins, list...)
		}
	}

	publicURL := ""
	if publicURLPref.Exists && publicURLPref.Value != nil {
		if s, ok := publicURLPref.Value.(string); ok {
			publicURL = strings.TrimSpace(s)
		}
	}

	return auth.OriginPreferences{
		AllowedOrigins: origins,
		PublicURL:      publicURL,
	}, nil
}

// NewHandlerChecked constructs the application handler with explicit validation of
// required dependencies, failing instead of silently swallowing initialization errors.
func NewHandlerChecked(options Options) (http.Handler, error) {
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	dynamicOrigins := options.DynamicOrigins
	if dynamicOrigins == nil && options.Store != nil {
		dynamicOrigins = &StoreOriginLookup{Store: options.Store}
	}

	authenticator := auth.New(options.Config, dynamicOrigins)

	resolver := options.Resolver
	if resolver == nil {
		defaultWS := options.Config.DefaultWorkspace
		if defaultWS == "" {
			defaultWS, _ = os.Getwd()
		}
		resolver = targetfs.NewResolver(targetfs.ResolverOptions{
			Store:            options.Store,
			DefaultWorkspace: defaultWS,
			AllowTempDirs:    true,
			HelperAssetsDir:  bundledHelperAssets(),
			LocalStageRoots:  []string{options.Config.StagingDir},
		})
	}

	eventHub := options.EventHub
	if eventHub == nil && options.Store != nil {
		eventHub = application.NewEventHub(options.Store)
	}

	var prefHandler http.Handler
	if options.Store != nil {
		prefHandler = preferences.NewHandler(preferences.Options{
			Store:      options.Store,
			Authorizer: authenticator,
		})
	}

	filesHandler := files.NewHandler(files.Options{
		Authorizer:  authenticator,
		Resolver:    resolver,
		Publisher:   eventHub,
		Opener:      files.NativeOpener{},
		Clipboard:   files.NativeClipboard{},
		StagingRoot: options.Config.StagingDir,
	})

	gitHandler := git.NewHandler(git.Options{
		Authorizer: authenticator,
		Resolver:   resolver,
		Publisher:  eventHub,
	})

	var attachHandler http.Handler
	allowedAttachmentTypes := options.Config.AllowedAttachmentTypes
	if len(allowedAttachmentTypes) == 0 {
		allowedAttachmentTypes = config.DefaultAttachmentTypes()
	}
	maxAttachmentSize := options.Config.MaxAttachmentSize
	if maxAttachmentSize <= 0 {
		maxAttachmentSize = config.DefaultMaxAttachmentSize
	}
	if options.Store != nil && options.Config.AttachmentsDir != "" {
		h, err := attachments.NewHandler(attachments.Options{
			Authorizer:   authenticator,
			Store:        options.Store,
			Publisher:    eventHub,
			Root:         options.Config.AttachmentsDir,
			AllowedTypes: allowedAttachmentTypes,
			MaxSize:      maxAttachmentSize,
		})
		if err != nil {
			return nil, fmt.Errorf("configure attachments handler: %w", err)
		}
		attachHandler = h
	}

	var memHandler http.Handler
	if options.MemoryService != nil {
		memHandler = options.MemoryService.Handler()
	}
	sessionHandler := options.Sessions
	agentHandler := options.AgentAPI
	if eventHub != nil {
		if closer, ok := options.BrowserHandler.(interface{ CloseSession(context.Context, string) }); ok {
			eventHub.SetSessionCleanup(func(ctx context.Context, aid, sid string) { closer.CloseSession(ctx, aid+"::"+sid) })
		}
	}
	if options.Store != nil {
		if sessionHandler == nil {
			var err error
			sessionHandler, err = sessions.NewHandler(sessions.Options{Store: options.Store, Client: options.DaemonClient, Authorizer: authenticator, Publisher: eventHub, DefaultWorkspace: options.Config.DefaultWorkspace, AttachmentsDir: options.Config.AttachmentsDir, MaxAttachmentSize: maxAttachmentSize, Resolver: resolver, RemoteExecutable: options.RemoteExecutable})
			if err != nil {
				return nil, fmt.Errorf("configure session handler: %w", err)
			}
			if registrar, ok := sessionHandler.(interface {
				RegisterNativeSession(agentID, nativeID string)
			}); ok && options.NativeSessions != nil {
				options.NativeSessions.SetNativeSessionRegistrar(registrar.RegisterNativeSession)
			}
			if sleeper, ok := sessionHandler.(interface{ RunIdleSleep(context.Context) }); ok && options.Context != nil && options.DaemonClient != nil {
				go sleeper.RunIdleSleep(options.Context)
			}
			if runner, ok := sessionHandler.(interface{ RunQueue(context.Context) }); ok && options.Context != nil && options.DaemonClient != nil {
				go runner.RunQueue(options.Context)
			}
			if scheduler, ok := sessionHandler.(interface{ RunSchedules(context.Context) }); ok && options.Context != nil && options.DaemonClient != nil {
				go scheduler.RunSchedules(options.Context)
			}
			if refresher, ok := sessionHandler.(activityRefresher); ok && options.DaemonClient != nil {
				go reconcileActivityOnStart(refresher, options.Logger)
			}
		}
		if recovery, ok := sessionHandler.(interface {
			RecoverDisconnectedSessions(context.Context, string, string) error
		}); ok && options.Context != nil {
			if connections, ok := options.Connections.(interface {
				SetSessionRecovery(context.Context, func(context.Context, string, string) error)
			}); ok {
				connections.SetSessionRecovery(options.Context, recovery.RecoverDisconnectedSessions)
			}
		}
		if agentHandler == nil {
			var commandSubmit func(context.Context, map[string]any) (map[string]any, error)
			if submitter, ok := sessionHandler.(interface {
				Submit(context.Context, map[string]any) (map[string]any, error)
			}); ok {
				commandSubmit = submitter.Submit
			}
			var wakeScheduler func()
			if waker, ok := sessionHandler.(interface{ WakeSchedules() }); ok {
				wakeScheduler = waker.WakeSchedules
			}
			var browserInvoke func(context.Context, string, map[string]any) (any, error)
			if invoker, ok := options.BrowserHandler.(interface {
				Invoke(context.Context, string, map[string]any) (any, error)
			}); ok {
				browserInvoke = invoker.Invoke
			}
			var err error
			agentHandler, err = agentapi.NewHandler(agentapi.Options{Store: options.Store, Client: options.DaemonClient, Authorizer: authenticator, Publisher: eventHub, Logger: logger, DefaultWorkspace: options.Config.DefaultWorkspace, FileResolver: resolver, RemoteExecutable: options.RemoteExecutable, BrowserInvoke: browserInvoke, CommandSubmit: commandSubmit, WakeScheduler: wakeScheduler})
			if err != nil {
				return nil, fmt.Errorf("configure agent handler: %w", err)
			}
		}
	}

	var reviewRelayHandler http.Handler
	var checkpointsHandler http.Handler
	var searchHandler http.Handler
	if options.Store != nil {
		_, _ = options.Store.HealSleptErrorSessions(context.Background())
		reviewRelayHandler = reviewrelay.NewHandler(reviewrelay.Options{
			Store:      options.Store,
			Authorizer: authenticator,
			Resolver:   resolver,
		})
		checkpointsHandler = checkpoints.NewService(checkpoints.Options{
			Store:      options.Store,
			Authorizer: authenticator,
			Resolver:   resolver,
			Publisher:  eventHub,
			Subscriber: eventHub,
		})
		searchHandler = search.NewHandler(search.Options{
			Store:      options.Store,
			Authorizer: authenticator,
		})
	}

	backupsHandler := options.Backups
	if backupsHandler == nil && options.Store != nil {
		agentRestarter, _ := options.Connections.(backups.AgentRestarter)
		defaultDestDir := ""
		if options.Config.DatabasePath != "" {
			if absDB, absErr := filepath.Abs(options.Config.DatabasePath); absErr == nil {
				defaultDestDir = filepath.Join(filepath.Dir(absDB), "backups")
			}
		}
		if defaultDestDir == "" {
			if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
				defaultDestDir = filepath.Join(home, "AstrorderBackups")
			}
		}
		backupSvc := backups.NewService(backups.Options{
			Store:                 options.Store,
			Authorizer:            authenticator,
			Resolver:              resolver,
			Opener:                files.NativeOpener{},
			AgentRestarter:        agentRestarter,
			DefaultWorkspace:      options.Config.DefaultWorkspace,
			DefaultDestinationDir: defaultDestDir,
			Logger:                logger,
		})
		if options.Context != nil {
			go backupSvc.RunScheduler(options.Context)
		}
		backupsHandler = backupSvc
	}

	integrationsHandler := options.Integrations
	if integrationsHandler == nil && options.Store != nil {
		integrationsHandler = integrations.NewHandler(integrations.Options{
			Store:      options.Store,
			Authorizer: authenticator,
		})
	}

	dbxAuditDir := ""
	if options.Config.DatabasePath != "" {
		if absDB, absErr := filepath.Abs(options.Config.DatabasePath); absErr == nil {
			dbxAuditDir = filepath.Dir(absDB)
		}
	}

	dbxHandler := options.DbxPlugin
	if dbxHandler == nil {
		dbxHandler = dbxplugin.NewHandler(dbxplugin.Options{
			Store:         options.Store,
			Authorizer:    authenticator,
			BrowserSecret: options.Config.BrowserSecret,
			AuditDir:      dbxAuditDir,
			Logger:        logger,
		})
	}

	officeHandler := options.Office
	if officeHandler == nil {
		officeHandler = office.NewHandler(office.Options{
			Store:      options.Store,
			Authorizer: authenticator,
			Resolver:   resolver,
		})
	}

	meetingsHandler := options.Meetings
	if meetingsHandler == nil {
		meetingsHandler = meetings.NewHandler(meetings.Options{
			Store:      options.Store,
			Authorizer: authenticator,
			Logger:     logger,
		})
	}

	digestHandler := options.Digest
	var historyReader digest.SessionHistoryReader
	if reader, ok := sessionHandler.(interface {
		ReadRecentMessages(context.Context, string, string, int) ([]map[string]any, error)
	}); ok {
		historyReader = reader.ReadRecentMessages
	}
	if digestHandler == nil {
		digestHandler = digest.NewHandler(digest.Options{
			Store:         options.Store,
			Authorizer:    authenticator,
			AuditDir:      dbxAuditDir,
			HistoryReader: historyReader,
			Logger:        logger,
		})
	}

	botsHandler := options.Bots
	if botsHandler == nil && options.Store != nil {
		var spawner bots.SessionSpawner
		if sp, ok := agentHandler.(interface {
			SpawnForBot(context.Context, map[string]any) (map[string]any, error)
		}); ok {
			spawner = func(ctx context.Context, req bots.SpawnRequest) (*bots.SpawnResult, error) {
				payload := map[string]any{
					"operation_id":  req.OperationID,
					"agent_id":      req.AgentID,
					"title":         req.Title,
					"workspace":     req.Workspace,
					"model":         req.Model,
					"provider":      req.Provider,
					"effort":        req.Effort,
					"approval_mode": req.ApprovalMode,
					"prompt":        req.Prompt,
				}
				if req.Caller != nil {
					payload["caller"] = req.Caller
				}
				res, err := sp.SpawnForBot(ctx, payload)
				if err != nil {
					return nil, err
				}
				out := &bots.SpawnResult{}
				if sess, ok := res["session"].(map[string]any); ok {
					out.Session = sess
				}
				if receipt, ok := res["receipt"].(map[string]any); ok {
					out.Receipt = receipt
				}
				if opID, ok := res["operation_id"].(string); ok {
					out.OperationID = opID
				}
				if stage, ok := res["stage"].(string); ok {
					out.Stage = stage
				}
				return out, nil
			}
		}
		var botHistory bots.SessionHistoryReader
		if historyReader != nil {
			botHistory = bots.SessionHistoryReader(historyReader)
		}
		botSvc := bots.NewService(bots.Options{
			Store:         options.Store,
			Authorizer:    authenticator,
			Resolver:      resolver,
			HistoryReader: botHistory,
			Spawner:       spawner,
		})
		if binder, ok := agentHandler.(interface {
			SetBotCallbacks(func(context.Context) (any, error), func(context.Context, map[string]any) (any, error))
		}); ok {
			binder.SetBotCallbacks(
				func(ctx context.Context) (any, error) {
					return botSvc.ListForAgentAPI(ctx)
				},
				func(ctx context.Context, payload map[string]any) (any, error) {
					req := bots.InvokeRequest{}
					req.Bot, _ = payload["bot"].(string)
					req.OperationID, _ = payload["operation_id"].(string)
					req.Title, _ = payload["title"].(string)
					req.Prompt, _ = payload["prompt"].(string)
					req.CallerSessionKey, _ = payload["caller_session_key"].(string)
					req.CallerWorkspace, _ = payload["caller_workspace"].(string)
					req.CallerConnectionID, _ = payload["caller_connection_id"].(string)
					req.AgentID, _ = payload["agent_id"].(string)
					req.Model, _ = payload["model"].(string)
					req.Provider, _ = payload["provider"].(string)
					req.Effort, _ = payload["effort"].(string)
					req.ApprovalMode, _ = payload["approval_mode"].(string)
					if nc, ok := payload["notify_caller"].(bool); ok {
						req.NotifyCaller = &nc
					}
					if caller, ok := payload["caller"].(map[string]any); ok {
						req.Caller = caller
					}
					switch rawAttach := payload["attach_paths"].(type) {
					case []any:
						for _, item := range rawAttach {
							if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
								req.AttachPaths = append(req.AttachPaths, strings.TrimSpace(s))
							}
						}
					case []string:
						for _, s := range rawAttach {
							if strings.TrimSpace(s) != "" {
								req.AttachPaths = append(req.AttachPaths, strings.TrimSpace(s))
							}
						}
					case string:
						for _, part := range strings.Split(rawAttach, ",") {
							if strings.TrimSpace(part) != "" {
								req.AttachPaths = append(req.AttachPaths, strings.TrimSpace(part))
							}
						}
					}
					return botSvc.Invoke(ctx, req)
				},
			)
		}
		botsHandler = botSvc
	}

	gitlabHandler := options.GitLab
	if gitlabHandler == nil && options.Store != nil {
		gitlabSvc := gitlabci.NewService(gitlabci.Options{
			Store:      options.Store,
			Authorizer: authenticator,
			Resolver:   resolver,
		})
		bindGitLabCallbacks(agentHandler, gitlabSvc)
		gitlabHandler = gitlabSvc
	}

	telemetryHandler := options.Telemetry
	if telemetryHandler == nil {
		telemetryDir := ""
		if dbxAuditDir != "" {
			telemetryDir = filepath.Join(dbxAuditDir, "telemetry")
		}
		telemetryHandler = telemetry.NewHandler(telemetry.Options{
			Authorizer:   authenticator,
			TelemetryDir: telemetryDir,
			Logger:       logger,
		})
	}

	app := &api{
		draftStore:      options.Store,
		qr:              newQRLogins(),
		config:          options.Config,
		auth:            authenticator,
		desktopShutdown: options.DesktopShutdown,
		logger:          logger,
		eventHub:        eventHub,
		preferences:     prefHandler,
		files:           filesHandler,
		git:             gitHandler,
		attachments:     attachHandler,
		memory:          memHandler,
		botGroups:       options.BotGroups,
		browser:         options.BrowserHandler,
		modelGateway:    options.ModelGateway,
		settings:        options.Settings,
		capabilities:    options.Capabilities,
		toolchain:       options.Toolchain,
		connections:     options.Connections,
		notifications:   options.Notifications,
		sessions:        sessionHandler,
		agentAPI:        agentHandler,
		reviewRelay:     reviewRelayHandler,
		checkpoints:     checkpointsHandler,
		search:          searchHandler,
		backups:         backupsHandler,
		integrations:    integrationsHandler,
		dbxPlugin:       dbxHandler,
		office:          officeHandler,
		bots:            botsHandler,
		gitlab:          gitlabHandler,
		digest:          digestHandler,
		meetings:        meetingsHandler,
		telemetry:       telemetryHandler,
	}
	if options.BotGroups == nil {
		app.botGroups = nil
	}
	if options.AudioService != nil {
		app.audio = options.AudioService
	}
	if options.DaemonClient != nil {
		app.terminal = &terminal.Relay{Client: options.DaemonClient, Store: options.Store,
			Authorizer: authenticator, DefaultWorkspace: options.Config.DefaultWorkspace}
		var invoke connector.InvokeFunc
		if gateway, ok := agentHandler.(interface {
			Invoke(context.Context, string, map[string]any) (any, error)
		}); ok {
			invoke = gateway.Invoke
		}
		app.connector = &connector.Relay{Client: options.DaemonClient, Store: options.Store, Authorizer: authenticator, Invoke: invoke}
	}
	if options.Config.StaticDir != "" {
		if err := lifecycle.ValidateStaticDir(options.Config.StaticDir); err != nil {
			return nil, fmt.Errorf("invalid static directory: %w", err)
		}
		app.static = newStaticHandler(options.Config.StaticDir)
		if app.static == nil {
			return nil, fmt.Errorf("invalid static directory: %s", options.Config.StaticDir)
		}
	}
	return recoverPanics(logger, accessLog(logger, cors(app.auth, gzipResponses(app)))), nil
}

func NewHandler(options Options) http.Handler {
	handler, err := NewHandlerChecked(options)
	if err != nil {
		if options.Logger != nil {
			options.Logger.Error("failed to construct handler", "error", err)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSONError(w, http.StatusInternalServerError, "Handler initialization failed: "+err.Error())
		})
	}
	return handler
}

func (a *api) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	pathKnown := false
	allowed := make([]string, 0, 3)
	for _, route := range registeredRoutes {
		if route.Path != request.URL.Path {
			continue
		}
		pathKnown = true
		allowed = append(allowed, route.Method)
		if route.Method == request.Method {
			route.serve(a, response, request)
			return
		}
	}
	if pathKnown {
		methodNotAllowed(response, allowed...)
		return
	}

	// Product domain handlers mounted BEFORE migration proxy:
	if a.notifications != nil && notifications.Matches(request.Method, request.URL.Path) {
		a.notifications.ServeHTTP(response, request)
		return
	}
	if a.reviewRelay != nil && reviewrelay.Matches(request.Method, request.URL.Path) {
		a.reviewRelay.ServeHTTP(response, request)
		return
	}
	if a.checkpoints != nil && checkpoints.Matches(request.Method, request.URL.Path) {
		a.checkpoints.ServeHTTP(response, request)
		return
	}
	if a.search != nil && search.Matches(request.Method, request.URL.Path) {
		a.search.ServeHTTP(response, request)
		return
	}
	if a.backups != nil && backups.Matches(request.Method, request.URL.Path) {
		a.backups.ServeHTTP(response, request)
		return
	}
	if a.integrations != nil && integrations.Matches(request.Method, request.URL.Path) {
		a.integrations.ServeHTTP(response, request)
		return
	}
	if a.dbxPlugin != nil && dbxplugin.Matches(request.Method, request.URL.Path) {
		a.dbxPlugin.ServeHTTP(response, request)
		return
	}
	if a.office != nil && office.Matches(request.Method, request.URL.Path) {
		a.office.ServeHTTP(response, request)
		return
	}
	if a.bots != nil && bots.Matches(request.Method, request.URL.Path) {
		a.bots.ServeHTTP(response, request)
		return
	}
	if a.gitlab != nil && gitlabci.Matches(request.Method, request.URL.Path) {
		a.gitlab.ServeHTTP(response, request)
		return
	}
	if a.digest != nil && digest.Matches(request.Method, request.URL.Path) {
		a.digest.ServeHTTP(response, request)
		return
	}
	if a.meetings != nil && meetings.Matches(request.Method, request.URL.Path) {
		a.meetings.ServeHTTP(response, request)
		return
	}
	if a.telemetry != nil && telemetry.Matches(request.Method, request.URL.Path) {
		a.telemetry.ServeHTTP(response, request)
		return
	}
	if a.sessions != nil && sessions.Matches(request.Method, request.URL.Path) {
		a.sessions.ServeHTTP(response, request)
		return
	}
	if a.agentAPI != nil && agentapi.Matches(request.Method, request.URL.Path) {
		a.agentAPI.ServeHTTP(response, request)
		return
	}
	if a.botGroups != nil && botgroups.Matches(request.Method, request.URL.Path) {
		a.botGroups.ServeHTTP(response, request)
		return
	}
	if a.browser != nil && browser.Matches(request.Method, request.URL.Path) {
		a.browser.ServeHTTP(response, request)
		return
	}
	if a.audio != nil && audio.Matches(request.Method, request.URL.Path) {
		a.audio.ServeHTTP(response, request)
		return
	}
	if a.modelGateway != nil && modelgateway.Matches(request.Method, request.URL.Path) {
		a.modelGateway.ServeHTTP(response, request)
		return
	}
	if a.capabilities != nil && capabilities.Matches(request.Method, request.URL.Path) {
		a.capabilities.ServeHTTP(response, request)
		return
	}
	if a.toolchain != nil && toolchain.Matches(request.Method, request.URL.Path) {
		a.toolchain.ServeHTTP(response, request)
		return
	}
	if a.settings != nil && settingsapi.Matches(request.Method, request.URL.Path) {
		a.settings.ServeHTTP(response, request)
		return
	}
	if a.connections != nil && connections.Matches(request.Method, request.URL.Path) {
		a.connections.ServeHTTP(response, request)
		return
	}
	// Preferences
	if a.preferences != nil && (request.URL.Path == "/api/v1/preferences" || request.URL.Path == "/api/v1/preferences/import") {
		a.preferences.ServeHTTP(response, request)
		return
	}

	// Files
	if a.files != nil && isFilesRoute(request.URL.Path) {
		a.files.ServeHTTP(response, request)
		return
	}

	// Git
	if a.git != nil && isGitRoute(request.URL.Path) {
		a.git.ServeHTTP(response, request)
		return
	}

	// Attachments
	if a.attachments != nil && isAttachmentsRoute(request.URL.Path) {
		a.attachments.ServeHTTP(response, request)
		return
	}

	// WebSocket Events
	if a.connector != nil && request.URL.Path == "/ws/v1/connector" {
		a.connector.ServeHTTP(response, request)
		return
	}
	if a.terminal != nil && request.URL.Path == "/ws/v1/terminal" {
		a.terminal.ServeHTTP(response, request)
		return
	}
	if a.eventHub != nil && request.URL.Path == "/ws/v1/events" {
		a.eventHub.ServeWebSocket(response, request, a.auth)
		return
	}

	// Memory
	if a.memory != nil && isMemoryRoute(request.URL.Path) {
		if failure := a.auth.AuthorizeBrowser(request); failure != nil {
			writeAuthError(response, failure)
			return
		}
		a.memory.ServeHTTP(response, request)
		return
	}

	if isReservedPath(request.URL.Path) {
		writeJSONError(response, http.StatusNotFound, "Not Found")
		return
	}
	if a.static != nil && (request.Method == http.MethodGet || request.Method == http.MethodHead) {
		if a.static.Serve(response, request) {
			return
		}
	}
	writeJSONError(response, http.StatusNotFound, "Not Found")
}

func serveHealth(_ *api, response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{
		"status": "ok", "service": "astrorder", "protocol_version": protocolVersion,
	})
}

func serveAuthSession(app *api, response http.ResponseWriter, request *http.Request) {
	app.serveAuthSession(response, request)
}

func serveDesktopShutdown(app *api, response http.ResponseWriter, request *http.Request) {
	app.serveDesktopShutdown(response, request)
}

func (a *api) serveAuthSession(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodPost:
		if a.config.BrowserSecret == "" {
			writeJSONError(response, http.StatusServiceUnavailable, "Authentication is not configured")
			return
		}
		if !a.auth.IsAllowedOrigin(request.Context(), request.Header.Get("Origin"), request.Host) {
			writeJSONError(response, http.StatusForbidden, "Origin is not allowed")
			return
		}
		var payload struct {
			Token string `json:"token"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, maxAuthBody))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil || payload.Token == "" || len(payload.Token) > 4096 || decodeHasTrailingValue(decoder) {
			writeJSONError(response, http.StatusUnprocessableEntity, "Invalid request")
			return
		}
		if !constantTimeEqual(payload.Token, a.config.BrowserSecret) {
			writeJSONError(response, http.StatusUnauthorized, "Invalid authentication token")
			return
		}
		setBrowserCookie(response, payload.Token)
		writeJSON(response, http.StatusOK, map[string]bool{"authenticated": true})
	case http.MethodGet:
		authenticated := a.auth.IsBrowserAuthenticated(request)
		if a.auth.IsBrowserBearer(request) {
			setBrowserCookie(response, a.config.BrowserSecret)
			authenticated = true
		}
		writeJSON(response, http.StatusOK, map[string]bool{"authenticated": authenticated})
	case http.MethodDelete:
		if !a.auth.IsAllowedOrigin(request.Context(), request.Header.Get("Origin"), request.Host) {
			writeJSONError(response, http.StatusForbidden, "Origin is not allowed")
			return
		}
		deleteBrowserCookie(response)
		response.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(response, http.MethodPost, http.MethodGet, http.MethodDelete)
	}
}

func (a *api) serveDesktopShutdown(response http.ResponseWriter, request *http.Request) {
	if a.desktopShutdown == nil {
		writeJSONError(response, http.StatusNotFound, "Not Found")
		return
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() || !a.auth.IsBrowserBearer(request) {
		writeJSONError(response, http.StatusForbidden, "Desktop shutdown is not allowed")
		return
	}
	a.desktopShutdown()
	writeJSON(response, http.StatusOK, map[string]bool{"stopping": true})
}

func isFilesRoute(p string) bool {
	switch p {
	case "/api/v1/files/tree",
		"/api/v1/files/search",
		"/api/v1/files/stage",
		"/api/v1/files/mkdir",
		"/api/v1/files/rename",
		"/api/v1/files/delete",
		"/api/v1/files/upload",
		"/api/v1/files/exists",
		"/api/v1/files/raw",
		"/api/v1/system/open-file",
		"/api/v1/system/write-file":
		return true
	default:
		return false
	}
}

func isGitRoute(p string) bool {
	for _, reg := range git.Registrations() {
		if reg.Path == p {
			return true
		}
	}
	return false
}

func isAttachmentsRoute(p string) bool {
	if p == "/api/v1/attachments" {
		return true
	}
	if strings.HasPrefix(p, "/api/v1/attachments/") {
		sub := strings.TrimPrefix(p, "/api/v1/attachments/")
		return sub != "" && !strings.Contains(sub, "/")
	}
	return false
}

func isMemoryRoute(p string) bool {
	switch p {
	case "/api/v1/memory/config",
		"/api/v1/memory/health",
		"/api/v1/memory/ls",
		"/api/v1/memory/tree",
		"/api/v1/memory/content",
		"/api/v1/memory/search",
		"/api/v1/memory/targets",
		"/api/v1/memory/apply":
		return true
	default:
		if strings.HasPrefix(p, "/api/v1/memory/targets/") && strings.HasSuffix(p, "/status") {
			return true
		}
		return false
	}
}

func decodeHasTrailingValue(decoder *json.Decoder) bool {
	var trailing any
	return decoder.Decode(&trailing) != io.EOF
}

func constantTimeEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func setBrowserCookie(response http.ResponseWriter, token string) {
	http.SetCookie(response, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 30,
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
}

func deleteBrowserCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name:     auth.CookieName,
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func isReservedPath(requestPath string) bool {
	return requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") ||
		requestPath == "/ws" || strings.HasPrefix(requestPath, "/ws/") ||
		requestPath == "/health"
}

type staticHandler struct {
	root      string
	indexPath string
}

func newStaticHandler(root string) *staticHandler {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	resolvedRoot, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil
	}
	indexPath := filepath.Join(resolvedRoot, "index.html")
	resolvedIndex, info, ok := confinedRegularFile(resolvedRoot, indexPath)
	if !ok {
		return nil
	}
	_ = info
	// A symlinked static root is allowed, but all candidates are confined to its
	// resolved target so links and junctions below it cannot escape.
	return &staticHandler{root: resolvedRoot, indexPath: resolvedIndex}
}

func (s *staticHandler) Serve(response http.ResponseWriter, request *http.Request) bool {
	requestPath := strings.ReplaceAll(request.URL.Path, `\`, "/")
	clean := path.Clean("/" + requestPath)
	relative := strings.TrimPrefix(clean, "/")
	candidate := filepath.Join(s.root, filepath.FromSlash(relative))
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		if !strings.HasSuffix(request.URL.Path, "/") {
			redirectPath := request.URL.Path + "/"
			if request.URL.RawQuery != "" {
				redirectPath += "?" + request.URL.RawQuery
			}
			http.Redirect(response, request, redirectPath, http.StatusTemporaryRedirect)
			return true
		}
		candidate = filepath.Join(candidate, "index.html")
	}
	if resolved, info, ok := confinedRegularFile(s.root, candidate); ok {
		serveStaticFile(response, request, resolved, info)
		return true
	}
	if custom404, info, ok := confinedRegularFile(s.root, filepath.Join(s.root, "404.html")); ok {
		setStaticCache(response.Header(), request.URL.Path, filepath.Base(custom404))
		serveStaticFileStatus(response, request, custom404, info, http.StatusNotFound)
		return true
	}
	if resolved, info, ok := confinedRegularFile(s.root, s.indexPath); ok {
		// Starlette's fallback returns the index response before the cache policy
		// for the originally requested path is applied.
		serveStaticFileStatus(response, request, resolved, info, http.StatusOK)
		return true
	}
	return false
}

func serveStaticFile(response http.ResponseWriter, request *http.Request, filename string, info os.FileInfo) {
	name := filepath.Base(filename)
	setStaticCache(response.Header(), request.URL.Path, name)
	serveStaticFileStatus(response, request, filename, info, http.StatusOK)
}

func serveStaticFileStatus(response http.ResponseWriter, request *http.Request, filename string, info os.FileInfo, status int) {
	name := filepath.Base(filename)
	if response.Header().Get("Content-Type") == "" {
		response.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(filename)))
	}
	file, err := os.Open(filename)
	if err != nil {
		writeJSONError(response, http.StatusNotFound, "Not Found")
		return
	}
	defer file.Close()
	if status != http.StatusOK {
		response.WriteHeader(status)
		if request.Method != http.MethodHead {
			_, _ = io.Copy(response, file)
		}
		return
	}
	http.ServeContent(response, request, name, info.ModTime(), file)
}

func setStaticCache(header http.Header, requestPath, name string) {
	lowerName := strings.ToLower(name)
	if lowerName == "index.html" || lowerName == "sw.js" || lowerName == "sw-astrorder.js" || lowerName == "registersw.js" || strings.HasPrefix(lowerName, "workbox-") || strings.HasPrefix(lowerName, "sw-") {
		header.Set("Cache-Control", "no-cache")
		return
	}
	lowerPath := strings.ToLower(requestPath)
	if strings.HasPrefix(lowerPath, "/assets/") || hasStaticExtension(lowerPath) {
		header.Set("Cache-Control", "public, max-age=2592000, immutable")
	}
}

func hasStaticExtension(value string) bool {
	for _, extension := range []string{".js", ".css", ".png", ".ico", ".svg"} {
		if strings.HasSuffix(value, extension) {
			return true
		}
	}
	return false
}

func confinedRegularFile(root, candidate string) (string, os.FileInfo, bool) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || !isWithin(resolved, root) {
		return "", nil, false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, false
	}
	return resolved, info, true
}

func isWithin(filename, root string) bool {
	relative, err := filepath.Rel(root, filename)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func cors(authenticator *auth.Authenticator, next http.Handler) http.Handler {
	const (
		allowMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
		allowHeaders = "Accept, Accept-Language, Authorization, Content-Language, Content-Type"
	)
	allowedMethods := map[string]struct{}{
		http.MethodGet: {}, http.MethodPost: {}, http.MethodPut: {},
		http.MethodPatch: {}, http.MethodDelete: {}, http.MethodOptions: {},
	}
	allowedHeaders := map[string]struct{}{
		"accept": {}, "accept-language": {}, "authorization": {},
		"content-language": {}, "content-type": {},
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		allowed := origin != "" && authenticator.IsCORSOrigin(request.Context(), origin, request.Host)
		if allowed {
			response.Header().Set("Access-Control-Allow-Origin", origin)
			response.Header().Set("Access-Control-Allow-Credentials", "true")
			appendVary(response.Header(), "Origin")
		}
		if request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != "" {
			response.Header().Set("Access-Control-Allow-Methods", allowMethods)
			response.Header().Set("Access-Control-Allow-Headers", allowHeaders)
			response.Header().Set("Access-Control-Max-Age", "600")
			failures := make([]string, 0, 3)
			if !allowed {
				failures = append(failures, "origin")
			}
			if _, ok := allowedMethods[request.Header.Get("Access-Control-Request-Method")]; !ok {
				failures = append(failures, "method")
			}
			for _, requested := range strings.Split(request.Header.Get("Access-Control-Request-Headers"), ",") {
				if requested = strings.ToLower(strings.TrimSpace(requested)); requested == "" {
					continue
				} else if _, ok := allowedHeaders[requested]; !ok {
					failures = append(failures, "headers")
					break
				}
			}
			response.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if len(failures) > 0 {
				response.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(response, "Disallowed CORS "+strings.Join(failures, ", "))
				return
			}
			response.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(response, "OK")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func gzipResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead || !strings.Contains(request.Header.Get("Accept-Encoding"), "gzip") || isWebSocketRequest(request) {
			next.ServeHTTP(response, request)
			return
		}
		writer := newGzipResponseWriter(response)
		next.ServeHTTP(writer, request)
		_ = writer.Close()
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	status      int
	buffer      bytes.Buffer
	gzip        *gzip.Writer
	committed   bool
	passthrough bool
}

func newGzipResponseWriter(response http.ResponseWriter) *gzipResponseWriter {
	return &gzipResponseWriter{ResponseWriter: response, status: http.StatusOK}
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.committed {
		return
	}
	w.status = status
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	if w.committed {
		if w.gzip != nil {
			return w.gzip.Write(data)
		}
		return w.ResponseWriter.Write(data)
	}
	length, _ := w.buffer.Write(data)
	if w.buffer.Len() >= gzipMinimumSize {
		if shouldCompress(w.Header(), w.status) {
			if err := w.startCompressed(); err != nil {
				return 0, err
			}
		} else if err := w.startPassthrough(); err != nil {
			return 0, err
		}
	}
	return length, nil
}

func (w *gzipResponseWriter) Flush() {
	if !w.committed {
		if shouldCompress(w.Header(), w.status) {
			_ = w.startCompressed()
		} else {
			_ = w.startPassthrough()
		}
	}
	if w.gzip != nil {
		_ = w.gzip.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *gzipResponseWriter) Close() error {
	if !w.committed {
		if shouldCompress(w.Header(), w.status) && w.buffer.Len() >= gzipMinimumSize {
			if err := w.startCompressed(); err != nil {
				return err
			}
		} else if err := w.startPassthrough(); err != nil {
			return err
		}
	}
	if w.gzip != nil {
		return w.gzip.Close()
	}
	return nil
}

func shouldCompress(header http.Header, status int) bool {
	if status < 200 || status == http.StatusNoContent || status == http.StatusNotModified || status == http.StatusPartialContent || header.Get("Content-Encoding") != "" || strings.HasPrefix(header.Get("Content-Type"), "text/event-stream") {
		return false
	}
	return true
}

func (w *gzipResponseWriter) startCompressed() error {
	if w.Header().Get("Content-Type") == "" && w.buffer.Len() > 0 {
		w.Header().Set("Content-Type", http.DetectContentType(w.buffer.Bytes()))
	}
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Encoding", "gzip")
	appendVary(w.Header(), "Accept-Encoding")
	w.ResponseWriter.WriteHeader(w.status)
	w.committed = true
	w.gzip = gzip.NewWriter(w.ResponseWriter)
	_, err := w.gzip.Write(w.buffer.Bytes())
	w.buffer.Reset()
	return err
}

func (w *gzipResponseWriter) startPassthrough() error {
	w.ResponseWriter.WriteHeader(w.status)
	w.committed = true
	w.passthrough = true
	if w.buffer.Len() == 0 {
		return nil
	}
	_, err := w.ResponseWriter.Write(w.buffer.Bytes())
	w.buffer.Reset()
	return err
}

func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		recorder := &recoveryWriter{ResponseWriter: response}
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("unhandled http panic", "method", request.Method, "path", request.URL.Path)
				if recorder.wrote {
					return
				}
				writeJSONError(response, http.StatusInternalServerError, "Internal server error")
			}
		}()
		next.ServeHTTP(recorder, request)
	})
}

type recoveryWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *recoveryWriter) WriteHeader(status int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *recoveryWriter) Write(data []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(data)
}

func (w *recoveryWriter) Flush() {
	w.wrote = true
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *recoveryWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.wrote = true
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *recoveryWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		logger.Info("http request",
			"method", request.Method,
			"path", request.URL.Path,
			"status", recorder.status,
			"bytes", recorder.bytes,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	length, err := w.ResponseWriter.Write(data)
	w.bytes += length
	return length, err
}

func (w *statusRecorder) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusRecorder) ReadFrom(reader io.Reader) (int64, error) {
	length, err := io.Copy(struct{ io.Writer }{w}, reader)
	return length, err
}

func writeAuthError(response http.ResponseWriter, failure *auth.Error) {
	writeJSONError(response, failure.Status, failure.Detail)
}

func writeJSONError(response http.ResponseWriter, status int, detail string) {
	writeJSON(response, status, map[string]string{"detail": detail})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func methodNotAllowed(response http.ResponseWriter, methods ...string) {
	response.Header().Set("Allow", strings.Join(methods, ", "))
	writeJSONError(response, http.StatusMethodNotAllowed, "Method Not Allowed")
}

func appendVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, token := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func headerToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func isWebSocketRequest(request *http.Request) bool {
	return strings.EqualFold(request.Header.Get("Upgrade"), "websocket") && headerToken(request.Header, "Connection", "upgrade")
}

// bundledHelperAssets returns <bin>/helpers next to the running executable.
func bundledHelperAssets() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "helpers")
}
