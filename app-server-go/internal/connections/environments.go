package connections

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// environmentKinds is the fixed Agent catalog rendered by the environments page.
var environmentKinds = []string{"hermes", "codex", "grok", "antigravity"}

// OSProber resolves the operating system of a remote environment through a real probe.
type OSProber interface {
	ProbeOS(ctx context.Context, connectionID string, sshConn map[string]any) (string, error)
}

// NativeOSProber probes the remote OS by executing uname over SSH. It never
// fabricates a value: failures surface as errors and the item keeps os=null.
type NativeOSProber struct {
	SSHBinary string
}

// ProbeOS executes one real SSH command and reports the remote kernel name.
func (p *NativeOSProber) ProbeOS(ctx context.Context, _ string, sshConn map[string]any) (string, error) {
	target, argv, err := sshProbeTarget(sshConn)
	if err != nil {
		return "", err
	}
	binary := p.SSHBinary
	if binary == "" {
		binary = "ssh"
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, binary, append(argv, target, "uname -s")...)
	configureChildProcess(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", errors.New("remote uname produced no output")
	}
	return value, nil
}

// discoveryResult holds the outcome of one real discovery probe run.
type discoveryResult struct {
	OS          string
	Versions    map[string]string
	Available   map[string]bool
	Executables map[string]string
}

// discoveryCache is an instance-level cache isolated by connection target.
type discoveryCache struct {
	mu      sync.Mutex
	entries map[string]discoveryResult
}

func newDiscoveryCache() *discoveryCache {
	return &discoveryCache{entries: map[string]discoveryResult{}}
}

func (c *discoveryCache) set(key string, result discoveryResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = result
}

func (c *discoveryCache) get(key string) (discoveryResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, ok := c.entries[key]
	return result, ok
}

