// Package antigravity hosts the official agy acp (Agent Client Protocol)
// JSON-RPC stdio transport. Conversation bodies remain in Antigravity's own
// SQLite store; live streaming events, dynamic model/mode/thought controls,
// and interactive permission requests run over official ACP.
package antigravity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

type Config struct {
	Executable             string
	Arguments, Environment []string
	Workspace              string
	Allowed                []string
	AgentID                string
	AgentName              string
	ConnectionID           string
	Home                   string
	Emit                   func(string, string, map[string]any, string) error
}

type Adapter struct {
	mu             sync.Mutex
	acpMu          sync.Mutex
	config         Config
	client         *acpClient
	sessions       map[string]*session
	earlyUpdates   map[string][]map[string]any
	opening        map[string]bool
	closed         bool
	catalog        map[string]string
	sshStarter     SSHStarter
	remoteHelper   RemoteHelperFunc
	quotaMu        sync.Mutex
	tierInfo       map[string]any
	modelsMu       sync.Mutex
	modelsCache    []map[string]any
	modelsCachedAt time.Time
	usageCacheMu   sync.Mutex
	usageCache     map[string]cachedConvEntry
}

type session struct {
	mu                sync.Mutex
	id, cwd, status   string
	model, effort     string
	mode, command     string
	control, closed   bool
	readonly          bool
	deleteOnly         bool
	deletePending      bool
	promptRunning      bool
	reactiveWaiting    bool
	reactiveMonitorGen uint64
	cancelRequested    bool
	cancelAt           time.Time
	lastUpdateAt       time.Time
	suppressReplay     bool
	client            *acpClient
	models            []map[string]any
	thoughtLevels     []string
	availableCommands []map[string]any
	approvals         map[string]*pendingApproval
	toolItems         map[string]*acpToolState
	liveSeq           int
	activeMsgID       string
	activeText        strings.Builder
	activeThinking    strings.Builder
	lastUsedTokens    int64
	contextWindow     int64
	hasCumMetrics     bool
	cumInputTokens    int64
	cumOutputTokens   int64
	cumCachedTokens   int64
	cumReasonTokens   int64
	cumTotalTokens    int64
	isResumed         bool
	lastUsageSync     time.Time

	eventCh   chan func()
	eventStop chan struct{}
	eventDone chan struct{}
	stopOnce  sync.Once
}

func newSession(id, cwd string, c *acpClient) *session {
	s := &session{
		id:        id,
		cwd:       cwd,
		status:    "idle",
		mode:      "manual",
		client:    c,
		approvals: make(map[string]*pendingApproval),
		toolItems: make(map[string]*acpToolState),
		eventCh:   make(chan func(), 512),
		eventStop: make(chan struct{}),
		eventDone: make(chan struct{}),
	}
	go s.runEventWorker()
	return s
}

func (s *session) runEventWorker() {
	defer close(s.eventDone)
	for {
		select {
		case fn := <-s.eventCh:
			if fn != nil {
				fn()
			}
		case <-s.eventStop:
			for {
				select {
				case fn := <-s.eventCh:
					if fn != nil {
						fn()
					}
				default:
					return
				}
			}
		}
	}
}

func (s *session) enqueue(fn func()) {
	if s == nil || s.eventCh == nil {
		fn()
		return
	}
	select {
	case <-s.eventStop:
		return
	default:
	}
	select {
	case s.eventCh <- fn:
	case <-s.eventStop:
	}
}

func (s *session) stopWorker() {
	if s == nil || s.eventStop == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.eventStop)
	})
}

func (s *session) nextLiveIDLocked() string {
	s.liveSeq++
	return fmt.Sprintf("antigravity:%s:live:%d", s.id, s.liveSeq)
}

func (s *session) hasTransport() bool {
	return s.client != nil
}

func (s *session) transportExited() bool {
	if s.control || s.readonly || s.deleteOnly {
		return false
	}
	if s.client == nil {
		return false
	}
	return s.client.exited()
}

func (s *session) remoteExitConfirmed() bool {
	if s.client == nil {
		return true
	}
	return s.client.remoteExitConfirmed()
}

func New(c Config) *Adapter {
	if c.AgentID == "" {
		if c.ConnectionID != "" {
			c.AgentID = fmt.Sprintf("ssh-antigravity-%s", c.ConnectionID)
		} else {
			c.AgentID = "local-antigravity"
		}
	}
	if c.Home == "" && c.ConnectionID == "" {
		c.Home, _ = os.UserHomeDir()
	}
	return &Adapter{
		config:       c,
		sessions:     map[string]*session{},
		earlyUpdates: map[string][]map[string]any{},
		opening:      map[string]bool{},
		catalog:      map[string]string{},
	}
}

func (a *Adapter) agentName() string {
	if a.config.AgentName != "" {
		return a.config.AgentName
	}
	return "Antigravity"
}

func (a *Adapter) probeSessionID() string {
	if a.config.ConnectionID != "" {
		return "probe-antigravity-" + a.config.ConnectionID
	}
	return "probe-antigravity-local"
}

func isWindowsHostPath(raw string) bool {
	if len(raw) >= 2 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' {
		return true
	}
	return strings.HasPrefix(raw, `\\`)
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	v, _ := m[k].(string)
	return v
}

