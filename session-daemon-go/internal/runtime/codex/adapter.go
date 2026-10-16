package codex

import (
	nativeprocess "astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Emitter func(string, string, map[string]any, string) error
type adapterSession struct {
	runtime             *Runtime
	mu                  sync.Mutex
	mutating            bool
	mutationDone        chan struct{}
	inputUnconfirmed    bool
	unconfirmedRevision uint64
	// control marks a transport-only control binding: the runtime owns a live
	// app-server transport but no native thread was created or resumed.
	control  bool
	model    string
	provider string
	effort   string
	// approval is the daemon-held Astrorder approval mode (manual/auto/
	// full_access). Codex applies approval policy per turn, so it is mapped
	// onto turn/start options on every send.
	approval string
}

// approvalTurnOptions maps an Astrorder approval mode to Codex turn/start
// overrides (approvalPolicy / approvalsReviewer / sandboxPolicy).
func approvalTurnOptions(mode string) (map[string]any, bool) {
	switch mode {
	case "manual":
		return map[string]any{"approvalPolicy": "untrusted", "approvalsReviewer": "user", "sandboxPolicy": map[string]any{"type": "workspaceWrite"}}, true
	case "auto":
		return map[string]any{"approvalPolicy": "on-request", "approvalsReviewer": "auto_review", "sandboxPolicy": map[string]any{"type": "workspaceWrite"}}, true
	case "full_access":
		return map[string]any{"approvalPolicy": "never", "approvalsReviewer": "user", "sandboxPolicy": map[string]any{"type": "dangerFullAccess"}}, true
	}
	return nil, false
}

// approvalModeFromThread derives the Astrorder mode from a native thread
// payload (thread/read or thread/resume response). Empty when unrecognised.
func approvalModeFromThread(resp map[string]any) string {
	policy, _ := resp["approvalPolicy"].(string)
	reviewer, _ := resp["approvalsReviewer"].(string)
	if thread, ok := resp["thread"].(map[string]any); ok {
		if policy == "" {
			policy, _ = thread["approvalPolicy"].(string)
		}
		if reviewer == "" {
			reviewer, _ = thread["approvalsReviewer"].(string)
		}
	}
	switch policy {
	case "never":
		return "full_access"
	case "untrusted":
		return "manual"
	case "on-request":
		if reviewer == "auto_review" || reviewer == "guardian_subagent" {
			return "auto"
		}
		return "manual"
	}
	return ""
}

type Adapter struct {
	config    Config
	emit      Emitter
	mu        sync.Mutex
	sessions  map[string]*adapterSession
	opening   map[string]bool
	starter   SSHStarter
	catalogMu sync.Mutex
	catalog   *Runtime
	closed    bool
	onClose   []func()
}

func NewAdapter(config Config, emit Emitter) *Adapter {
	return &Adapter{config: config, emit: emit, sessions: map[string]*adapterSession{}, opening: map[string]bool{}}
}

func NewSSHAdapter(config Config, emit Emitter, starter SSHStarter) *Adapter {
	return &Adapter{config: config, emit: emit, sessions: map[string]*adapterSession{}, opening: map[string]bool{}, starter: starter}
}
func (a *Adapter) Create(ctx context.Context, r core.Request) (core.Result, error) {
	return a.open(ctx, r, false)
}
func (a *Adapter) Spawn(ctx context.Context, r core.Request) (core.Result, error) {
	if runtimeControlParams(r) {
		return a.controlSpawn(ctx, r)
	}
	return a.open(ctx, r, true)
}
func (a *Adapter) open(ctx context.Context, r core.Request, resume bool) (core.Result, error) {
	cwd := a.config.Workspace
	if value, exists := r.Fields["cwd"]; exists {
		var ok bool
		cwd, ok = value.(string)
		if !ok || cwd == "" {
			return core.Result{}, fmt.Errorf("codex workspace is invalid")
		}
	}
	params := map[string]any{}
	raw, exists := r.Fields["params"]
	if exists {
		object, ok := raw.(map[string]any)
		if !ok {
			return core.Result{}, fmt.Errorf("codex params must be an object")
		}
		for _, key := range []string{"model", "modelProvider", "approvalPolicy", "sandbox", "baseInstructions", "developerInstructions", "ephemeral"} {
			if value, ok := object[key]; ok {
				params[key] = value
			}
		}
	}
	parent, title := "", ""
	if !resume {
		if raw, exists := r.Fields["parent_session_id"]; exists && raw != nil {
			var ok bool
			parent, ok = raw.(string)
			if !ok || parent == "" || len(parent) > 256 {
				return core.Result{}, fmt.Errorf("Codex parent_session_id is invalid")
			}
		}
		if raw, exists := r.Fields["ephemeral"]; exists {
			value, ok := raw.(bool)
			if !ok {
				return core.Result{}, fmt.Errorf("Codex ephemeral must be boolean")
			}
			params["ephemeral"] = value
		}
		if raw, exists := r.Fields["title"]; exists && raw != nil {
			var ok bool
			title, ok = raw.(string)
			if !ok || strings.TrimSpace(title) == "" || len(title) > 512 || strings.ContainsAny(title, "\x00\r\n") {
				return core.Result{}, fmt.Errorf("Codex title is invalid")
			}
		}
	}
	for _, key := range []string{"model", "modelProvider"} {
		if value, exists := params[key]; exists {
			if text, ok := value.(string); !ok || !validToken(text, 160) {
				return core.Result{}, fmt.Errorf("codex model binding is invalid")
			}
		}
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("codex adapter is closed")
	}
	var retired *adapterSession
	if resume {
		if r.SessionID == "" || len(r.SessionID) > 256 {
			a.mu.Unlock()
			return core.Result{}, fmt.Errorf("codex session id is invalid")
		}
		if a.opening[r.SessionID] {
			a.mu.Unlock()
			return core.Result{}, fmt.Errorf("codex session already attached")
		}
		if existing := a.sessions[r.SessionID]; existing != nil {
			if !existing.runtime.recoverable() {
				a.mu.Unlock()
				return core.Result{}, fmt.Errorf("codex session already attached; native exit or projection completion is unconfirmed")
			}
			retired = existing
			delete(a.sessions, r.SessionID)
		}
		a.opening[r.SessionID] = true
	}
	a.mu.Unlock()
	if retired != nil {
		retired.runtime.Close()
	}
	if resume {
		defer func() {
			a.mu.Lock()
			delete(a.opening, r.SessionID)
			// Failed native resume (including another writer) must retain the
			// retired error snapshot instead of exposing the registry's old run.
			if retired != nil && !a.closed && a.sessions[r.SessionID] == nil {
				a.sessions[r.SessionID] = retired
			}
			a.mu.Unlock()
		}()
	}
	config := a.config
	var rt *Runtime
	config.OnNotification = func(n Notification) {
		if n.SessionID == "" {
			return
		}
		payload := map[string]any{"agent_id": config.AgentID, "frame": n.Frame}
		if n.CommandID != "" {
			payload["command_id"] = n.CommandID
		}
		if n.ApprovalID != "" {
			payload["approval_id"] = n.ApprovalID
		}
		if a.emit != nil {
			event, status := "codex.notification", n.Status
			if n.Method == "runtime/closed" {
				event, status = "session.error", "error"
				payload["session_id"] = n.SessionID
			}
			if err := a.emit(n.SessionID, event, payload, status); err != nil {
				rt.setStatus("error")
				rt.Close()
			}
		}
	}
	if a.starter != nil {
		rt = NewWithSSHStarter(config, a.starter)
	} else {
		rt = New(config)
	}
	nativeRequest := Request{Cwd: cwd, SessionID: r.SessionID, Params: params, ParentSessionID: parent, Title: title}
	var native Result
	var err error
	if resume {
		native, err = rt.Resume(ctx, nativeRequest)
	} else {
		native, err = rt.Create(ctx, nativeRequest)
	}
	if err != nil {
		rt.Close()
		return core.Result{}, err
	}
	a.mu.Lock()
	if a.closed || a.sessions[native.SessionID] != nil {
		a.mu.Unlock()
		rt.Close()
		return core.Result{}, fmt.Errorf("codex ownership changed during startup")
	}
	var model, provider, effort string
	if m, ok := params["model"].(string); ok {
		model = m
	}
	if p, ok := params["modelProvider"].(string); ok {
		provider = p
	}
	if thread, ok := native.Native["thread"].(map[string]any); ok {
		if m, ok := thread["model"].(string); ok && m != "" {
			model = m
		}
		if p, ok := thread["modelProvider"].(string); ok && p != "" {
			provider = p
		}
		if e, ok := thread["reasoningEffort"].(string); ok && e != "" {
			effort = e
		}
	}
	if m, ok := native.Native["model"].(string); ok && m != "" {
		model = m
	}
	if p, ok := native.Native["modelProvider"].(string); ok && p != "" {
		provider = p
	}
	a.sessions[native.SessionID] = &adapterSession{runtime: rt, model: model, provider: provider, effort: effort}
	a.mu.Unlock()
	if err := nativeprocess.ReportOwner(a.emit, native.SessionID, native.Status, rt.LocalPID()); err != nil {
		a.mu.Lock()
		delete(a.sessions, native.SessionID)
		a.mu.Unlock()
		rt.Close()
		return core.Result{}, err
	}
	payload := map[string]any{"session_id": native.SessionID, "status": native.Status, "agent_id": a.config.AgentID, "created": !resume}
	if thread, exists := native.Native["thread"]; exists {
		payload["thread"] = thread
	}
	if model != "" {
		payload["model"] = model
	}
	if provider != "" {
		payload["provider"] = provider
	}
	if effort != "" {
		payload["effort"] = effort
	}
	return core.Result{SessionID: native.SessionID, Status: native.Status, Payload: payload}, nil
}
func (a *Adapter) session(id string) (*adapterSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[id]
	if a.closed || s == nil {
		return nil, fmt.Errorf("codex session is not daemon-owned")
	}
	return s, nil
}
func validToken(value string, limit int) bool {
	return value != "" && len(value) <= limit && !strings.ContainsAny(value, " \t\r\n\x00")
}
func (a *Adapter) Command(ctx context.Context, r core.Request) (core.Result, error) {
	s, err := a.session(r.SessionID)
	if err != nil {
		return core.Result{}, err
	}
	if s.control {
		// Control bindings own no native thread: only the closed read-only
		// catalog set is served; native-session actions are rejected.
		return a.controlCommand(ctx, s, r)
	}
	rt := s.runtime
	if r.Action == "session.model.read" && rt.recoverable() {
		// Let the App's exact-identity attach path restore this persisted thread.
		return core.Result{}, core.ErrSessionNotOwned
	}
	if err := rt.transportError(); err != nil {
		return core.Result{}, err
	}
	if err = rt.requireSession(r.SessionID); err != nil {
		return core.Result{}, err
	}
	fields := r.Fields
	payload := map[string]any{}
	command := Command{Action: r.Action, SessionID: r.SessionID}
	command.CommandID, _ = fields["command_id"].(string)
	method := ""
	params := map[string]any{"threadId": r.SessionID}
	switch r.Action {
	case "session.send":
		command.Input, err = adapterInput(rt, fields)
		if err != nil {
			return core.Result{}, err
		}
		command.Options = map[string]any{}
		s.mu.Lock()
		approval := s.approval
		s.mu.Unlock()
		if defaults, ok := approvalTurnOptions(approval); ok {
			for key, value := range defaults {
				command.Options[key] = value
			}
		}
		for _, field := range []string{"params", "options"} {
			options, exists := fields[field]
			if !exists {
				continue
			}
			object, ok := options.(map[string]any)
			if !ok {
				return core.Result{}, fmt.Errorf("codex options must be an object")
			}
			for _, key := range []string{"model", "effort", "summary", "approvalPolicy", "approvalsReviewer", "sandboxPolicy", "outputSchema", "idle_only"} {
				if v, ok := object[key]; ok {
					command.Options[key] = v
				}
			}
		}
	case "session.approve":
		command.ApprovalID, _ = fields["approval_id"].(string)
		command.Decision, _ = fields["decision"].(string)
		command.Permissions, _ = fields["permissions"].(map[string]any)
		command.Options, _ = fields["user_input"].(map[string]any)
	case "session.interrupt", "session.steer":
		turn, _ := fields["turn_id"].(string)
		rt.mu.Lock()
		active := rt.activeTurnID
		rt.mu.Unlock()
		if turn == "" || turn != active {
			return core.Result{}, fmt.Errorf("codex turn is not the active daemon-owned turn")
		}
		if r.Action == "session.interrupt" {
			method = "turn/interrupt"
			params["turnId"] = turn
		} else {
			command.TurnID = turn
			command.Input, err = adapterInput(rt, fields)
			if err != nil {
				return core.Result{}, err
			}
		}
		payload["turn_id"] = turn
		payload["accepted"] = true
	case "session.compact":
		method = "thread/compact/start"
		payload["accepted"] = true
	case "session.review":
		target, ok := fields["target"].(map[string]any)
		if !ok {
			return core.Result{}, fmt.Errorf("codex review target is invalid")
		}
		delivery, _ := fields["delivery"].(string)
		if delivery != "inline" && delivery != "detached" {
			return core.Result{}, fmt.Errorf("codex review delivery is invalid")
		}
		method = "review/start"
		params["target"] = target
		params["delivery"] = delivery
	case "session.settings":
		method = "thread/settings/update"
		for _, key := range []string{"model", "effort"} {
			if value, exists := fields[key]; exists {
				v, ok := value.(string)
				if !ok || !validToken(v, 160) {
					return core.Result{}, fmt.Errorf("codex settings are invalid")
				}
				if key == "effort" && !validEffort(v) {
					return core.Result{}, fmt.Errorf("codex reasoning effort is invalid")
				}
				params[key] = v
				payload[key] = v
			}
		}
		if len(params) == 1 {
			return core.Result{}, fmt.Errorf("codex settings request is empty")
		}
		payload["accepted"] = true
	case "session.rename":
		title, ok := fields["title"].(string)
		if !ok || strings.TrimSpace(title) == "" || len(title) > 512 || strings.ContainsAny(title, "\x00\r\n") {
			return core.Result{}, fmt.Errorf("codex title is invalid")
		}
		method = "thread/name/set"
		params["name"] = title
		payload["title"] = title
	case "session.processes":
		return a.backgroundProcesses(ctx, rt, r)
	case "session.history_page":
		return a.historyPage(ctx, rt, r)
	case "session.models":
		method = "model/list"
		params = map[string]any{"limit": 100}
	case "session.goal.set", "session.goal.clear":
		return a.handleGoal(ctx, s, r)
	case "session.model.read":
		if err := ctx.Err(); err != nil {
			return core.Result{}, err
		}
		s.mu.Lock()
		model := s.model
		provider := s.provider
		effort := s.effort
		s.mu.Unlock()
		if resp, err := rt.request(ctx, "thread/read", map[string]any{"threadId": r.SessionID, "includeTurns": false}); err == nil {
			s.mu.Lock()
			if thread, ok := resp["thread"].(map[string]any); ok {
				if m, ok := thread["model"].(string); ok && m != "" {
					s.model = m
				}
				if p, ok := thread["modelProvider"].(string); ok && p != "" {
					s.provider = p
				}
				if e, ok := thread["reasoningEffort"].(string); ok && e != "" {
					s.effort = e
				}
			}
			if m, ok := resp["model"].(string); ok && m != "" {
				s.model = m
			}
			if p, ok := resp["modelProvider"].(string); ok && p != "" {
				s.provider = p
			}
			model = s.model
			provider = s.provider
			effort = s.effort
			s.mu.Unlock()
		}
		return core.Result{
			SessionID: r.SessionID,
			Status:    rt.Status(),
			Payload: map[string]any{
				"model":    model,
				"provider": provider,
				"effort":   effort,
			},
		}, nil
	case "session.model.set":
		model, _ := fields["model"].(string)
		if model == "" || !validToken(model, 160) {
			return core.Result{}, fmt.Errorf("codex model is invalid")
		}
		provider, _ := fields["provider"].(string)
		if provider != "" && !validToken(provider, 160) {
			return core.Result{}, fmt.Errorf("codex provider is invalid")
		}
		method = "thread/settings/update"
		params["model"] = model
		payload["model"] = model
		payload["provider"] = provider
		if provider != "" {
			params["modelProvider"] = provider
		}
		if effort, ok := fields["effort"].(string); ok && effort != "" {
			if !validEffort(effort) {
				return core.Result{}, fmt.Errorf("codex reasoning effort is invalid")
			}
			params["effort"] = effort
			payload["effort"] = effort
		}
	case "session.approval.set":
		mode, _ := fields["mode"].(string)
		if _, ok := approvalTurnOptions(mode); !ok {
			return core.Result{}, fmt.Errorf("codex approval mode is invalid")
		}
		s.mu.Lock()
		s.approval = mode
		s.mu.Unlock()
		return core.Result{SessionID: r.SessionID, Status: rt.Status(), Payload: map[string]any{"mode": mode, "accepted": true}}, nil
	case "session.approval.read":
		if err := ctx.Err(); err != nil {
			return core.Result{}, err
		}
		s.mu.Lock()
		mode := s.approval
		s.mu.Unlock()
		if mode == "" {
			if resp, err := rt.request(ctx, "thread/read", map[string]any{"threadId": r.SessionID, "includeTurns": false}); err == nil {
				mode = approvalModeFromThread(resp)
			}
		}
		if mode == "" {
			// Codex default for an untouched thread is on-request with user review.
			mode = "manual"
		}
		return core.Result{SessionID: r.SessionID, Status: rt.Status(), Payload: map[string]any{"mode": mode}}, nil
	case "session.reasoning.set":
		effort, _ := fields["effort"].(string)
		if !validEffort(effort) {
			return core.Result{}, fmt.Errorf("codex reasoning effort is invalid")
		}
		method = "thread/settings/update"
		params["effort"] = effort
		payload["effort"] = effort
	case "session.delete":
		s.mu.Lock()
		if s.mutating {
			s.mu.Unlock()
			return core.Result{}, fmt.Errorf("codex session has pending mutation")
		}
		s.mutating = true
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.mutating = false; s.mu.Unlock() }()
		native, err := rt.Command(ctx, command)
		if err != nil {
			return core.Result{}, err
		}
		rt.Close()
		a.mu.Lock()
		delete(a.sessions, r.SessionID)
		a.mu.Unlock()
		return core.Result{SessionID: r.SessionID, Status: native.Status, Payload: map[string]any{"deleted": r.SessionID}}, nil
	case "session.disconnect":
		return core.Result{SessionID: r.SessionID, Status: rt.Status(), Payload: map[string]any{"disconnected": r.SessionID}}, nil
	default:
		return core.Result{}, fmt.Errorf("codex action is unsupported")
	}
	mutation := r.Action == "session.send" || r.Action == "session.steer" || r.Action == "session.compact" || r.Action == "session.review"
	var inputRevision uint64
	if mutation {
		for {
			if err := ctx.Err(); err != nil {
				return core.Result{}, err
			}
			s.mu.Lock()
			if s.mutating && (r.Action == "session.send" || r.Action == "session.steer") && s.mutationDone != nil {
				done := s.mutationDone
				s.mu.Unlock()
				select {
				case <-done:
					continue
				case <-ctx.Done():
					return core.Result{}, ctx.Err()
				}
			}
			rt.mu.Lock()
			active, activeTurn := rt.status == "running" || rt.status == "waiting_approval", rt.activeTurnID
			inputRevision = rt.turnRevision
			rt.mu.Unlock()
			options, _ := fields["params"].(map[string]any)
			if options["idle_only"] == true && (active || activeTurn != "" || s.mutating) {
				s.mu.Unlock()
				return core.Result{}, fmt.Errorf("queued send requires an idle session")
			}
			canSteer := (r.Action == "session.send" || r.Action == "session.steer") && activeTurn != ""
			if (r.Action == "session.send" || r.Action == "session.steer") && s.inputUnconfirmed && activeTurn == "" && inputRevision <= s.unconfirmedRevision {
				s.mu.Unlock()
				return core.Result{}, fmt.Errorf("codex previous input is unconfirmed; reconcile before sending another turn")
			}
			if s.mutating || (active && !canSteer) {
				s.mu.Unlock()
				return core.Result{}, fmt.Errorf("codex session is active")
			}
			s.mutating = true
			s.mutationDone = make(chan struct{})
			s.mu.Unlock()
			break
		}
		defer func() {
			s.mu.Lock()
			s.mutating = false
			close(s.mutationDone)
			s.mutationDone = nil
			s.mu.Unlock()
		}()
	}
	if method == "" {
		native, err := rt.Command(ctx, command)
		if r.Action == "session.send" || r.Action == "session.steer" {
			s.mu.Lock()
			s.inputUnconfirmed = err != nil
			s.unconfirmedRevision = inputRevision
			s.mu.Unlock()
		}
		if err != nil {
			return core.Result{}, err
		}
		payload["accepted"] = native.Accepted
		if native.Completed {
			payload["completed"] = true
			if a.emit != nil && command.CommandID != "" {
				if err := a.emit(r.SessionID, "codex.command_complete", map[string]any{"agent_id": a.config.AgentID, "session_id": r.SessionID, "command_id": command.CommandID}, rt.Status()); err != nil {
					return core.Result{}, fmt.Errorf("guidance accepted but durable delivery event failed: %w", err)
				}
			}
		}
		if native.TurnID != "" {
			payload["turn_id"] = native.TurnID
		}
		return core.Result{SessionID: r.SessionID, Status: native.Status, Payload: payload}, nil
	}
	response, err := rt.request(ctx, method, params)
	if err != nil {
		return core.Result{}, err
	}
	if r.Action == "session.history_page" {
		payload = response
	} else if r.Action == "session.models" {
		payload["items"] = response["data"]
	} else if r.Action == "session.review" {
		payload = response
		payload["accepted"] = true
	} else if r.Action == "session.model.set" {
		s.mu.Lock()
		if m, ok := payload["model"].(string); ok && m != "" {
			s.model = m
		}
		if p, ok := payload["provider"].(string); ok {
			s.provider = p
		}
		if e, ok := payload["effort"].(string); ok && e != "" {
			s.effort = e
		}
		s.mu.Unlock()
	} else if r.Action == "session.reasoning.set" {
		s.mu.Lock()
		if e, ok := payload["effort"].(string); ok && e != "" {
			s.effort = e
		}
		s.mu.Unlock()
	} else if r.Action == "session.settings" {
		s.mu.Lock()
		if m, ok := payload["model"].(string); ok && m != "" {
			s.model = m
		}
		if e, ok := payload["effort"].(string); ok && e != "" {
			s.effort = e
		}
		s.mu.Unlock()
	}
	return core.Result{SessionID: r.SessionID, Status: rt.Status(), Payload: payload}, nil
}
func validEffort(value string) bool {
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return true
	}
	return false
}
func adapterInput(rt *Runtime, fields map[string]any) ([]map[string]any, error) {
	raw, exists := fields["input"]
	if !exists {
		prompt, ok := fields["prompt"].(string)
		if !ok || prompt == "" || len(prompt) > 16<<20 {
			return nil, fmt.Errorf("codex prompt is invalid")
		}
		return []map[string]any{{"type": "text", "text": prompt}}, nil
	}
	values, ok := raw.([]any)
	if !ok || len(values) == 0 || len(values) > 1000 {
		return nil, fmt.Errorf("codex input must be a non-empty array")
	}
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("codex input item is invalid")
		}
		kind, _ := item["type"].(string)
		switch kind {
		case "text":
			text, ok := item["text"].(string)
			if !ok || len(text) > 16<<20 {
				return nil, fmt.Errorf("codex text is invalid")
			}
		case "image":
			text, ok := item["url"].(string)
			u, err := url.Parse(text)
			if !ok || err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "data") {
				return nil, fmt.Errorf("codex image URL is invalid")
			}
		case "localImage":
			path, ok := item["path"].(string)
			if !ok {
				return nil, fmt.Errorf("codex local image path is invalid")
			}
			absolute, err := filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			resolved, err := filepath.EvalSymlinks(absolute)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(resolved)
			if err != nil || info.IsDir() || !rt.allowed(resolved) {
				return nil, fmt.Errorf("codex local image is outside allowed paths")
			}
			item = map[string]any{"type": "localImage", "path": resolved}
		case "skill", "mention":
			name, ok := item["name"].(string)
			path, pathOK := item["path"].(string)
			if !ok || !pathOK || name == "" || path == "" {
				return nil, fmt.Errorf("codex input reference is invalid")
			}
		default:
			return nil, fmt.Errorf("codex input type is unsupported")
		}
		copy := map[string]any{}
		for k, v := range item {
			copy[k] = v
		}
		out = append(out, copy)
	}
	return out, nil
}
func (a *Adapter) Snapshot() map[string]core.Result {
	a.mu.Lock()
	sessions := map[string]*adapterSession{}
	for id, s := range a.sessions {
		sessions[id] = s
	}
	a.mu.Unlock()
	out := map[string]core.Result{}
	for id, s := range sessions {
		payload := map[string]any{}
		if s.control {
			// Control bindings stay identifiable and re-bindable after an
			// App restart: the snapshot carries the same metadata as spawn.
			payload["runtime_control"] = true
			payload["control_state"] = "owned"
			if a.config.AgentID != "" {
				payload["agent_id"] = a.config.AgentID
			}
		}
		out[id] = core.Result{SessionID: id, Status: s.runtime.Status(), Payload: payload}
	}
	return out
}

// OnClose registers fn to run once when the adapter closes. It lets a remote
// connection tie auxiliary work (the hook-event stream) to the adapter lifetime.
func (a *Adapter) OnClose(fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		go fn()
		return
	}
	a.onClose = append(a.onClose, fn)
}

func (a *Adapter) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	callbacks := a.onClose
	a.onClose = nil
	sessions := a.sessions
	catalog := a.catalog
	a.catalog = nil
	a.sessions = map[string]*adapterSession{}
	a.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
	if catalog != nil {
		catalog.Close()
	}
	for _, s := range sessions {
		s.runtime.Close()
	}
	return nil
}