// forget drops a previous result so a failed probe run cannot leave stale
// success in place of the current truth.
func (c *discoveryCache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// discoveryKey isolates cached results by connection ID plus the exact SSH
// target parameters, so edited settings never reuse a stale probe result.
func discoveryKey(connectionID string, sshConn map[string]any) string {
	if connectionID == "local" || sshConn == nil {
		return connectionID + "@local"
	}
	settings, _ := sshConn["settings"].(map[string]any)
	if settings == nil {
		settings = sshConn
	}
	host, _ := settings["host"].(string)
	alias, _ := settings["ssh_config_alias"].(string)
	user, _ := settings["user"].(string)
	identity, _ := settings["identity_file"].(string)
	port, _ := sshPort(settings["port"])
	return fmt.Sprintf("%s@%s|%s|%s|%s|%d", connectionID, host, alias, user, identity, port)
}

// sshProbeTarget derives the real SSH target and base argv from stored settings.
func sshProbeTarget(sshConn map[string]any) (string, []string, error) {
	settings, _ := sshConn["settings"].(map[string]any)
	if settings == nil {
		settings = sshConn
	}
	host, _ := settings["host"].(string)
	alias, _ := settings["ssh_config_alias"].(string)
	user, _ := settings["user"].(string)
	identity, _ := settings["identity_file"].(string)
	port, err := sshPort(settings["port"])
	if err != nil {
		return "", nil, err
	}
	target := alias
	if target == "" {
		target = host
	}
	if target == "" {
		return "", nil, errors.New("ssh target host/alias is missing")
	}
	argv := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=4", "-p", strconv.Itoa(int(port))}
	if user != "" {
		argv = append(argv, "-l", user)
	}
	if identity != "" {
		argv = append(argv, "-i", identity)
	}
	return target, argv, nil
}

// remoteRuntimeEvidence is the authoritative daemon evidence for one remote
// runtime kind scoped to a single connection.
type remoteRuntimeEvidence struct {
	// statusKnown is true when the daemon GetStatus call itself succeeded with
	// this daemon epoch; absent kinds then mean "no evidence", not "unknown".
	statusKnown bool
	// registered is the daemon-reported adapter registration for the kind.
	registered bool
	// owned is the daemon's live native ownership count for the kind.
	owned uint32
	// controlSessionID is this connection/kind's own control session identity.
	controlSessionID string
	// controlStatus is the daemon-reported status of that control session.
	controlStatus sessiondv1.SessionStatus
	// controlPresent is true when the control session appears in the daemon
	// authority listing (it may outlive the process as a durable journal row).
	controlPresent bool
	// controlOwnedKnown is true when the daemon explicitly reported the
	// live-ownership verdict (runtime_owned) for the control session. An
	// absent verdict means ownership is unknown and must never be upgraded to
	// connected by the global ownership count.
	controlOwnedKnown bool
	// controlOwned is the explicit live-ownership verdict for the control
	// session. Only an explicit true, with a matching typed runtime kind and
	// this connection's identity, proves a live attachment.
	controlOwned bool
	// controlRuntimeMatches is true when the live owner's typed runtime kind
	// equals this kind's expected remote runtime.
	controlRuntimeMatches bool
	// controlConnectionMatches is true when the live owner's identity carries
	// this connection's ID (remote ownership requires an exact match).
	controlConnectionMatches bool
	// controlStatusKnown is true when the control session's status enum is a
	// known contract value. An unknown enum can never confirm a live
	// attachment.
	controlStatusKnown bool
}

// remoteRuntimeKind maps an environment agent kind to its remote runtime kind.
func remoteRuntimeKind(kind string) sessiondv1.RuntimeKind {
	switch kind {
	case "codex":
		return sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX_SSH
	case "grok":
		return sessiondv1.RuntimeKind_RUNTIME_KIND_GROK_SSH
	default:
		return sessiondv1.RuntimeKind_RUNTIME_KIND_SSH_HERMES
	}
}

// collectRemoteEvidence reads one authoritative GetStatus snapshot and extracts
// per-kind evidence for a single connection. Sessions listed here include
// durable journal rows that survive a released native process, so a session
// entry alone never proves a live attachment.
func (h *Handler) collectRemoteEvidence(ctx context.Context, connID string) (map[string]remoteRuntimeEvidence, error) {
	evidence := map[string]remoteRuntimeEvidence{}
	for _, kind := range environmentKinds {
		evidence[kind] = remoteRuntimeEvidence{
			controlSessionID: fmt.Sprintf("probe-%s-%s", kind, connID),
		}
	}
	if h.client == nil {
		return evidence, nil
	}
	status, err := h.client.Daemon.GetStatus(ctx, &sessiondv1.GetStatusRequest{})
	if err != nil || status == nil || status.GetDaemonId() != h.client.DaemonID() {
		return evidence, nil
	}
	byKind := map[sessiondv1.RuntimeKind]remoteRuntimeEvidence{}
	for _, runtime := range status.Runtimes {
		entry := byKind[runtime.GetRuntime()]
		entry.statusKnown = true
		entry.registered = runtime.GetRegistered()
		entry.owned = runtime.GetOwnedSessions()
		byKind[runtime.GetRuntime()] = entry
	}
	for kind, entry := range evidence {
		entry.statusKnown = true
		if runtime, ok := byKind[remoteRuntimeKind(kind)]; ok {
			entry.registered = runtime.registered
			entry.owned = runtime.owned
		}
		for _, session := range status.Sessions {
			if session.GetSession().GetSessionId() != entry.controlSessionID {
				continue
			}
			entry.controlPresent = true
			entry.controlStatus = session.GetStatus()
			switch session.GetStatus() {
			case sessiondv1.SessionStatus_SESSION_STATUS_IDLE,
				sessiondv1.SessionStatus_SESSION_STATUS_RUNNING,
				sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL,
				sessiondv1.SessionStatus_SESSION_STATUS_ERROR:
				entry.controlStatusKnown = true
			}
			// The live-ownership verdict is optional on the wire: a daemon
			// without the field reports ownership as unknown, and an unknown
			// verdict must never be upgraded to connected by the global
			// ownership count. Only an explicit runtime_owned=true with the
			// expected typed runtime and this connection's identity counts.
			if session.RuntimeOwned != nil {
				entry.controlOwnedKnown = true
				entry.controlOwned = session.GetRuntimeOwned()
				entry.controlRuntimeMatches = session.GetRuntime() == remoteRuntimeKind(kind)
				connectionID := session.GetSession().GetConnectionId()
				entry.controlConnectionMatches = connectionID != "" && connectionID == connID
			}
			break
		}
		evidence[kind] = entry
	}
	return evidence, nil
}

// storedConnectionAgents returns each kind's own stored Agent projection for
// one connection. Rows never leak across kinds or connections.
func (h *Handler) storedConnectionAgents(ctx context.Context, connID string) (map[string]map[string]any, error) {
	agents, err := h.store.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read connection Agent projection: %w", err)
	}
	result := map[string]map[string]any{}
	for _, agent := range agents {
		kind, _ := agent["kind"].(string)
		if kind == "" || !containsKind(kind) {
			continue
		}
		connectionID, _ := agent["connection_id"].(string)
		if connectionID != connID {
			continue
		}
		if result[kind] == nil || agent["status"] == "ready" {
			result[kind] = agent
		}
	}
	return result, nil
}