func options(r core.Request) map[string]any {
	p, _ := r.Fields["params"].(map[string]any)
	if p == nil {
		if r.Fields == nil {
			return map[string]any{}
		}
		return r.Fields
	}
	out := map[string]any{}
	for k, v := range p {
		out[k] = v
	}
	for k, v := range r.Fields {
		if k != "params" {
			out[k] = v
		}
	}
	return out
}

func (a *Adapter) allowedRemote(clean string) bool {
	for _, root := range a.config.Allowed {
		if !strings.HasPrefix(root, "/") {
			continue
		}
		cleanRoot := path.Clean(root)
		if cleanRoot == "/" || clean == cleanRoot || strings.HasPrefix(clean, cleanRoot+"/") {
			return true
		}
	}
	return false
}

func (a *Adapter) workspace(raw string) (string, error) {
	if a.sshStarter != nil || a.config.ConnectionID != "" {
		if strings.ContainsRune(raw, 0) {
			return "", fmt.Errorf("remote Antigravity workspace contains null byte")
		}
		if raw == "" || isWindowsHostPath(raw) {
			if strings.HasPrefix(a.config.Workspace, "/") {
				raw = a.config.Workspace
			} else {
				raw = "/"
			}
		}
		if !strings.HasPrefix(raw, "/") {
			return "", fmt.Errorf("remote Antigravity workspace must be an absolute POSIX path")
		}
		clean := path.Clean(raw)
		if !a.allowedRemote(clean) {
			return "", fmt.Errorf("Antigravity workspace is outside allowlist")
		}
		return clean, nil
	}
	if raw == "" {
		raw = a.config.Workspace
	}
	p, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(p)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("Antigravity workspace must be an existing directory")
	}
	for _, root := range a.config.Allowed {
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, e := filepath.Rel(root, p)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel) {
			return p, nil
		}
	}
	return "", fmt.Errorf("Antigravity workspace is outside allowlist")
}

func isInternalWorkerModel(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "-worker")
}

func (a *Adapter) result(s *session) core.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "owned"
	if s.readonly {
		state = "readonly"
	}
	payload := map[string]any{
		"session_id":        s.id,
		"source_session_id": s.id,
		"agent_id":          a.config.AgentID,
		"source_id":         a.config.AgentID,
		"runtime_id":        a.config.AgentID,
		"name":              a.agentName(),
		"runtime_control":   s.control,
		"control_state":     state,
		"workspace":         s.cwd,
		"model":             s.model,
		"effort":            s.effort,
	}
	if a.config.ConnectionID != "" {
		payload["connection_id"] = a.config.ConnectionID
	}
	return core.Result{SessionID: s.id, Status: s.status, Payload: payload}
}

func (a *Adapter) emit(id, kind string, p map[string]any, status string) error {
	if a.config.Emit == nil {
		return nil
	}
	return a.config.Emit(id, kind, p, status)
}

func (a *Adapter) event(s *session, kind string, data map[string]any, status string) error {
	return a.emit(s.id, "connector.event", map[string]any{
		"agent_id":   a.config.AgentID,
		"session_id": s.id,
		"type":       kind,
		"data":       data,
	}, status)
}

func (a *Adapter) announce(s *session) error {
	if s.control || s.deleteOnly {
		return nil
	}
	s.mu.Lock()
	model, effort, status, readonly := s.model, s.effort, s.status, s.readonly
	cwd := s.cwd
	s.mu.Unlock()
	ctrlState := "owned"
	if readonly {
		ctrlState = "readonly"
	}
	data := map[string]any{
		"history_state":     "available",
		"native_kind":       "rpc",
		"model":             model,
		"effort":            effort,
		"provider":          "antigravity",
		"control_state":     ctrlState,
		"id":                s.id,
		"agent_id":          a.config.AgentID,
		"source_id":         a.config.AgentID,
		"source_session_id": s.id,
		"workspace":         cwd,
		"status":            status,
		"updated_at":        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if a.config.ConnectionID != "" {
		data["connection_id"] = a.config.ConnectionID
	}
	return a.event(s, "session.upsert", data, status)
}

func (a *Adapter) emitOwner(s *session, status, errReason string) error {
	s.mu.Lock()
	c := s.client
	id := s.id
	s.mu.Unlock()
	if c == nil || id == "" {
		return nil
	}
	pid := c.pid()
	if pid <= 0 && c.ssh == nil {
		return nil
	}
	ownerPayload := map[string]any{
		"agent_id":      a.config.AgentID,
		"session_id":    id,
		"runtime_owned": true,
		"status":        status,
	}
	if pid > 0 {
		ownerPayload["owner_pid"] = pid
	}
	if errReason != "" {
		ownerPayload["error"] = errReason
	}
	if a.config.ConnectionID != "" {
		ownerPayload["connection_id"] = a.config.ConnectionID
	}
	return a.emit(id, "runtime.owner", ownerPayload, status)
}

func parseACPAvailableModels(raw any, thoughtLevels []string) []map[string]any {
	modelsMap, _ := raw.(map[string]any)
	if modelsMap == nil {
		return nil
	}
	avail, _ := modelsMap["availableModels"].([]any)
	if len(avail) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(avail))
	for _, elem := range avail {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		id := firstNonEmptyStr(m, "modelId", "id", "model")
		if id == "" {
			continue
		}
		label := firstNonEmptyStr(m, "name", "label", "displayName")
		if label == "" {
			label = id
		}
		efforts := []string{}
		if len(thoughtLevels) > 0 && modelSupportsThoughtLevel(id, label) {
			efforts = append(efforts, thoughtLevels...)
		}
		items = append(items, map[string]any{
			"id":       id,
			"model":    id,
			"name":     label,
			"label":    label,
			"provider": "antigravity",
			"efforts":  efforts,
		})
	}
	return items
}

