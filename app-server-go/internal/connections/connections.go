package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// Publisher broadcasts business events.
type Publisher interface {
	Publish(ctx context.Context, eventType string, payload map[string]any) error
}

// VersionExecutor executes version queries against agent binaries or SSH.
type VersionExecutor interface {
	GetVersion(ctx context.Context, kind, connID string, sshConn map[string]any) (version string, isUpToDate bool, err error)
}

// NativeVersionExecutor implements VersionExecutor via actual local/remote CLI execution.
type NativeVersionExecutor struct {
	SSHBinary string
}

func (e *NativeVersionExecutor) GetVersion(ctx context.Context, kind, connID string, sshConn map[string]any) (string, bool, error) {
	binName := strings.ToLower(kind)
	switch binName {
	case "codex", "hermes", "grok":
	default:
		return "", false, fmt.Errorf("unsupported agent kind %q", kind)
	}

	if connID != "" && connID != "local" {
		if sshConn == nil {
			return "", false, errors.New("ssh connection data is required")
		}
		targetHost, baseArgs, err := sshProbeTarget(sshConn)
		if err != nil {
			return "", false, err
		}

		sshBin := e.SSHBinary
		if sshBin == "" {
			sshBin = "ssh"
		}
		args := append(append([]string(nil), baseArgs...), targetHost, remoteAgentSetup(binName)+`exec "$p"`, "--version")

		cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, sshBin, args...)
		configureChildProcess(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", false, err
		}
		return parseAgentVersionOutput(binName, string(out))
	}

	// Local
	cmdCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	binary, err := localAgentExecutable(ctx, binName)
	if err != nil {
		return "", false, err
	}
	cmd := exec.CommandContext(cmdCtx, binary, "--version")
	configureChildProcess(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", false, err
	}
	return parseAgentVersionOutput(binName, string(out))
}