func containsKind(kind string) bool {
	for _, candidate := range environmentKinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

// remoteAgentState projects one kind of a remote environment from its own
// daemon evidence and its own stored projection. A kind is only "connected"
// when the daemon explicitly confirms live ownership (runtime_owned=true) for
// this kind's own control session with the expected typed runtime kind and
// this connection's identity; a kind that is merely registered, whose journal
// row outlived its process, or whose ownership verdict is missing (old
// daemon), false, or mismatched stays disconnected or connecting. A confirmed
// app-side disablement gates stale daemon evidence, and true unknown/error
// evidence is reported explicitly.
func remoteAgentState(kind string, evidence remoteRuntimeEvidence, stored map[string]any, row map[string]any, choiceEnabled, choicePresent bool) (state, detail string, agentID any, daemonMode, available bool) {
	state, detail = "disconnected", ""
	daemonMode = evidence.controlPresent
	if kind == "hermes" {
		rowState, _ := row["state"].(string)
		rowDetail, _ := row["detail"].(string)
		if rowState != "" {
			state, detail = rowState, rowDetail
		}
		daemonMode = state == "connected" || state == "connecting" || evidence.controlPresent
	}
	if stored != nil {
		agentID = stored["id"]
	}
	// Live ownership is only ever proven by an explicit verdict for this
	// kind's own control session, with the typed runtime kind and this
	// connection's identity all matching. A missing (old daemon), false, or
	// mismatched verdict is never upgraded by the global ownership count, and
	// an unknown status enum cannot confirm a live attachment either.
	liveOwned := evidence.controlOwnedKnown && evidence.controlOwned && evidence.controlRuntimeMatches && evidence.controlConnectionMatches && evidence.controlStatusKnown
	switch {
	case !evidence.statusKnown:
		// The daemon epoch is unconfirmable; no stored projection may keep
		// claiming a live connection, though a previously confirmed row may
		// still be retried.
		if kind == "hermes" {
			if state == "connected" || state == "connecting" {
				state, detail = "connecting", "小内核状态未知；等待原生状态确认。"
				available = true
			}
		} else {
			detail = "小内核状态未知；无法确认远端运行时。"
		}
	case evidence.controlPresent && !evidence.controlStatusKnown:
		// The daemon returned an enum this build does not understand: report
		// the ambiguity explicitly instead of guessing a state.
		state, detail = "connecting", "小内核返回了未知的控制会话状态；等待状态收敛。"
		available = evidence.registered
	case evidence.controlStatus == sessiondv1.SessionStatus_SESSION_STATUS_ERROR:
		state, detail = "error", "远端运行时控制会话异常；原生状态需要核对。"
		// Registration still permits an explicit retry/reconnect attempt.
		available = evidence.registered
	case choicePresent && !choiceEnabled:
		// A confirmed disablement gates stale daemon evidence, including live
		// ownership: the snapshot may not claim success after an explicit
		// disconnect until the daemon confirms a new attachment.
		if state == "connected" || state == "connecting" {
			state, detail = "disconnected", "已确认断开；等待小内核状态收敛。"
		}
	case kind == "hermes":
		// The app-owned SSH row is authoritative for remote Hermes only while
		// live ownership confirms it; an explicit release or an unconfirmed
		// verdict demotes the stored claim instead of faking connected.
		switch {
		case liveOwned:
			state, detail = "connected", "远端运行时已由小内核持有。"
			available = true
		case state == "connected" || state == "connecting":
			if evidence.controlOwnedKnown && !evidence.controlOwned {
				state, detail = "disconnected", "小内核已释放远端运行时；等待状态收敛。"
			} else {
				state, detail = "connecting", "小内核未确认远端运行时所有权；等待原生状态确认。"
				available = true
			}
		}
	case liveOwned:
		state, detail = "connected", "远端运行时已由小内核持有。"
		available = true
	}
	return state, detail, agentID, daemonMode, available
}

// remoteEnvironmentAgents projects one SSH environment. Each kind uses its own
// connection/kind daemon evidence, choice, and stored projection; no kind
// inherits another kind's or another connection's state.
func (h *Handler) remoteEnvironmentAgents(ctx context.Context, connID string, row map[string]any, cached discoveryResult, discovered bool) ([]map[string]any, error) {
	evidence, err := h.collectRemoteEvidence(ctx, connID)
	if err != nil {
		return nil, err
	}
	stored, err := h.storedConnectionAgents(ctx, connID)
	if err != nil {
		return nil, err
	}
	agents := make([]map[string]any, 0, len(environmentKinds))
	for _, kind := range environmentKinds {
		if kind == "antigravity" {
			agents = append(agents, map[string]any{"kind": kind, "available": false, "state": "unsupported", "detail": "Antigravity 暂不支持 SSH 接入。", "daemon_mode": true})
			continue
		}
		choiceEnabled, choicePresent, err := h.store.GetAgentConnectionChoice(ctx, connID+":"+kind)
		if err != nil {
			return nil, err
		}
		state, detail, agentID, daemonMode, available := remoteAgentState(kind, evidence[kind], stored[kind], row, choiceEnabled, choicePresent)
		if discovered {
			if probeAvailable, probed := cached.Available[kind]; probed && probeAvailable {
				available = true
			}
		}
		if kind == "hermes" && !discovered && state == "connected" {
			// The stored row is the app-owned confirmation for remote Hermes;
			// discovery availability must not downgrade it.
			available = true
		}
		var executable any
		if explicit := explicitRemoteExecutable(kind, row); explicit != "" {
			executable = explicit
		} else if cached.Executables != nil && cached.Executables[kind] != "" {
			executable = cached.Executables[kind]
		} else if discovered && h.executables != nil {
			if resolved, err := h.executables.Resolve(ctx, kind, row); err == nil && resolved != "" {
				executable = resolved
			}
		}
		agents = append(agents, map[string]any{
			"kind": kind, "available": available, "state": state,
			"agent_id": agentID, "detail": detail, "daemon_mode": daemonMode,
			"executable": executable,
		})
	}
	return agents, nil
}

// remoteEnvironmentItem renders the shared environment item contract used by
// both the listing and the discover response.
func (h *Handler) remoteEnvironmentItem(ctx context.Context, connID string, row map[string]any, cached discoveryResult, discovered bool) (map[string]any, error) {
	name, _ := row["display_name"].(string)
	if name == "" {
		name = connID
	}
	agents, err := h.remoteEnvironmentAgents(ctx, connID, row, cached, discovered)
	if err != nil {
		return nil, err
	}
	item := map[string]any{
		"id": connID, "name": name, "method": "ssh", "kind": "ssh",
		"discovered": discovered, "os": nil,
		"agents": agents,
	}
	if discovered && cached.OS != "" {
		item["os"] = cached.OS
	}
	return item, nil
}

// localEnvironmentItem renders the local environment from daemon-authoritative
// runtime registration plus the stored Agent projection. A previous local
// discovery may additionally surface real CLI availability, but registration
// is never downgraded by it.
func (h *Handler) localEnvironmentItem(ctx context.Context) (map[string]any, error) {
	cached, discovered := h.discovery.get(discoveryKey("local", nil))
	agents := make([]map[string]any, 0, len(environmentKinds))
	for _, kind := range environmentKinds {
		state, err := h.localConnection(ctx, kind)
		if err != nil {
			return nil, err
		}
		available := state["available"] == true
		if discovered && cached.Available[kind] {
			available = true
		}
		var executable any
		if cached.Executables != nil && cached.Executables[kind] != "" {
			executable = cached.Executables[kind]
		} else if available {
			if execPath, err := localAgentExecutable(ctx, kind); err == nil && execPath != "" {
				if abs, err := filepath.Abs(execPath); err == nil {
					executable = abs
				} else {
					executable = execPath
				}
			}
		}
		agents = append(agents, map[string]any{
			"kind": kind, "available": available, "state": state["state"],
			"agent_id": state["agent_id"], "detail": state["detail"],
			"daemon_mode": state["daemon_mode"] == true,
			"executable":  executable,
		})
	}
	return map[string]any{
		"id": "local", "name": "本机", "method": "local", "kind": "local",
		"discovered": true, "os": nil, "agents": agents,
	}, nil
}

// environmentSnapshot builds the environments page contract from the
// daemon-authoritative local runtimes, stored Agent projections, and cached
// discovery results. It never starts a process, session, or external CLI run.
func (h *Handler) environmentSnapshot(ctx context.Context) ([]map[string]any, error) {
	rows, err := h.store.ListSSHConnections(ctx)
	if err != nil {
		return nil, err
	}
	local, err := h.localEnvironmentItem(ctx)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{local}
	for _, row := range rows {
		connID, _ := row["id"].(string)
		if connID == "" {
			continue
		}
		cached, discovered := h.discovery.get(discoveryKey(connID, row))
		item, err := h.remoteEnvironmentItem(ctx, connID, row, cached, discovered)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// probeLocal executes real availability probes for the local Agent CLIs.
func (h *Handler) probeLocal(ctx context.Context) discoveryResult {
	result := discoveryResult{Versions: map[string]string{}, Available: map[string]bool{}, Executables: map[string]string{}}
	for _, kind := range environmentKinds {
		version, _, err := h.versioner.GetVersion(ctx, kind, "local", nil)
		if err != nil {
			result.Available[kind] = false
			continue
		}
		result.Available[kind] = true
		result.Versions[kind] = version
		if execPath, err := localAgentExecutable(ctx, kind); err == nil && execPath != "" {
			if abs, err := filepath.Abs(execPath); err == nil {
				result.Executables[kind] = abs
			} else {
				result.Executables[kind] = execPath
			}
		}
	}
	return result
}

// probeRemote executes real version probes plus one OS probe against an SSH
// environment. It reports whether any probe answered, so a fully unreachable
// host is not reported as a successful discovery.
func (h *Handler) probeRemote(ctx context.Context, connID string, conn map[string]any) (discoveryResult, bool) {
	result := discoveryResult{Versions: map[string]string{}, Available: map[string]bool{}, Executables: map[string]string{}}
	answered := false
	for _, kind := range environmentKinds {
		if kind == "antigravity" {
			continue
		}
		version, _, err := h.versioner.GetVersion(ctx, kind, connID, conn)
		if err != nil {
			result.Available[kind] = false
			continue
		}
		result.Available[kind] = true
		result.Versions[kind] = version
		answered = true
		if h.executables != nil {
			if execPath, err := h.executables.Resolve(ctx, kind, conn); err == nil && execPath != "" {
				result.Executables[kind] = execPath
			}
		}
	}
	if h.osProber != nil {
		if os, err := h.osProber.ProbeOS(ctx, connID, conn); err == nil {
			result.OS = os
			answered = true
		}
	}
	return result, answered
}