func modelSupportsThoughtLevel(id, label string) bool {
	lowerID := strings.ToLower(id)
	lowerLabel := strings.ToLower(label)
	for _, suffix := range []string{"-low", "-medium", "-high"} {
		if strings.HasSuffix(lowerID, suffix) {
			return true
		}
	}
	return strings.Contains(lowerID, "thinking") || strings.Contains(lowerLabel, "thinking")
}

func (s *session) applyConfigOptionsLocked(opts []any) {
	foundThought := false
	baseModel := ""
	for _, elem := range opts {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		switch str(m, "id") {
		case "model":
			baseModel = strings.TrimSpace(str(m, "currentValue"))
		case "mode":
			if curMode := strings.TrimSpace(str(m, "currentValue")); curMode != "" {
				if !(s.mode == "full_access" && curMode == "auto") {
					if mapped := fromACPMode(curMode); mapped != "" {
						s.mode = mapped
					}
				}
			}
		case "thought_level":
			foundThought = true
			s.effort = strings.TrimSpace(str(m, "currentValue"))
			levels := []string{}
			if optList, ok := m["options"].([]any); ok {
				for _, optRaw := range optList {
					if optMap, ok := optRaw.(map[string]any); ok {
						if val := strings.TrimSpace(str(optMap, "value")); val != "" {
							levels = append(levels, val)
						}
					}
				}
			}
			s.thoughtLevels = levels
		}
	}
	if !foundThought {
		s.thoughtLevels = nil
		s.effort = ""
	}
	if baseModel != "" && s.effort != "" {
		cand := baseModel + "-" + s.effort
		for _, m := range s.models {
			if str(m, "id") == cand {
				s.model = cand
				break
			}
		}
	}
}

func (a *Adapter) ingestSessionResponse(s *session, resp map[string]any) {
	s.mu.Lock()
	if modelsObj, ok := resp["models"].(map[string]any); ok {
		if cur := strings.TrimSpace(str(modelsObj, "currentModelId")); cur != "" {
			s.model = cur
		}
	}
	if modesObj, ok := resp["modes"].(map[string]any); ok {
		if curMode := strings.TrimSpace(str(modesObj, "currentModeId")); curMode != "" {
			if mapped := fromACPMode(curMode); mapped != "" {
				s.mode = mapped
			}
		}
	}
	if cfgOpts, ok := resp["configOptions"].([]any); ok {
		s.applyConfigOptionsLocked(cfgOpts)
	}
	parsedModels := parseACPAvailableModels(resp["models"], s.thoughtLevels)
	if len(parsedModels) > 0 {
		s.models = parsedModels
	}
	// Re-apply config options once s.models is populated so baseModel+effort resolves cleanly.
	if cfgOpts, ok := resp["configOptions"].([]any); ok {
		s.applyConfigOptionsLocked(cfgOpts)
	}
	s.mu.Unlock()

	if len(parsedModels) > 0 {
		a.modelsMu.Lock()
		a.modelsCache = cloneModelsSlice(parsedModels)
		a.modelsCachedAt = time.Now()
		a.modelsMu.Unlock()
	}

	if metaObj, ok := resp["_meta"].(map[string]any); ok {
		if agyMeta, ok := metaObj["antigravity"].(map[string]any); ok {
			if tierObj, ok := agyMeta["tier"].(map[string]any); ok && len(tierObj) > 0 {
				a.setTierInfo(tierObj)
			}
		}
	}
}

func (a *Adapter) Create(ctx context.Context, r core.Request) (core.Result, error) {
	p := options(r)
	if str(p, "parent_session_id") != "" {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity ACP does not support native conversation fork")
	}
	return a.openSession(ctx, "", p, nil)
}