var versionPattern = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?(?:[a-zA-Z0-9_\-\.]+)?|\d{4}\.\d+\.\d+)`)

func parseAgentVersionOutput(kind, out string) (string, bool, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return "未知", false, nil
	}
	isUpToDate := strings.Contains(strings.ToLower(trimmed), "up to date")
	hasUpdateFlag := strings.Contains(strings.ToLower(trimmed), "update available")

	m := versionPattern.FindStringSubmatch(trimmed)
	ver := "未知"
	if len(m) > 1 {
		ver = m[1]
	} else {
		lines := strings.Split(trimmed, "\n")
		if len(lines) > 0 {
			ver = strings.TrimSpace(lines[0])
		}
	}
	return ver, isUpToDate && !hasUpdateFlag, nil
}

// Options configures the connections product service.
type Options struct {
	Store           *store.Store
	Client          *daemonclient.Client
	Authorizer      *auth.Authenticator
	Publisher       Publisher
	VersionExecutor VersionExecutor
	UpgradeExecutor UpgradeExecutor
	OSProber        OSProber
	SSHBinary       string
	// RemoteExecutable resolves remote codex/grok CLI paths before attach.
	RemoteExecutable RemoteExecutableResolver
	// Hooks installs native Codex hooks; nil keeps the observer endpoint inert.
	Hooks HookInstaller
}

// Handler manages /api/v1/connections, /api/v1/environments, and /api/v1/agents endpoints.
type Handler struct {
	store       *store.Store
	client      *daemonclient.Client
	authorizer  *auth.Authenticator
	publisher   Publisher
	versioner   VersionExecutor
	upgrader    UpgradeExecutor
	osProber    OSProber
	sshBinary   string
	executables RemoteExecutableResolver
	hooks       HookInstaller
	discovery   *discoveryCache
	restoreMu   sync.Mutex
	autoOnce    sync.Once
}

// NewHandler creates a new connections HTTP handler.
func NewHandler(opts Options) (*Handler, error) {
	if opts.Store == nil {
		return nil, errors.New("store is required")
	}
	versioner := opts.VersionExecutor
	if versioner == nil {
		versioner = &NativeVersionExecutor{SSHBinary: opts.SSHBinary}
	}
	upgrader := opts.UpgradeExecutor
	if upgrader == nil {
		upgrader = &NativeUpgradeExecutor{SSHBinary: opts.SSHBinary}
	}
	osProber := opts.OSProber
	if osProber == nil {
		osProber = &NativeOSProber{SSHBinary: opts.SSHBinary}
	}
	executables := opts.RemoteExecutable
	if executables == nil {
		executables = &NativeRemoteExecutableResolver{SSHBinary: opts.SSHBinary}
	}
	return &Handler{
		store:       opts.Store,
		client:      opts.Client,
		authorizer:  opts.Authorizer,
		publisher:   opts.Publisher,
		versioner:   versioner,
		upgrader:    upgrader,
		osProber:    osProber,
		sshBinary:   opts.SSHBinary,
		executables: executables,
		hooks:       opts.Hooks,
		discovery:   newDiscoveryCache(),
	}, nil
}

// Matches checks if the method and path match the connections/environments/agents routes.
func Matches(method, path string) bool {
	clean := strings.TrimRight(path, "/")
	if clean == "/api/v1/agents" {
		return method == http.MethodGet
	}
	if strings.HasPrefix(clean, "/api/v1/agents/") {
		return true
	}
	if clean == "/api/v1/connections" || clean == "/api/v1/environments" {
		return method == http.MethodGet
	}
	if strings.HasPrefix(clean, "/api/v1/connections/") || strings.HasPrefix(clean, "/api/v1/environments/") {
		return true
	}
	return false
}

func parseSSHConfig(conn map[string]any) *sessiondv1.SshConnectConfig {
	if conn == nil {
		return nil
	}
	settings, _ := conn["settings"].(map[string]any)
	if settings == nil {
		settings = conn
	}
	host, _ := settings["host"].(string)
	if host == "" {
		host, _ = conn["host"].(string)
	}
	port, _ := sshPort(settings["port"])

	var userPtr, aliasPtr, idFilePtr, wsPtr *string
	if u, ok := settings["user"].(string); ok && u != "" {
		userPtr = &u
	}
	if a, ok := settings["ssh_config_alias"].(string); ok && a != "" {
		aliasPtr = &a
	}
	if i, ok := settings["identity_file"].(string); ok && i != "" {
		idFilePtr = &i
	}
	if w, ok := settings["workspace"].(string); ok && w != "" {
		wsPtr = &w
	}

	return &sessiondv1.SshConnectConfig{
		Host:             host,
		Port:             port,
		User:             userPtr,
		SshConfigAlias:   aliasPtr,
		IdentityFile:     idFilePtr,
		Workspace:        wsPtr,
		ProfileName:      sshConfigText(settings, conn, "profile_name"),
		DisplayName:      sshConfigText(settings, conn, "display_name"),
		HermesPath:       sshConfigText(settings, conn, "hermes_path"),
		HermesExecutable: sshConfigText(settings, conn, "hermes_executable"),
		CodexExecutable:  sshConfigText(settings, conn, "codex_executable"),
		GrokExecutable:   sshConfigText(settings, conn, "grok_executable"),
	}
}

func sshConfigText(settings, conn map[string]any, key string) *string {
	value, _ := settings[key].(string)
	if value == "" {
		value, _ = conn[key].(string)
	}
	if value == "" {
		return nil
	}
	return &value
}

func runtimeTarget(agentType string, connID *string, sshConn map[string]any) *sessiondv1.RuntimeTarget {
	kind := sessiondv1.RuntimeKind_RUNTIME_KIND_UNSPECIFIED
	switch strings.ToLower(agentType) {
	case "codex":
		if connID != nil && *connID != "" {
			kind = sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX_SSH
		} else {
			kind = sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX
		}
	case "hermes":
		if connID != nil && *connID != "" {
			kind = sessiondv1.RuntimeKind_RUNTIME_KIND_SSH_HERMES
		} else {
			kind = sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES
		}
	case "grok":
		if connID != nil && *connID != "" {
			kind = sessiondv1.RuntimeKind_RUNTIME_KIND_GROK_SSH
		} else {
			kind = sessiondv1.RuntimeKind_RUNTIME_KIND_GROK
		}
	}
	var sshCfg *sessiondv1.SshConnectConfig
	if connID != nil && *connID != "" && sshConn != nil {
		sshCfg = parseSSHConfig(sshConn)
	}
	return &sessiondv1.RuntimeTarget{
		Runtime:      kind,
		ConnectionId: connID,
		Ssh:          sshCfg,
	}
}

func canonicalMeta(operationID string) *sessiondv1.CommandMeta {
	return &sessiondv1.CommandMeta{
		OperationId:      operationID,
		Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_ASTRORDER_OPERATION_V1,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authorizer != nil {
		if failure := h.authorizer.AuthorizeBrowser(r); failure != nil {
			writeError(w, failure.Status, "unauthorized", failure.Detail)
			return
		}
	}

	path := strings.TrimRight(r.URL.Path, "/")
	// Serialize user connection changes with automatic reconciliation so a
	// confirmed disconnect cannot be overtaken by an already queued attach.
	if r.Method != http.MethodGet && (strings.HasPrefix(path, "/api/v1/connections/") ||
		strings.HasPrefix(path, "/api/v1/environments/") && strings.Contains(path, "/agents/")) {
		h.restoreMu.Lock()
		defer h.restoreMu.Unlock()
	}
	switch {
	// Agents
	case r.Method == http.MethodGet && path == "/api/v1/agents":
		h.handleListAgents(w, r)
	case r.Method == http.MethodGet && path == "/api/v1/agents/upgrade/targets":
		h.handleAgentUpgradeTargets(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/agents/") && strings.HasSuffix(path, "/observations"):
		h.handleAgentObservations(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/agents/") && strings.HasSuffix(path, "/observer"):
		h.handleAgentObserver(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/agents/") && strings.HasSuffix(path, "/upgrade"):
		h.handleAgentUpgrade(w, r)

	// Environments
	case r.Method == http.MethodGet && path == "/api/v1/environments":
		h.handleListEnvironments(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/environments/") && strings.HasSuffix(path, "/discover"):
		h.handleDiscoverEnvironment(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/environments/") && strings.Contains(path, "/agents/"):
		h.handleEnvironmentAgentAction(w, r)

	// Connections
	case r.Method == http.MethodGet && path == "/api/v1/connections":
		h.handleConnectionsSnapshot(w, r)
	case r.Method == http.MethodGet && path == "/api/v1/connections/codex":
		h.handleCodexConnection(w, r)
	case r.Method == http.MethodPost && path == "/api/v1/connections/codex/connect":
		h.handleConnectCodex(w, r)
	case r.Method == http.MethodPost && path == "/api/v1/connections/codex/disconnect":
		h.handleDisconnectCodex(w, r)
	case r.Method == http.MethodPost && path == "/api/v1/connections/local/connect":
		h.handleConnectLocal(w, r)
	case r.Method == http.MethodPost && path == "/api/v1/connections/local/disconnect":
		h.handleDisconnectLocal(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/connections/") && strings.HasSuffix(path, "/history"):
		h.handleConnectionHistory(w, r)

	// SSH connections
	case r.Method == http.MethodPut && path == "/api/v1/connections/ssh":
		h.handleSaveSSHConnection(w, r, "")
	case r.Method == http.MethodPost && path == "/api/v1/connections/ssh/test":
		h.handleTestSSHConnection(w, r, "")
	case r.Method == http.MethodPost && path == "/api/v1/connections/ssh/connect":
		h.handleConnectSSHConnection(w, r, "")
	case r.Method == http.MethodPost && path == "/api/v1/connections/ssh/disconnect":
		h.handleDisconnectSSHConnection(w, r, "")

	case r.Method == http.MethodPut && strings.HasPrefix(path, "/api/v1/connections/ssh/"):
		connID := strings.TrimPrefix(path, "/api/v1/connections/ssh/")
		h.handleSaveSSHConnection(w, r, connID)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/connections/ssh/") && strings.HasSuffix(path, "/test"):
		connID := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/connections/ssh/"), "/test")
		h.handleTestSSHConnection(w, r, connID)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/connections/ssh/") && strings.HasSuffix(path, "/connect"):
		connID := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/connections/ssh/"), "/connect")
		h.handleConnectSSHConnection(w, r, connID)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/connections/ssh/") && strings.HasSuffix(path, "/disconnect"):
		connID := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/connections/ssh/"), "/disconnect")
		h.handleDisconnectSSHConnection(w, r, connID)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/connections/ssh/"):
		connID := strings.TrimPrefix(path, "/api/v1/connections/ssh/")
		h.handleDeleteSSHConnection(w, r, connID)

	default:
		writeError(w, http.StatusNotFound, "not_found", "Route not found")
	}
}

// handleListAgents lists agents from the persistent store.
func (h *Handler) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.store.ListCurrentAgents(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": agents})
}

// handleAgentUpgradeTargets returns actual inspected upgrade targets with versions.
func (h *Handler) handleAgentUpgradeTargets(w http.ResponseWriter, r *http.Request) {
	agents, err := h.store.ListCurrentAgents(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	items := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		kind := strings.ToLower(fmt.Sprint(a["kind"]))
		if kind != "codex" && kind != "hermes" && kind != "grok" {
			continue
		}
		connID, _ := a["connection_id"].(string)
		if connID == "" {
			connID = "local"
		}

		var sshConn map[string]any
		if connID != "local" {
			sshConn, err = h.store.GetSSHConnection(r.Context(), connID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "store_error", err.Error())
				return
			}
			// Historical registrations retain their sessions, but a removed
			// machine has no installation that can be inspected or upgraded.
			if sshConn == nil {
				continue
			}
		}

		check := VersionCheck{}
		if inspector, ok := h.versioner.(UpgradeVersionInspector); ok {
			check = inspector.CheckUpgrade(r.Context(), kind, connID, sshConn)
		} else {
			current, upToDate, err := h.versioner.GetVersion(r.Context(), kind, connID, sshConn)
			check.Current = current
			if err != nil {
				check.Error = err.Error()
			} else if upToDate {
				check.Latest = current
			} else {
				check.Error = "原生版本输出未确认更新状态"
			}
		}
		if check.Current == "" {
			check.Current = "未知"
		}
		if check.Latest == "" {
			check.Latest = "未知"
		}

		items = append(items, map[string]any{
			"id":              a["id"],
			"name":            a["name"],
			"kind":            kind,
			"status":          a["status"],
			"connection_id":   connID,
			"current_version": check.Current,
			"latest_version":  check.Latest,
			"has_update":      check.Error == "" && check.HasUpdate,
			"version_error":   check.Error,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleAgentObservations(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "invalid_path", "Invalid agent ID")
		return
	}
	agentID := parts[3]
	agent, err := h.store.GetAgent(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if agent == nil {
		writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": agent["status"],
		"items":  []any{},
	})
}

func (h *Handler) handleAgentObserver(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "invalid_path", "Invalid agent ID")
		return
	}
	agentID := parts[3]
	agent, err := h.store.GetAgent(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if agent == nil {
		writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	if h.hooks == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agent_id": agentID})
		return
	}
	result, err := h.hooks.Install(r.Context(), agent)
	if err != nil {
		writeError(w, http.StatusBadGateway, "hook_install_failed", err.Error())
		return
	}
	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]any{
		"ok": result.OK, "agent_id": agentID, "error": result.Error, "rewrite": result.Rewrite,
		"trust": result.Trust, "trust_error": result.TrustError,
		"installed": result.OK, "trusted": result.Trust != nil && result.TrustError == "",
		"needs_review": result.OK && result.TrustError != "",
	})
}

func (h *Handler) handleAgentUpgrade(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "invalid_path", "Invalid agent ID")
		return
	}
	agentID := parts[3]
	agent, err := h.store.GetAgent(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if agent == nil {
		writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	kind := strings.ToLower(fmt.Sprint(agent["kind"]))
	connID, _ := agent["connection_id"].(string)
	var sshConn map[string]any
	if connID != "" && connID != "local" {
		sshConn, err = h.store.GetSSHConnection(r.Context(), connID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		if sshConn == nil {
			writeError(w, http.StatusConflict, "connection_removed", "Agent 所属环境已移除，无法升级")
			return
		}
	}
	h.streamAgentUpgrade(w, r, kind, connID, sshConn)
}

// handleListEnvironments returns the environments page snapshot contract.
func (h *Handler) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	items, err := h.environmentSnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleDiscoverEnvironment executes real availability probes and returns the
// same environment item structure as the listing.
func (h *Handler) handleDiscoverEnvironment(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "invalid_path", "Invalid connection ID")
		return
	}
	connID := parts[3]
	if connID == "local" {
		result := h.probeLocal(r.Context())
		h.discovery.set(discoveryKey("local", nil), result)
		items, err := h.environmentSnapshot(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, items[0])
		return
	}

	conn, err := h.store.GetSSHConnection(r.Context(), connID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if conn == nil {
		writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
		return
	}

	result, answered := h.probeRemote(r.Context(), connID, conn)
	if !answered {
		// A fully unreachable probe run must not leave a stale successful
		// discovery behind: clear the cached result for this exact target so
		// the listing cannot keep claiming old availability.
		h.discovery.forget(discoveryKey(connID, conn))
		writeError(w, http.StatusBadGateway, "discovery_unreachable", "SSH 探测未确认：未获得任何远端应答，未保留发现结果。")
		return
	}
	h.discovery.set(discoveryKey(connID, conn), result)
	item, err := h.remoteEnvironmentItem(r.Context(), connID, conn, result, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handler) handleEnvironmentAgentAction(w http.ResponseWriter, r *http.Request) {
	// /api/v1/environments/{connection_id}/agents/{kind}/{action}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 7 {
		writeError(w, http.StatusBadRequest, "invalid_path", "Invalid route arguments")
		return
	}
	connID := parts[3]
	kind := parts[5]
	action := parts[6]
	if kind != "hermes" && kind != "codex" && kind != "grok" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_kind", "Unsupported agent kind")
		return
	}

	if action != "connect" && action != "disconnect" && action != "restart" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_action", "不支持的连接操作。")
		return
	}

	var connPtr *string
	var sshConn map[string]any
	if connID != "local" {
		connPtr = &connID
		var err error
		sshConn, err = h.store.GetSSHConnection(r.Context(), connID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		if sshConn == nil {
			writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
			return
		}
		settings, _ := sshConn["settings"].(map[string]any)
		if _, err := validateSSHSettings(settings); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_settings", err.Error())
			return
		}
	}

	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable", "Session daemon client is unavailable")
		return
	}

	// Remote codex/grok attach needs the actual CLI path: resolve it with one
	// read-only probe before the target is built, and only for operations that
	// attach. A failed resolution never disconnects or persists anything.
	if connID != "local" && (kind == "codex" || kind == "grok") && (action == "connect" || action == "restart") {
		resolver := h.executables
		if resolver == nil {
			resolver = &NativeRemoteExecutableResolver{SSHBinary: h.sshBinary}
		}
		executable, err := resolver.Resolve(r.Context(), kind, sshConn)
		if err != nil {
			writeError(w, http.StatusBadGateway, "executable_unresolved", err.Error())
			return
		}
		sshConn = withResolvedExecutable(sshConn, kind, executable)
	}

	target := runtimeTarget(kind, connPtr, sshConn)

	switch action {
	case "disconnect":
		opID := fmt.Sprintf("disconnect-%s-%s-%d", kind, connID, time.Now().UnixNano())
		resp, err := h.client.Runtime.DisconnectRuntime(r.Context(), &sessiondv1.DisconnectRuntimeRequest{
			Meta:   canonicalMeta(opID),
			Target: target,
		})
		if err := h.disconnectReceipt(resp, err); err != nil {
			writeError(w, http.StatusConflict, "disconnect_refused", err.Error())
			return
		}

	case "connect":
		expected := &sessiondv1.SessionRef{
			SessionId:    fmt.Sprintf("probe-%s-%s", kind, connID),
			ConnectionId: connPtr,
		}
		receipt, err := h.client.Session.AttachSession(r.Context(), &sessiondv1.AttachSessionRequest{
			Session:        expected,
			Target:         target,
			RuntimeControl: true,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, "attach_failed", err.Error())
			return
		}
		if err := h.attachReceipt(receipt, expected); err != nil {
			writeError(w, http.StatusBadGateway, "attach_unconfirmed", err.Error())
			return
		}

	case "restart":
		// Restart performs clean authoritative disconnect followed by attach
		opID := fmt.Sprintf("restart-disconnect-%s-%s-%d", kind, connID, time.Now().UnixNano())
		resp, err := h.client.Runtime.DisconnectRuntime(r.Context(), &sessiondv1.DisconnectRuntimeRequest{
			Meta:   canonicalMeta(opID),
			Target: target,
		})
		if err := h.disconnectReceipt(resp, err); err != nil {
			writeError(w, http.StatusConflict, "disconnect_refused", err.Error())
			return
		}
		// The old runtime is authoritatively released; until the re-attach is
		// confirmed the environment may not keep claiming the previous
		// connected state.
		if connID != "local" && kind == "hermes" {
			if _, err := h.store.UpdateSSHConnectionState(r.Context(), connID, "connecting", "重启进行中：旧运行时已断开，等待重新接入确认。", nil); err != nil {
				writeError(w, http.StatusInternalServerError, "store_error", err.Error())
				return
			}
		}
		expected := &sessiondv1.SessionRef{
			SessionId:    fmt.Sprintf("probe-%s-%s", kind, connID),
			ConnectionId: connPtr,
		}
		receipt, err := h.client.Session.AttachSession(r.Context(), &sessiondv1.AttachSessionRequest{
			Session:        expected,
			Target:         target,
			RuntimeControl: true,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, "restart_attach_failed", err.Error())
			return
		}
		if err := h.attachReceipt(receipt, expected); err != nil {
			writeError(w, http.StatusBadGateway, "attach_unconfirmed", err.Error())
			return
		}
	}

	// The SSH row is the app-owned persisted state for the remote Hermes
	// environment; update it only after the daemon confirmed the operation.
	if connID != "local" && kind == "hermes" {
		state, detail, event := "connected", "Connected successfully", "ssh_connection.connected"
		if action == "disconnect" {
			state, detail, event = "disconnected", "Disconnected explicitly", "ssh_connection.disconnected"
		}
		updated, err := h.store.UpdateSSHConnectionState(r.Context(), connID, state, detail, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		if updated == nil {
			writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
			return
		}
		if h.publisher != nil {
			_ = h.publisher.Publish(r.Context(), event, updated)
		}
	}

	// Only an authoritative success reaches this point; refused or unconfirmed
	// operations already returned above without touching the persisted choice.
	if err := h.store.SetAgentConnectionChoice(r.Context(), connID+":"+kind, action != "disconnect"); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}

	items, err := h.environmentSnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleConnectionsSnapshot returns connections state snapshot.
func (h *Handler) handleConnectionsSnapshot(w http.ResponseWriter, r *http.Request) {
	connections, err := h.store.ListSSHConnections(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	local, err := h.localConnection(r.Context(), "hermes")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	ssh := map[string]any{"items": connections, "state": "unconfigured", "settings": nil, "detail": "尚未配置远程 SSH 连接。"}
	if len(connections) > 0 {
		for _, key := range []string{"state", "settings", "detail"} {
			ssh[key] = connections[0][key]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"local": local,
		"ssh":   ssh,
	})
}

func (h *Handler) handleCodexConnection(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.localConnection(r.Context(), "codex")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) handleConnectCodex(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable", "Session daemon client is unavailable")
		return
	}
	expected := &sessiondv1.SessionRef{SessionId: "probe-codex-local"}
	receipt, err := h.client.Session.AttachSession(r.Context(), &sessiondv1.AttachSessionRequest{
		Session:        expected,
		Target:         runtimeTarget("codex", nil, nil),
		RuntimeControl: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "attach_failed", err.Error())
		return
	}
	if err := h.attachReceipt(receipt, expected); err != nil {
		writeError(w, http.StatusBadGateway, "attach_unconfirmed", err.Error())
		return
	}
	if err := h.store.SetAgentConnectionChoice(r.Context(), "local:codex", true); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	h.handleCodexConnection(w, r)
}

func (h *Handler) handleDisconnectCodex(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable", "Session daemon client is unavailable")
		return
	}
	opID := fmt.Sprintf("disconnect-codex-%d", time.Now().UnixNano())
	resp, err := h.client.Runtime.DisconnectRuntime(r.Context(), &sessiondv1.DisconnectRuntimeRequest{
		Meta:   canonicalMeta(opID),
		Target: runtimeTarget("codex", nil, nil),
	})
	if err := h.disconnectReceipt(resp, err); err != nil {
		writeError(w, http.StatusConflict, "disconnect_refused", err.Error())
		return
	}
	if err := h.store.SetAgentConnectionChoice(r.Context(), "local:codex", false); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	h.handleCodexConnection(w, r)
}

func (h *Handler) handleConnectLocal(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable", "Session daemon client is unavailable")
		return
	}
	expected := &sessiondv1.SessionRef{SessionId: "probe-hermes-local"}
	receipt, err := h.client.Session.AttachSession(r.Context(), &sessiondv1.AttachSessionRequest{
		Session:        expected,
		Target:         runtimeTarget("hermes", nil, nil),
		RuntimeControl: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "attach_failed", err.Error())
		return
	}
	if err := h.attachReceipt(receipt, expected); err != nil {
		writeError(w, http.StatusBadGateway, "attach_unconfirmed", err.Error())
		return
	}
	if err := h.store.SetAgentConnectionChoice(r.Context(), "local:hermes", true); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	h.handleConnectionsSnapshot(w, r)
}

func (h *Handler) handleDisconnectLocal(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable", "Session daemon client is unavailable")
		return
	}
	opID := fmt.Sprintf("disconnect-local-%d", time.Now().UnixNano())
	resp, err := h.client.Runtime.DisconnectRuntime(r.Context(), &sessiondv1.DisconnectRuntimeRequest{
		Meta:   canonicalMeta(opID),
		Target: runtimeTarget("hermes", nil, nil),
	})
	if err := h.disconnectReceipt(resp, err); err != nil {
		writeError(w, http.StatusConflict, "disconnect_refused", err.Error())
		return
	}
	if err := h.store.SetAgentConnectionChoice(r.Context(), "local:hermes", false); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	h.handleConnectionsSnapshot(w, r)
}

func (h *Handler) handleConnectionHistory(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeError(w, http.StatusBadRequest, "invalid_path", "Invalid connection ID")
		return
	}
	connID := parts[3]
	conn, err := h.store.GetSSHConnection(r.Context(), connID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if conn == nil {
		writeError(w, http.StatusNotFound, "connection_not_found", "Connection was not found")
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	before := r.URL.Query().Get("before")
	items, nextCursor, err := h.store.ListConnectionHistory(r.Context(), connID, before, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cursor", "Invalid history cursor")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
	})
}

// SSH CRUD & Operations

func (h *Handler) handleSaveSSHConnection(w http.ResponseWriter, r *http.Request, connID string) {
	var payload map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON request")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json", "Expected one JSON object")
		return
	}
	settings, err := validateSSHSettings(payload)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_settings", err.Error())
		return
	}
	if connID == "" {
		if raw := payload["connection_id"]; raw != nil {
			var ok bool
			connID, ok = raw.(string)
			if !ok {
				writeError(w, http.StatusUnprocessableEntity, "invalid_settings", "connection_id must be text")
				return
			}
		}
	}
	if connID != "" {
		existing, err := h.store.GetSSHConnection(r.Context(), connID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		if existing == nil {
			writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
			return
		}
		if existing["state"] == "connected" || existing["state"] == "connecting" {
			writeError(w, http.StatusConflict, "connection_in_use", "Disconnect the SSH connection before editing its settings")
			return
		}
		settings["id"] = connID
	} else if id, ok := payload["id"].(string); ok && id != "" {
		existing, err := h.store.GetSSHConnection(r.Context(), id)
		if err != nil || existing != nil {
			writeError(w, http.StatusConflict, "connection_exists", "Use connection_id to update an existing connection")
			return
		}
		settings["id"] = id
	}
	settings["state"] = "configured"
	settings["detail"] = "SSH 配置已保存，尚未连接远端。"
	saved, err := h.store.SaveSSHConnection(r.Context(), settings)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "save_failed", err.Error())
		return
	}
	if h.publisher != nil {
		_ = h.publisher.Publish(r.Context(), "ssh_connection.updated", saved)
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) handleTestSSHConnection(w http.ResponseWriter, r *http.Request, connID string) {
	conn, err := h.store.GetSSHConnection(r.Context(), connID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if conn == nil {
		writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
		return
	}
	settings, _ := conn["settings"].(map[string]any)
	validated, err := validateSSHSettings(settings)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_settings", err.Error())
		return
	}
	port, _ := sshPort(validated["port"])
	target, _ := validated["ssh_config_alias"].(string)
	if target == "" {
		target, _ = validated["host"].(string)
	}
	args := []string{"-G", "-p", strconv.Itoa(int(port))}
	for _, option := range []struct{ key, flag string }{{"user", "-l"}, {"identity_file", "-i"}} {
		if value, _ := validated[option.key].(string); value != "" {
			args = append(args, option.flag, value)
		}
	}
	args = append(args, target)
	binary := h.sshBinary
	if binary == "" {
		binary = "ssh"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	configureChildProcess(cmd)
	err = cmd.Run()
	state, detail := "validated", "OpenSSH 配置已校验；尚未连接或启动远端 Hermes。"
	statusCode := http.StatusOK
	if err != nil {
		state, detail = "error", "SSH 配置校验失败；未建立远程连接。"
		statusCode = http.StatusUnprocessableEntity
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || ctx.Err() != nil {
			statusCode, detail = http.StatusServiceUnavailable, "SSH 配置校验未完成或 OpenSSH 客户端不可用。"
		}
	}
	actualID, _ := conn["id"].(string)
	updated, storeErr := h.store.UpdateSSHConnectionState(r.Context(), actualID, state, detail, nil)
	if storeErr != nil {
		writeError(w, http.StatusInternalServerError, "store_error", storeErr.Error())
		return
	}
	if updated == nil {
		writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
		return
	}
	if err != nil {
		writeError(w, statusCode, "validation_failed", detail)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleConnectSSHConnection(w http.ResponseWriter, r *http.Request, connID string) {
	conn, err := h.store.GetSSHConnection(r.Context(), connID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if conn == nil {
		writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
		return
	}
	actualID, _ := conn["id"].(string)

	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable", "Session daemon client is unavailable")
		return
	}

	target := runtimeTarget("hermes", &actualID, conn)
	expected := &sessiondv1.SessionRef{
		SessionId:    "probe-hermes-" + actualID,
		ConnectionId: &actualID,
	}
	receipt, err := h.client.Session.AttachSession(r.Context(), &sessiondv1.AttachSessionRequest{
		Session:        expected,
		Target:         target,
		RuntimeControl: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "attach_failed", err.Error())
		return
	}
	if err := h.attachReceipt(receipt, expected); err != nil {
		writeError(w, http.StatusBadGateway, "attach_unconfirmed", err.Error())
		return
	}

	if err := h.store.SetAgentConnectionChoice(r.Context(), actualID+":hermes", true); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	updated, err := h.store.UpdateSSHConnectionState(r.Context(), actualID, "connected", "Connected successfully", nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if h.publisher != nil {
		_ = h.publisher.Publish(r.Context(), "ssh_connection.connected", updated)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleDisconnectSSHConnection(w http.ResponseWriter, r *http.Request, connID string) {
	conn, err := h.store.GetSSHConnection(r.Context(), connID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if conn == nil {
		writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
		return
	}
	actualID, _ := conn["id"].(string)

	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable", "Session daemon client is unavailable")
		return
	}

	opID := fmt.Sprintf("disconnect-ssh-%s-%d", actualID, time.Now().UnixNano())
	resp, err := h.client.Runtime.DisconnectRuntime(r.Context(), &sessiondv1.DisconnectRuntimeRequest{
		Meta:   canonicalMeta(opID),
		Target: runtimeTarget("hermes", &actualID, conn),
	})
	if err := h.disconnectReceipt(resp, err); err != nil {
		writeError(w, http.StatusConflict, "disconnect_refused", err.Error())
		return
	}

	if err := h.store.SetAgentConnectionChoice(r.Context(), actualID+":hermes", false); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	updated, err := h.store.UpdateSSHConnectionState(r.Context(), actualID, "disconnected", "Disconnected explicitly", nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if h.publisher != nil {
		_ = h.publisher.Publish(r.Context(), "ssh_connection.disconnected", updated)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleDeleteSSHConnection(w http.ResponseWriter, r *http.Request, connID string) {
	conn, err := h.store.GetSSHConnection(r.Context(), connID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if conn == nil {
		writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
		return
	}
	actualID, _ := conn["id"].(string)

	if h.client != nil {
		opID := fmt.Sprintf("delete-ssh-%s-%d", actualID, time.Now().UnixNano())
		resp, err := h.client.Runtime.DisconnectRuntime(r.Context(), &sessiondv1.DisconnectRuntimeRequest{
			Meta:   canonicalMeta(opID),
			Target: runtimeTarget("hermes", &actualID, conn),
		})
		if err := h.disconnectReceipt(resp, err); err != nil {
			writeError(w, http.StatusConflict, "disconnect_refused", err.Error())
			return
		}
	}

	ok, err := h.store.DeleteSSHConnection(r.Context(), actualID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "connection_not_found", "Connection not found")
		return
	}
	if h.publisher != nil {
		_ = h.publisher.Publish(r.Context(), "ssh_connection.deleted", map[string]any{"id": actualID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": actualID})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"detail": message,
		"code":   code,
	})
}