func (a *Adapter) Spawn(ctx context.Context, r core.Request) (core.Result, error) {
	p := options(r)
	if rc, _ := p["runtime_control"].(bool); rc {
		if r.SessionID == "" {
			return core.Result{}, fmt.Errorf("control identity required")
		}
		if _, err := a.ensureClient(ctx); err != nil {
			return core.Result{}, err
		}
		cwd := a.config.Workspace
		if validated, err := a.workspace(cwd); err == nil {
			cwd = validated
		}
		s := &session{id: r.SessionID, cwd: cwd, status: "idle", control: true}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.closed {
			return core.Result{}, fmt.Errorf("Antigravity adapter closed")
		}
		if old := a.sessions[s.id]; old != nil {
			return a.result(old), nil
		}
		a.sessions[s.id] = s
		return a.result(s), nil
	}

	if !validID(r.SessionID) {
		return core.Result{}, fmt.Errorf("invalid Antigravity conversation ID")
	}
	a.mu.Lock()
	previous := a.sessions[r.SessionID]
	a.mu.Unlock()

	if delOnly, _ := p["delete_only"].(bool); delOnly {
		if previous != nil && !previous.deleteOnly && !previous.transportExited() {
			return a.result(previous), nil
		}
		cwd := a.config.Workspace
		if validated, wErr := a.workspace(str(p, "cwd")); wErr == nil {
			cwd = validated
		}
		s := &session{id: r.SessionID, cwd: cwd, status: "idle", deleteOnly: true}
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			return core.Result{}, fmt.Errorf("Antigravity adapter closed")
		}
		if cur := a.sessions[r.SessionID]; cur != nil && cur != previous {
			a.mu.Unlock()
			return a.result(cur), nil
		}
		a.sessions[s.id] = s
		a.mu.Unlock()
		return a.result(s), nil
	}

	if previous != nil && !previous.deleteOnly && !previous.transportExited() {
		return core.Result{}, fmt.Errorf("Antigravity transport has not exited; recovery is unsafe")
	}

	exitedOwner := previous != nil && !previous.deleteOnly && previous.remoteExitConfirmed()
	desktopReadonly, err := a.inspectResumeTarget(ctx, r.SessionID, exitedOwner)
	if desktopReadonly {
		cwd := a.config.Workspace
		if validated, wErr := a.workspace(str(p, "cwd")); wErr == nil {
			cwd = validated
		}
		s := &session{id: r.SessionID, cwd: cwd, status: "idle", readonly: true}
		a.mu.Lock()
		if a.closed || (a.sessions[s.id] != nil && a.sessions[s.id] != previous) {
			a.mu.Unlock()
			return core.Result{}, fmt.Errorf("Antigravity attach unavailable")
		}
		a.sessions[s.id] = s
		a.mu.Unlock()
		return a.result(s), nil
	}
	if err != nil {
		return core.Result{}, err
	}

	return a.openSession(ctx, r.SessionID, p, previous)
}

func (a *Adapter) openSession(ctx context.Context, id string, p map[string]any, previous *session) (core.Result, error) {
	cwd, err := a.workspace(str(p, "cwd"))
	if err != nil && id != "" && a.sshStarter == nil && str(p, "cwd") != "" && errors.Is(wUnwrap(err), os.ErrNotExist) {
		cwd, err = a.workspace("")
	}
	if err != nil {
		return core.Result{}, err
	}

	reqModel := strings.TrimSpace(str(p, "model"))
	if id != "" && isInternalWorkerModel(reqModel) {
		reqModel = ""
	}
	reqEffort := strings.TrimSpace(str(p, "effort"))
	reqMode, err := parseRequestedApprovalMode(p)
	if err != nil {
		return core.Result{}, err
	}

	openKey := id
	if openKey == "" {
		openKey = fmt.Sprintf("create-%d", time.Now().UnixNano())
	}
	a.mu.Lock()
	if a.closed || a.opening[openKey] {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("Antigravity attach unavailable")
	}
	if id != "" && a.sessions[id] != previous {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("Antigravity conversation already attached")
	}
	a.opening[openKey] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.opening, openKey)
		a.mu.Unlock()
	}()

	c, err := a.ensureClient(ctx)
	if err != nil {
		return core.Result{}, err
	}

	var resp map[string]any
	var sess *session
	if id == "" {
		resp, err = c.call(ctx, "session/new", map[string]any{
			"cwd":        cwd,
			"mcpServers": []any{},
		})
		if err != nil {
			return core.Result{}, mapRPCErrorToPreDispatch(err)
		}
		sid := str(resp, "sessionId")
		if !validID(sid) {
			return core.Result{}, fmt.Errorf("Antigravity ACP did not confirm a valid session ID")
		}
		sess = newSession(sid, cwd, c)
		a.ingestSessionResponse(sess, resp)
	} else {
		sess = newSession(id, cwd, c)
		sess.isResumed = true
		if c.hasResume || !c.hasLoad {
			resp, err = c.call(ctx, "session/resume", map[string]any{
				"sessionId":  id,
				"cwd":        cwd,
				"mcpServers": []any{},
			})
			if err != nil && ctx.Err() == nil {
				if fbCwd, fbErr := a.workspace(""); fbErr == nil && fbCwd != cwd {
					if fbResp, retryErr := c.call(ctx, "session/resume", map[string]any{
						"sessionId":  id,
						"cwd":        fbCwd,
						"mcpServers": []any{},
					}); retryErr == nil {
						cwd = fbCwd
						sess.cwd = fbCwd
						resp = fbResp
						err = nil
					}
				}
			}
		} else {
			sess.mu.Lock()
			sess.suppressReplay = true
			sess.mu.Unlock()
			a.mu.Lock()
			a.sessions[id] = sess
			a.mu.Unlock()
			resp, err = c.call(ctx, "session/load", map[string]any{
				"sessionId":  id,
				"cwd":        cwd,
				"mcpServers": []any{},
			})
			sess.mu.Lock()
			sess.suppressReplay = false
			sess.mu.Unlock()
		}
		if err != nil {
			sess.stopWorker()
			if previous != nil {
				a.mu.Lock()
				if a.sessions[id] == sess {
					a.sessions[id] = previous
				}
				a.mu.Unlock()
			}
			return core.Result{}, mapRPCErrorToPreDispatch(err)
		}
		if confirmedID := str(resp, "sessionId"); confirmedID != "" && confirmedID != id {
			sess.stopWorker()
			return core.Result{}, fmt.Errorf("Antigravity ACP resumed mismatched session ID")
		}
		a.ingestSessionResponse(sess, resp)
	}

	cleanupNew := func() {
		sess.stopWorker()
		if c.hasClose {
			cCtx, cCancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = c.call(cCtx, "session/close", map[string]any{"sessionId": sess.id})
			cCancel()
		}
	}

	if reqModel != "" {
		if err := a.applyModelOnOpen(ctx, sess, c, reqModel); err != nil {
			cleanupNew()
			return core.Result{}, err
		}
	}
	if reqEffort != "" {
		if err := a.applyEffortOnOpen(ctx, sess, c, reqEffort); err != nil {
			cleanupNew()
			return core.Result{}, err
		}
	}
	if reqMode != "" {
		if err := a.applyModeOnOpen(ctx, sess, c, reqMode); err != nil {
			cleanupNew()
			return core.Result{}, err
		}
	}

	if id == "" {
		sess.mu.Lock()
		emptyModel := sess.model == ""
		sess.mu.Unlock()
		if emptyModel {
			if defModel := a.resolveDefaultModel(ctx); defModel != "" {
				sess.mu.Lock()
				if sess.model == "" {
					sess.model = defModel
				}
				sess.mu.Unlock()
			}
		}
	}

	a.mu.Lock()
	if a.closed || (id != "" && a.sessions[sess.id] != previous && a.sessions[sess.id] != sess) || (id == "" && a.sessions[sess.id] != nil) {
		delete(a.earlyUpdates, sess.id)
		a.mu.Unlock()
		cleanupNew()
		return core.Result{}, fmt.Errorf("Antigravity identity already owned or adapter closed")
	}
	a.sessions[sess.id] = sess
	early := a.earlyUpdates[sess.id]
	delete(a.earlyUpdates, sess.id)
	a.mu.Unlock()

	for _, upd := range early {
		a.handleSessionUpdate(sess, upd)
	}

	if previous != nil && previous != sess {
		previous.stopWorker()
	}

	if id != "" {
		go func(target *session) {
			uCtx, uCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer uCancel()
			a.emitSessionUsageSnapshot(uCtx, target)
		}(sess)
	}

	_ = a.emitOwner(sess, "idle", "")
	if err := a.announce(sess); err != nil {
		return core.Result{}, err
	}
	return a.result(sess), nil
}

func wUnwrap(err error) error { return err }

func (a *Adapter) handleClientExit(c *acpClient, reason string) {
	a.mu.Lock()
	affected := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		if s != nil && s.client == c && !s.control && !s.readonly && !s.deleteOnly {
			affected = append(affected, s)
		}
	}
	a.mu.Unlock()

	for _, s := range affected {
		s.mu.Lock()
		wasClosed := s.closed
		s.mu.Unlock()
		if !wasClosed {
			a.failSession(s, reason)
		}
	}
}

func (s *session) hasPendingAskQuestionLocked() bool {
	for _, st := range s.toolItems {
		if st != nil && st.name == "ask_question" && st.acpStatus == "pending" {
			return true
		}
	}
	return false
}

func (s *session) hasBackgroundToolOutputLocked() bool {
	for _, st := range s.toolItems {
		if st == nil {
			continue
		}
		if strings.Contains(st.rawOutput, "Tool is running as a background task") {
			return true
		}
		if (st.name == "schedule" || st.name == "invoke_subagent" || st.name == "run_workflow") && st.acpStatus != "failed" && st.acpStatus != "error" && st.acpStatus != "cancelled" {
			return true
		}
	}
	return false
}

func (a *Adapter) failSession(s *session, reason string) {
	s.mu.Lock()
	s.status = "error"
	s.promptRunning = false
	s.reactiveWaiting = false
	s.cancelRequested = false
	s.reactiveMonitorGen++
	command := s.command
	s.command = ""
	id := s.id
	pending := s.approvals
	s.approvals = make(map[string]*pendingApproval)
	s.mu.Unlock()

	if id == "" {
		return
	}
	for appID, appr := range pending {
		_ = a.event(s, "approval.upsert", map[string]any{
			"id":         appID,
			"agent_id":   a.config.AgentID,
			"session_id": id,
			"state":      "rejected",
			"native_id":  appr.rawID,
		}, "")
	}
	if command != "" {
		_ = a.event(s, "command.upsert", map[string]any{"id": command, "state": "unknown", "error": reason}, "")
	}
	_ = a.announce(s)
	_ = a.emitOwner(s, "error", reason)
}

func (a *Adapter) runPrompt(s *session, c *acpClient, commandID string, blocks []map[string]any) {
	resp, err := c.call(context.Background(), "session/prompt", map[string]any{
		"sessionId": s.id,
		"prompt":    blocks,
	})

	s.enqueue(func() {
		s.mu.Lock()
		if s.closed || s.client != c {
			s.mu.Unlock()
			return
		}
		// If handleClientExit already marked error on transport EOF, do not overwrite with idle.
		if c.exited() && err != nil {
			s.mu.Unlock()
			return
		}

		stopReason := str(resp, "stopReason")
		nextStatus := "idle"
		cmdState := "completed"
		var cmdErr any
		if err != nil {
			var rpcErr *acpRPCError
			if asACPRPCError(err, &rpcErr) && strings.Contains(strings.ToLower(rpcErr.Message), "cancel") {
				nextStatus = "idle"
				cmdState = "failed"
				cmdErr = rpcErr.Message
			} else {
				nextStatus = "error"
				cmdState = "failed"
				cmdErr = err.Error()
			}
		} else if stopReason == "cancelled" || stopReason == "canceled" || s.cancelRequested {
			nextStatus = "idle"
			cmdState = "failed"
			cmdErr = "cancelled"
		}

		awaitingInput := err == nil && cmdState == "completed" && s.hasPendingAskQuestionLocked()
		hasBgTool := err == nil && cmdState == "completed" && !awaitingInput && s.hasBackgroundToolOutputLocked()

		s.promptRunning = false
		s.activeMsgID = ""
		s.activeText.Reset()
		s.activeThinking.Reset()

		pending := s.approvals
		s.approvals = make(map[string]*pendingApproval)
		sid := s.id
		s.mu.Unlock()

		for appID, appr := range pending {
			_ = a.event(s, "approval.upsert", map[string]any{
				"id":         appID,
				"agent_id":   a.config.AgentID,
				"session_id": sid,
				"state":      "rejected",
				"native_id":  appr.rawID,
			}, "")
		}
		_ = a.emitUsage(s, resp)

		stillActive := false
		if err == nil && cmdState == "completed" && !awaitingInput {
			checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
			stillActive = a.isConversationStillActive(checkCtx, sid) || hasBgTool
			checkCancel()
		}

		s.mu.Lock()
		if s.closed || s.client != c {
			s.mu.Unlock()
			return
		}
		if s.hasPendingAskQuestionLocked() {
			awaitingInput = true
			stillActive = false
		}
		if s.cancelRequested && cmdState == "completed" {
			cmdState = "failed"
			cmdErr = "cancelled"
			stillActive = false
			awaitingInput = false
		}
		if stillActive {
			s.status = "running"
			s.reactiveWaiting = true
			s.reactiveMonitorGen++
			gen := s.reactiveMonitorGen
			s.lastUpdateAt = time.Now()
			s.mu.Unlock()
			go a.monitorReactiveCompletion(s, c, gen)
			return
		}

		s.reactiveWaiting = false
		s.cancelRequested = false
		s.command = ""
		s.status = nextStatus
		s.mu.Unlock()

		if commandID != "" {
			cmdPayload := map[string]any{
				"id":    commandID,
				"state": cmdState,
				"error": cmdErr,
			}
			if awaitingInput {
				cmdPayload["awaiting_input"] = true
			}
			_ = a.event(s, "command.upsert", cmdPayload, "")
		}
		_ = a.announce(s)
		_ = a.emitOwner(s, nextStatus, "")
	})
}

func (a *Adapter) monitorReactiveCompletion(s *session, c *acpClient, gen uint64) {
	delays := []time.Duration{
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1200 * time.Millisecond,
	}
	step := 0
	for {
		delay := delays[len(delays)-1]
		if step < len(delays) {
			delay = delays[step]
			step++
		}
		time.Sleep(delay)

		s.mu.Lock()
		if s.closed || s.client != c || !s.reactiveWaiting || s.reactiveMonitorGen != gen {
			s.mu.Unlock()
			return
		}
		sid := s.id
		lastUpd := s.lastUpdateAt
		cancelReq := s.cancelRequested
		cancelAt := s.cancelAt
		s.mu.Unlock()

		if c == nil || c.exited() {
			return
		}

		checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
		active := a.isConversationStillActive(checkCtx, sid)
		checkCancel()

		if active {
			if cancelReq && !cancelAt.IsZero() && time.Since(cancelAt) > 3*time.Second && time.Since(lastUpd) > 1500*time.Millisecond {
				active = false
			} else {
				continue
			}
		}

		if !lastUpd.IsZero() && time.Since(lastUpd) < 350*time.Millisecond {
			continue
		}

		done := make(chan bool, 1)
		s.enqueue(func() {
			s.mu.Lock()
			if s.closed || s.client != c || !s.reactiveWaiting || s.reactiveMonitorGen != gen {
				s.mu.Unlock()
				done <- true
				return
			}
			if !s.lastUpdateAt.IsZero() && time.Since(s.lastUpdateAt) < 300*time.Millisecond {
				s.mu.Unlock()
				done <- false
				return
			}
			awaitingInput := s.hasPendingAskQuestionLocked()
			wasCancelled := s.cancelRequested
			cmdID := s.command
			s.reactiveWaiting = false
			s.cancelRequested = false
			s.command = ""
			s.status = "idle"
			s.activeMsgID = ""
			s.activeText.Reset()
			s.activeThinking.Reset()
			s.mu.Unlock()

			uCtx, uCancel := context.WithTimeout(context.Background(), 4*time.Second)
			a.emitSessionUsageSnapshot(uCtx, s)
			uCancel()

			if cmdID != "" {
				cmdState := "completed"
				var cmdErr any
				if wasCancelled {
					cmdState = "failed"
					cmdErr = "cancelled"
				}
				cmdPayload := map[string]any{
					"id":    cmdID,
					"state": cmdState,
					"error": cmdErr,
				}
				if awaitingInput && !wasCancelled {
					cmdPayload["awaiting_input"] = true
				}
				_ = a.event(s, "command.upsert", cmdPayload, "")
			}
			_ = a.announce(s)
			_ = a.emitOwner(s, "idle", "")
			done <- true
		})

		select {
		case finished := <-done:
			if finished {
				return
			}
		case <-s.eventDone:
			return
		}
	}
}

func (a *Adapter) interrupt(s *session) (core.Result, error) {
	s.mu.Lock()
	if s.control || s.readonly || s.deleteOnly || s.closed {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity interrupt requires an owned CLI conversation")
	}
	if s.status != "running" && s.status != "waiting_approval" {
		res := a.resultUnlocked(s)
		s.mu.Unlock()
		res.Payload["accepted"] = true
		res.Payload["interrupted"] = false
		return res, nil
	}
	s.cancelRequested = true
	s.cancelAt = time.Now()
	c := s.client
	s.mu.Unlock()

	if c == nil || c.exited() {
		return core.Result{}, fmt.Errorf("Antigravity ACP transport is unavailable")
	}
	if err := c.notify("session/cancel", map[string]any{"sessionId": s.id}); err != nil {
		return core.Result{}, err
	}
	res := a.result(s)
	res.Payload["accepted"] = true
	res.Payload["interrupted"] = true
	return res, nil
}

func (a *Adapter) resultUnlocked(s *session) core.Result {
	state := "owned"
	if s.readonly {
		state = "readonly"
	}
	payload := map[string]any{
		"session_id":        s.id,
		"source_session_id": s.id,
		"agent_id":          a.config.AgentID,
		"source_id":         a.config.AgentID,
		"runtime_id":        a.config.AgentID,
		"name":              a.agentName(),
		"runtime_control":   s.control,
		"control_state":     state,
		"workspace":         s.cwd,
		"model":             s.model,
		"effort":            s.effort,
	}
	if a.config.ConnectionID != "" {
		payload["connection_id"] = a.config.ConnectionID
	}
	return core.Result{SessionID: s.id, Status: s.status, Payload: payload}
}

func (a *Adapter) Command(ctx context.Context, r core.Request) (core.Result, error) {
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	a.mu.Lock()
	s := a.sessions[r.SessionID]
	a.mu.Unlock()
	if s == nil {
		return core.Result{}, fmt.Errorf("Antigravity conversation not owned")
	}
	result := a.result(s)
	switch r.Action {
	case "session.delete":
		return a.deleteConversation(ctx, s)
	case "session.interrupt":
		return a.interrupt(s)
	case "session.approve":
		return a.respondApproval(s, options(r))
	case "session.models":
		s.mu.Lock()
		sessModels := cloneModelsSlice(s.models)
		s.mu.Unlock()
		if len(sessModels) > 0 {
			result.Payload["items"] = sessModels
			return result, nil
		}
		items, err := a.models(ctx)
		result.Payload["items"] = items
		return result, err
	case "session.commands":
		if s.control || s.deleteOnly || s.transportExited() {
			return core.Result{}, core.ErrSessionNotOwned
		}
		s.mu.Lock()
		cmds := cloneModelsSlice(s.availableCommands)
		s.mu.Unlock()
		result.Payload["items"] = cmds
		return result, nil
	case "session.model.read":
		if s.deleteOnly || s.transportExited() {
			return core.Result{}, core.ErrSessionNotOwned
		}
		s.mu.Lock()
		levels := append([]string{}, s.thoughtLevels...)
		s.mu.Unlock()
		result.Payload["provider"] = "antigravity"
		result.Payload["model_switch_supported"] = !s.control && !s.readonly
		result.Payload["reasoning_switch_supported"] = !s.control && !s.readonly && len(levels) > 0
		if len(levels) > 0 {
			result.Payload["supported_efforts"] = levels
		}
		result.Payload["read_only"] = s.readonly
		if s.readonly {
			result.Payload["detail"] = "此会话来自 Antigravity 桌面版，可读取历史；agy CLI 无法续聊，请新建 Antigravity 会话。"
		}
		return result, nil
	case "session.approval.read":
		return a.readApproval(s)
	case "session.approval.set":
		return a.switchApproval(ctx, s, options(r))
	case "session.model.set":
		return a.switchModel(ctx, s, options(r))
	case "session.reasoning.set":
		return a.switchReasoning(ctx, s, options(r))
	case "session.history_page":
		if s.control {
			return core.Result{}, fmt.Errorf("control binding has no history")
		}
		limit := 50
		if v, ok := intVal(r.Fields["limit"]); ok {
			limit = v
		}
		before := str(r.Fields, "before")
		items, next, err := a.readSessionHistory(ctx, s.id, before, limit, s.readonly)
		if err != nil && errors.Is(err, os.ErrNotExist) && !s.readonly && before == "" {
			items = []map[string]any{}
			next = nil
			err = nil
		}
		if err == nil && before == "" {
			uCtx, uCancel := context.WithTimeout(ctx, 4*time.Second)
			a.emitSessionUsageSnapshot(uCtx, s)
			uCancel()
		}
		result.Payload["items"] = items
		result.Payload["next_cursor"] = next
		result.Payload["has_more"] = next != nil
		return result, err
	case "session.send":
		if s.readonly {
			return core.Result{}, core.RejectBeforeDispatch("Antigravity desktop conversation is history-only; create a CLI conversation to chat")
		}
		if s.control {
			return core.Result{}, core.RejectBeforeDispatch("control binding cannot send")
		}
		if s.deleteOnly || s.transportExited() {
			return core.Result{}, core.ErrSessionNotOwned
		}
		p := options(r)
		for _, key := range []string{"model", "effort", "provider"} {
			if str(p, key) != "" {
				return core.Result{}, core.RejectBeforeDispatch(fmt.Sprintf("Antigravity cannot override %s per turn; switch session policy first", key))
			}
		}
		s.mu.Lock()
		c := s.client
		allowImages := c == nil || c.promptImage
		if s.closed || s.status != "idle" || s.promptRunning || c == nil || c.exited() {
			s.mu.Unlock()
			return core.Result{}, core.RejectBeforeDispatch("Antigravity conversation is not idle")
		}
		blocks, bErr := buildACPPromptBlocks(p, allowImages)
		if bErr != nil {
			s.mu.Unlock()
			return core.Result{}, bErr
		}
		cmdID := str(p, "command_id")
		s.status = "running"
		s.promptRunning = true
		s.reactiveWaiting = false
		s.cancelRequested = false
		s.reactiveMonitorGen++
		s.command = cmdID
		s.activeMsgID = ""
		s.activeText.Reset()
		s.activeThinking.Reset()
		preserved := make(map[string]*acpToolState)
		for k, st := range s.toolItems {
			if st != nil && st.name == "ask_question" && st.questionEmitted {
				preserved[k] = st
			}
		}
		s.toolItems = preserved
		s.mu.Unlock()

		_ = a.announce(s)
		go a.runPrompt(s, c, cmdID, blocks)

		result = a.result(s)
		result.Payload["accepted"] = true
		return result, nil
	case "session.close":
		s.mu.Lock()
		if s.status != "idle" {
			s.mu.Unlock()
			return core.Result{}, core.RejectBeforeDispatch("Antigravity active or ambiguous session cannot close")
		}
		s.closed = true
		c := s.client
		isRealSession := !s.control && !s.readonly && !s.deleteOnly
		s.mu.Unlock()
		s.stopWorker()

		if isRealSession && c != nil && !c.exited() && c.hasClose {
			closeCtx, closeCancel := context.WithTimeout(ctx, 5*time.Second)
			_, closeErr := c.call(closeCtx, "session/close", map[string]any{"sessionId": s.id})
			closeCancel()
			if closeErr != nil && !c.exited() {
				var rpcErr *acpRPCError
				if !asACPRPCError(closeErr, &rpcErr) || rpcErr.Code != -32601 {
					return core.Result{}, closeErr
				}
			}
		}
		a.mu.Lock()
		delete(a.sessions, s.id)
		a.mu.Unlock()
		result.Payload["closed"] = true
		result.Payload["termination_confirmed"] = true
		return result, nil
	default:
		return core.Result{}, core.RejectBeforeDispatch(fmt.Sprintf("Antigravity ACP transport does not support %s", r.Action))
	}
}

func (a *Adapter) Snapshot() map[string]core.Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]core.Result{}
	for id, s := range a.sessions {
		out[id] = a.result(s)
	}
	return out
}

func (a *Adapter) Close() error {
	a.mu.Lock()
	a.closed = true
	sessions := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.mu.Unlock()

	for _, s := range sessions {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.stopWorker()
	}

	a.acpMu.Lock()
	c := a.client
	a.client = nil
	a.acpMu.Unlock()

	if c != nil {
		err := c.forceClose()
		<-c.done
		return err
	}
	return nil
}

func (a *Adapter) Shutdown(ctx context.Context) ([]string, error) {
	a.mu.Lock()
	if len(a.opening) > 0 {
		a.mu.Unlock()
		return nil, fmt.Errorf("Antigravity attach in progress")
	}
	for _, s := range a.sessions {
		if a.result(s).Status != "idle" {
			a.mu.Unlock()
			return nil, fmt.Errorf("Antigravity active or ambiguous session cannot disconnect")
		}
	}
	ids := make([]string, 0, len(a.sessions))
	sessions := make([]*session, 0, len(a.sessions))
	for id, s := range a.sessions {
		ids = append(ids, id)
		sessions = append(sessions, s)
	}
	a.mu.Unlock()

	a.acpMu.Lock()
	c := a.client
	a.client = nil
	a.acpMu.Unlock()

	for _, s := range sessions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.closed = true
		isRealSession := !s.control && !s.readonly && !s.deleteOnly
		s.mu.Unlock()
		s.stopWorker()
		if isRealSession && c != nil && !c.exited() && c.hasClose {
			_, _ = c.call(ctx, "session/close", map[string]any{"sessionId": s.id})
		}
	}

	if c != nil {
		if err := c.closeIdle(ctx); err != nil {
			return nil, err
		}
	}

	a.mu.Lock()
	for _, id := range ids {
		delete(a.sessions, id)
	}
	a.mu.Unlock()
	return ids, nil
}
