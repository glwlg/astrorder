package hermes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	nativeprocess "astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
)

type Config struct {
	Executable   string
	Arguments    []string
	Environment  []string
	Workspace    string
	Allowed      []string
	AgentID      string
	AgentName    string
	ConnectionID string
	SourceID     string
	ProfileName  string
	StateDBPath  string
	Emit         func(sessionID, event string, payload map[string]any, status string) error
}

type SessionState struct {
	SessionID    string
	CommandID    string
	sending      bool
	Handle       string
	Status       string
	Title        string
	Workspace    string
	Model        string
	Provider     string
	Effort       string
	Metadata     map[string]any
	resumePermit chan struct{}
}

type Adapter struct {
	config Config

	mu               sync.RWMutex
	writeMu          sync.Mutex
	cmd              *exec.Cmd
	owner            *nativeprocess.CommandOwner
	cleanupErr       error
	stdin            io.WriteCloser
	canonical        bool
	canonicalSocket  *canonicalTransport
	stdout           io.ReadCloser
	stderr           io.ReadCloser
	readyCh          chan struct{}
	processDone      chan struct{}
	stderrBuf        string
	closed           bool
	failed           bool
	sshStarter       SSHStarter
	sshSession       SSHSession
	eventOnce        sync.Once
	eventQueue       chan *rpcMessage
	eventStop        chan struct{}
	eventDone        chan struct{}
	eventStopOnce    sync.Once
	sessions         map[string]*SessionState
	handleToSession  map[string]string
	pending          map[string]pendingRequest
	pendingApprovals map[string]*approvalRecord
}

func New(config Config) *Adapter {
	return &Adapter{
		config:     config,
		eventQueue: make(chan *rpcMessage, 128), eventStop: make(chan struct{}), eventDone: make(chan struct{}),
		sessions:         make(map[string]*SessionState),
		handleToSession:  make(map[string]string),
		pending:          make(map[string]pendingRequest),
		pendingApprovals: make(map[string]*approvalRecord),
	}
}

func (a *Adapter) publishLocalOwner(sessionID, status string) error {
	a.mu.RLock()
	pid := 0
	if a.sshSession == nil && a.cmd != nil && a.cmd.Process != nil {
		pid = a.cmd.Process.Pid
	}
	emit := a.config.Emit
	a.mu.RUnlock()
	return nativeprocess.ReportOwner(emit, sessionID, status, pid)
}

func (a *Adapter) getSession(sessionID string) (*SessionState, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, ok := a.sessions[sessionID]
	return s, ok
}

func (a *Adapter) ensureHandle(ctx context.Context, session *SessionState) (string, error) {
	a.mu.Lock()
	if session.resumePermit == nil {
		session.resumePermit = make(chan struct{}, 1)
	}
	permit := session.resumePermit
	a.mu.Unlock()
	select {
	case permit <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-permit }()
	a.mu.RLock()
	handle := session.Handle
	a.mu.RUnlock()
	if handle != "" {
		return handle, nil
	}
	res, err := a.rpc(ctx, "session.resume", map[string]any{
		"session_id":    session.SessionID,
		"lazy":          true,
		"omit_messages": true,
	})
	if err != nil {
		return "", err
	}
	h, _ := res["session_id"].(string)
	if h == "" {
		return "", fmt.Errorf("hermes did not return a live session handle")
	}
	info, _ := res["info"].(map[string]any)
	cwd, ok := info["cwd"].(string)
	if !ok || cwd == "" {
		return "", fmt.Errorf("native resume omitted workspace")
	}
	workspace, err := a.validateWorkspace(cwd)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.closed || a.failed || a.sessions[session.SessionID] != session {
		a.mu.Unlock()
		return "", fmt.Errorf("native ownership changed during resume")
	}
	session.Handle = h
	session.Workspace = workspace
	a.handleToSession[h] = session.SessionID
	a.mu.Unlock()
	if todos, ok := res["todo_state"].(map[string]any); ok {
		a.handleGatewayEvent(map[string]any{"type": "todo.updated", "session_id": h, "payload": todos})
	}
	a.refreshGoal(ctx, h)
	return h, nil
}

func (a *Adapter) Create(ctx context.Context, req core.Request) (core.Result, error) {
	workspaceRaw, _ := req.Fields["cwd"].(string)
	workspace, err := a.validateWorkspace(workspaceRaw)
	if err != nil {
		return core.Result{}, err
	}
	if err := a.startProcess(ctx); err != nil {
		return core.Result{}, err
	}

	title, _ := req.Fields["title"].(string)
	if title == "" {
		title = "新会话"
	}
	provider, _ := req.Fields["provider"].(string)
	model, _ := req.Fields["model"].(string)
	effort, _ := req.Fields["effort"].(string)
	parentID, _ := req.Fields["parent_session_id"].(string)

	var method string
	var params map[string]any

	if parentID != "" {
		method = "session.branch"
		params = map[string]any{
			"session_id": parentID,
			"name":       title,
		}
		if target, exists := req.Fields["target_message_id"]; exists {
			params["target_message_id"] = target
		}
	} else {
		method = "session.create"
		params = map[string]any{
			"source": "local",
			"cwd":    workspace,
			"title":  title,
		}
		if model != "" && provider != "" {
			params["model"] = model
			params["provider"] = provider
		}
		if effort != "" {
			params["reasoning_effort"] = effort
		}
	}

	resp, err := a.rpc(ctx, method, params)
	if err != nil {
		return core.Result{}, fmt.Errorf("hermes %s failed: %w", method, err)
	}

	durableID, _ := resp["stored_session_id"].(string)
	if durableID == "" {
		durableID, _ = resp["id"].(string)
	}
	if durableID == "" {
		durableID, _ = resp["session_id"].(string)
	}
	if durableID == "" {
		return core.Result{}, fmt.Errorf("hermes did not return a session identity")
	}
	if a.canonical && method == "session.branch" {
		// Mutation returns the child identity; resume subscribes this viewer to it.
		resp, err = a.rpc(ctx, "session.resume", map[string]any{"session_id": durableID, "omit_messages": true})
		if err != nil {
			return core.Result{}, fmt.Errorf("created Hermes branch %s but attachment is unconfirmed: %w", durableID, err)
		}
	}
	// A display title must never select an existing canonical conversation.
	// Leave untitled drafts available for native auto-naming on the first turn.
	if a.canonical && method == "session.create" {
		if requested, _ := req.Fields["title"].(string); requested != "" && requested != "新会话" {
			label := []rune(requested)
			if len(label) > 89 {
				label = label[:89]
			}
			suffix := durableID
			if len(suffix) > 8 {
				suffix = suffix[len(suffix)-8:]
			}
			title = string(label) + " · " + suffix
			if _, err := a.rpc(ctx, "session.title", map[string]any{"session_id": durableID, "title": title}); err != nil {
				return core.Result{}, fmt.Errorf("created Hermes session %s but title is unconfirmed: %w", durableID, err)
			}
		}
	}

	handle, _ := resp["session_id"].(string)

	state := &SessionState{
		SessionID: durableID,
		Handle:    handle,
		Status:    "idle",
		Title:     title,
		Workspace: workspace,
		Model:     model,
		Provider:  provider,
		Effort:    effort,
		Metadata:  map[string]any{},
	}

	a.mu.Lock()
	a.sessions[durableID] = state
	if handle != "" {
		a.handleToSession[handle] = durableID
	}
	a.mu.Unlock()

	if err := a.publishLocalOwner(durableID, "idle"); err != nil {
		a.mu.Lock()
		delete(a.sessions, durableID)
		if handle != "" {
			delete(a.handleToSession, handle)
		}
		a.mu.Unlock()
		return core.Result{}, err
	}

	return core.Result{
		SessionID: durableID,
		Status:    "idle",
		Payload: map[string]any{
			"agent_id":   a.config.AgentID,
			"workspace":  workspace,
			"title":      title,
			"session_id": durableID,
		},
	}, nil
}

func (a *Adapter) Spawn(ctx context.Context, req core.Request) (core.Result, error) {
	if runtimeControl(req) {
		return a.connectControl(ctx, req)
	}
	if req.SessionID == "" {
		return core.Result{}, fmt.Errorf("session_id is required")
	}
	if err := a.startProcess(ctx); err != nil {
		return core.Result{}, err
	}

	a.mu.Lock()
	state, exists := a.sessions[req.SessionID]
	if !exists {
		state = &SessionState{
			SessionID: req.SessionID,
			Status:    "idle",
			Metadata:  map[string]any{},
		}
		a.sessions[req.SessionID] = state
	}
	a.mu.Unlock()

	if _, err := a.ensureHandle(ctx, state); err != nil {
		a.mu.Lock()
		if a.sessions[req.SessionID] == state {
			delete(a.sessions, req.SessionID)
		}
		a.mu.Unlock()
		return core.Result{}, err
	}

	if err := a.publishLocalOwner(req.SessionID, "idle"); err != nil {
		a.mu.Lock()
		if a.sessions[req.SessionID] == state {
			delete(a.sessions, req.SessionID)
		}
		a.mu.Unlock()
		return core.Result{}, err
	}

	return core.Result{
		SessionID: req.SessionID,
		Status:    "idle",
		Payload: map[string]any{
			"agent_id": a.config.AgentID,
			"status":   "idle",
		},
	}, nil
}

func (a *Adapter) Command(ctx context.Context, req core.Request) (result core.Result, commandErr error) {
	defer func() {
		if commandErr == nil {
			a.mu.RLock()
			if session := a.sessions[req.SessionID]; session != nil {
				result.Status = session.Status
			}
			a.mu.RUnlock()
		}
	}()
	a.mu.RLock()
	unavailable := a.closed || a.failed
	a.mu.RUnlock()
	if unavailable {
		return core.Result{}, fmt.Errorf("Hermes native transport is unavailable")
	}
	if req.SessionID == "" {
		return core.Result{}, fmt.Errorf("session_id is required")
	}
	session, ok := a.getSession(req.SessionID)
	if !ok {
		return core.Result{}, fmt.Errorf("hermes session is not owned by this runtime")
	}

	switch req.Action {
	case "session.observe_status":
		activity, _ := req.Fields["status"].(string)
		if activity != "running" && activity != "waiting_approval" {
			return core.Result{}, fmt.Errorf("invalid positive activity observation")
		}
		a.mu.Lock()
		if session.Status != "waiting_approval" {
			session.Status = activity
		}
		current := session.Status
		a.mu.Unlock()
		return core.Result{SessionID: session.SessionID, Status: current, Payload: map[string]any{"status": current}}, nil
	case "session.send":
		return a.handleSend(ctx, session, req)
	case "session.compact":
		fields := make(map[string]any, len(req.Fields)+1)
		for key, value := range req.Fields {
			fields[key] = value
		}
		fields["text"] = "/compress"
		if instructions, _ := req.Fields["instructions"].(string); strings.TrimSpace(instructions) != "" {
			fields["text"] = "/compress " + strings.TrimSpace(instructions)
		}
		req.Fields = fields
		return a.handleSend(ctx, session, req)
	case "session.interrupt":
		return a.handleInterrupt(ctx, session)
	case "session.rename":
		return a.handleRename(ctx, session, req)
	case "session.delete":
		return a.handleDelete(ctx, session)
	case "session.models":
		result, err := a.handleModels(ctx)
		result.SessionID = session.SessionID
		return result, err
	case "session.commands":
		return a.handleCommands(ctx, session)
	case "session.model.read":
		return a.handleModelRead(ctx, session)
	case "session.goal.set", "session.goal.clear":
		return a.handleGoal(ctx, session, req)
	case "session.model.set":
		return a.handleModelSet(ctx, session, req)
	case "session.reasoning.set":
		return a.handleReasoningSet(ctx, session, req)
	case "session.approval.read":
		return a.handleApprovalRead(ctx, session)
	case "session.approval.set":
		return a.handleApprovalSet(ctx, session, req)
	case "session.approve", "session.approval.respond":
		return a.handleApprove(ctx, session, req)
	case "session.processes":
		result, err := a.rpc(ctx, "process.list", map[string]any{"session_id": session.Handle})
		return core.Result{SessionID: session.SessionID, Payload: result}, err
	case "session.history_page":
		return a.handleHistoryPage(ctx, session, req)
	default:
		return core.Result{}, fmt.Errorf("unsupported action: %s", req.Action)
	}
}

func (a *Adapter) handleSend(ctx context.Context, session *SessionState, req core.Request) (core.Result, error) {
	a.mu.Lock()
	params, _ := req.Fields["params"].(map[string]any)
	if params["idle_only"] == true && (session.Status != "idle" || session.sending) {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("queued send requires an idle session")
	}
	if session.sending || session.Status == "waiting_approval" {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("Hermes session already has an active command")
	}
	steering := session.Status == "running" && session.CommandID != "" && session.Handle != ""
	if params["guidance_only"] == true && !steering {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("Hermes guidance requires an owned running command")
	}
	if session.Status == "running" && !steering {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("Hermes active command ownership is unconfirmed")
	}
	session.sending = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); session.sending = false; a.mu.Unlock() }()
	text, _ := req.Fields["text"].(string)
	attachments, _ := req.Fields["attachments"].([]any)
	if attachments == nil {
		if rawAtt, ok := req.Fields["attachments"].([]map[string]any); ok {
			for _, m := range rawAtt {
				attachments = append(attachments, m)
			}
		}
	}

	if text == "" && len(attachments) == 0 {
		return core.Result{}, fmt.Errorf("hermes command text or attachments are required")
	}
	if err := validateAttachments(attachments); err != nil {
		return core.Result{}, err
	}
	if steering {
		if len(attachments) != 0 {
			return core.Result{}, core.RejectBeforeDispatch("attachments must be queued for the next Hermes turn")
		}
		a.mu.RLock()
		handle := session.Handle
		a.mu.RUnlock()
		response, err := a.rpc(ctx, "session.steer", map[string]any{"session_id": handle, "text": text})
		if err != nil {
			return core.Result{}, err
		}
		if response["status"] != "queued" {
			return core.Result{}, fmt.Errorf("Hermes did not accept the follow-up")
		}
		a.mu.RLock()
		current := session.Status
		a.mu.RUnlock()
		commandID, _ := req.Fields["command_id"].(string)
		if a.config.Emit != nil && commandID != "" {
			if err := a.config.Emit(session.SessionID, "hermes.command_complete", map[string]any{"agent_id": a.config.AgentID, "session_id": session.SessionID, "command_id": commandID}, current); err != nil {
				return core.Result{}, err
			}
		}
		return core.Result{SessionID: session.SessionID, Status: current, Payload: map[string]any{"accepted": true, "completed": true}}, nil
	}

	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, fmt.Errorf("failed to resume session: %w", err)
	}

	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "/") && len(attachments) == 0 {
		slashRes, err := a.handleSlash(ctx, handle, trimmed)
		if err != nil {
			return core.Result{}, err
		}
		if completed, _ := slashRes["completed"].(bool); completed {
			commandID, _ := req.Fields["command_id"].(string)
			if a.config.Emit != nil && commandID != "" {
				if err := a.config.Emit(session.SessionID, "hermes.command_complete", map[string]any{"agent_id": a.config.AgentID, "session_id": session.SessionID, "command_id": commandID, "output": slashRes["output"]}, "idle"); err != nil {
					return core.Result{}, err
				}
			}
			return core.Result{
				SessionID: session.SessionID,
				Status:    "idle",
				Payload: map[string]any{
					"accepted":  true,
					"completed": true,
					"output":    slashRes["output"],
				},
			}, nil
		}
		if newMsg, ok := slashRes["message"].(string); ok && newMsg != "" {
			text = newMsg
		}
	}

	text, err = a.attachInputs(ctx, handle, text, attachments)
	if err != nil {
		return core.Result{}, err
	}

	submitParams := map[string]any{
		"session_id": handle,
		"text":       text,
		"surface":    "hud",
	}
	a.mu.Lock()
	session.CommandID, _ = req.Fields["command_id"].(string)
	session.Status = "running"
	a.mu.Unlock()
	if a.config.Emit != nil {
		if err := a.config.Emit(session.SessionID, "session.active", map[string]any{"agent_id": a.config.AgentID, "session_id": session.SessionID}, "running"); err != nil {
			a.failCloseSession(session.SessionID, err)
			return core.Result{}, err
		}
	}
	_, err = a.rpc(ctx, "prompt.submit", submitParams)
	if err != nil {
		a.mu.Lock()
		if !a.failed && !a.closed {
			session.Status = "error"
			session.Metadata["last_error"] = err.Error()
		}
		a.mu.Unlock()
		if a.config.Emit != nil {
			if emitErr := a.config.Emit(session.SessionID, "session.error", map[string]any{"agent_id": a.config.AgentID, "session_id": session.SessionID}, "error"); emitErr != nil {
				return core.Result{}, fmt.Errorf("prompt.submit failed: %w; status publish failed: %v", err, emitErr)
			}
		}
		return core.Result{}, fmt.Errorf("prompt.submit failed: %w", err)
	}

	a.mu.RLock()
	status := session.Status
	a.mu.RUnlock()

	return core.Result{
		SessionID: session.SessionID,
		Status:    status,
		Payload:   map[string]any{"accepted": true},
	}, nil
}

func (a *Adapter) handleSlash(ctx context.Context, handle, command string) (map[string]any, error) {
	return a.resolveSlash(ctx, handle, command, map[string]bool{})
}

func (a *Adapter) resolveSlash(ctx context.Context, handle, command string, visited map[string]bool) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := strings.TrimPrefix(strings.TrimSpace(command), "/")
	if visited[key] {
		return nil, fmt.Errorf("slash alias cycle detected")
	}
	if len(visited) >= 32 {
		return nil, fmt.Errorf("slash alias depth limit exceeded")
	}
	visited[key] = true
	parts := strings.SplitN(strings.TrimPrefix(command, "/"), " ", 2)
	name := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	dispatched, err := a.rpc(ctx, "command.dispatch", map[string]any{
		"session_id": handle,
		"name":       name,
		"arg":        arg,
	})
	// Only explicit routing refusals prove dispatch never executed the command.
	// A timeout or native execution error must not start the same work again.
	var dispatchError *rpcErrorObject
	if errors.As(err, &dispatchError) && (dispatchError.Code == -32601 || dispatchError.Code == 4018) {
		dispatched, err = a.rpc(ctx, "slash.exec", map[string]any{
			"session_id": handle,
			"command":    command,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("slash command failed: %w", err)
	}

	directive, _ := dispatched["type"].(string)
	if directive == "alias" {
		if target, ok := dispatched["target"].(string); ok {
			return a.resolveSlash(ctx, handle, target, visited)
		}
	}
	if directive == "send" || directive == "skill" {
		return map[string]any{"message": dispatched["message"]}, nil
	}
	if directive == "prefill" {
		notice, _ := dispatched["notice"].(string)
		if notice == "" {
			notice = "该命令需要补充参数"
		}
		return nil, fmt.Errorf("%s", notice)
	}
	output := dispatched["output"]
	if output == nil {
		output = dispatched["display"]
	}
	if output == nil {
		output = dispatched["notice"]
	}
	if output == nil {
		output = "命令已执行"
	}
	return map[string]any{"completed": true, "output": fmt.Sprint(output)}, nil
}

func (a *Adapter) handleInterrupt(ctx context.Context, session *SessionState) (core.Result, error) {
	a.mu.RLock()
	ownedHandle := session.Handle
	a.mu.RUnlock()
	activeRes, err := a.rpc(ctx, "session.active_list", map[string]any{})
	if err != nil {
		return core.Result{}, fmt.Errorf("failed to list active sessions: %w", err)
	}

	var matchHandle string
	if rows, ok := activeRes["sessions"].([]any); ok {
		for _, rowAny := range rows {
			if row, ok := rowAny.(map[string]any); ok {
				if row["session_key"] == session.SessionID || (ownedHandle != "" && row["id"] == ownedHandle) {
					matchHandle, _ = row["id"].(string)
					break
				}
			}
		}
	}

	if matchHandle == "" {
		matchHandle = ownedHandle
	}
	if matchHandle == "" {
		return core.Result{}, fmt.Errorf("no active session handle found to interrupt")
	}

	_, err = a.rpc(ctx, "session.interrupt", map[string]any{
		"session_id": matchHandle,
	})
	if err != nil {
		return core.Result{}, fmt.Errorf("session.interrupt failed: %w", err)
	}

	a.mu.Lock()
	session.Status = "idle"
	a.mu.Unlock()

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload:   map[string]any{"accepted": true},
	}, nil
}

func (a *Adapter) handleRename(ctx context.Context, session *SessionState, req core.Request) (core.Result, error) {
	title, _ := req.Fields["title"].(string)
	if strings.TrimSpace(title) == "" {
		return core.Result{}, fmt.Errorf("title is required")
	}
	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, err
	}

	_, err = a.rpc(ctx, "session.title", map[string]any{
		"session_id": handle,
		"title":      title,
	})
	if err != nil {
		return core.Result{}, err
	}
	verified, err := a.rpc(ctx, "session.title", map[string]any{"session_id": handle})
	if err != nil {
		return core.Result{}, err
	}
	if verified["title"] != title {
		return core.Result{}, fmt.Errorf("native title readback did not match")
	}

	a.mu.Lock()
	session.Title = title
	a.mu.Unlock()

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload:   map[string]any{"title": title},
	}, nil
}

func (a *Adapter) handleDelete(ctx context.Context, session *SessionState) (core.Result, error) {
	_, err := a.rpc(ctx, "session.delete", map[string]any{
		"session_id": session.SessionID,
	})
	if err != nil {
		if rpcErr, ok := err.(*rpcErrorObject); ok && rpcErr.Code == 4023 {
			activeRes, activeErr := a.rpc(ctx, "session.active_list", map[string]any{})
			if activeErr == nil {
				if rows, ok := activeRes["sessions"].([]any); ok {
					for _, rowAny := range rows {
						if row, ok := rowAny.(map[string]any); ok {
							if row["session_key"] == session.SessionID {
								status, _ := row["status"].(string)
								if status != "idle" {
									return core.Result{}, fmt.Errorf("refusing to close an active or uncertain native handle for deletion")
								}
								if hid, ok := row["id"].(string); ok && hid != "" {
									if _, closeErr := a.rpc(ctx, "session.close", map[string]any{"session_id": hid}); closeErr != nil {
										return core.Result{}, closeErr
									}
								}
							}
						}
					}
				}
			}
			_, err = a.rpc(ctx, "session.delete", map[string]any{
				"session_id": session.SessionID,
			})
		}
	}

	if err != nil {
		if rpcErr, ok := err.(*rpcErrorObject); ok && rpcErr.Code == 4007 {
			err = nil
		} else {
			return core.Result{}, fmt.Errorf("session.delete failed: %w", err)
		}
	}

	a.mu.Lock()
	delete(a.sessions, session.SessionID)
	if session.Handle != "" {
		delete(a.handleToSession, session.Handle)
	}
	a.mu.Unlock()

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload:   map[string]any{"deleted": session.SessionID},
	}, nil
}

func (a *Adapter) handleModels(ctx context.Context) (core.Result, error) {
	catalog, err := a.rpc(ctx, "model.options", map[string]any{
		"explicit_only":        true,
		"include_unconfigured": false,
	})
	if err != nil {
		return core.Result{}, fmt.Errorf("model.options failed: %w", err)
	}

	var items []map[string]string
	providers, _ := catalog["providers"].([]any)
	for _, pAny := range providers {
		p, ok := pAny.(map[string]any)
		if !ok || p["authenticated"] == false {
			continue
		}
		slug, _ := p["slug"].(string)
		if slug == "" {
			continue
		}
		name, _ := p["name"].(string)
		if name == "" {
			name = slug
		}
		models, _ := p["models"].([]any)
		for _, mAny := range models {
			var modelID string
			if str, ok := mAny.(string); ok {
				modelID = str
			} else if mObj, ok := mAny.(map[string]any); ok {
				if id, ok := mObj["id"].(string); ok {
					modelID = id
				} else if n, ok := mObj["name"].(string); ok {
					modelID = n
				}
			}
			if modelID == "" {
				continue
			}
			items = append(items, map[string]string{
				"provider": slug,
				"model":    modelID,
				"label":    fmt.Sprintf("%s · %s", name, modelID),
			})
		}
	}

	return core.Result{
		Status:  "idle",
		Payload: map[string]any{"items": items},
	}, nil
}

func (a *Adapter) handleCommands(ctx context.Context, session *SessionState) (core.Result, error) {
	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, err
	}
	cat, err := a.rpc(ctx, "commands.catalog", map[string]any{
		"session_id": handle,
	})
	if err != nil {
		return core.Result{}, err
	}

	var items []map[string]any
	pairs, _ := cat["pairs"].([]any)
	meta, _ := cat["commands"].(map[string]any)

	for _, pairAny := range pairs {
		pair, ok := pairAny.([]any)
		if !ok || len(pair) < 2 {
			continue
		}
		rawName, _ := pair[0].(string)
		name := strings.TrimPrefix(rawName, "/")
		if name == "" {
			continue
		}
		desc := fmt.Sprint(pair[1])
		var inputHint any
		if meta != nil {
			if m, ok := meta[rawName].(map[string]any); ok {
				mode, _ := m["argument_mode"].(string)
				if mode == "text" || mode == "mixed" || mode == "options" {
					inputHint = "输入参数"
				}
			}
		}
		items = append(items, map[string]any{
			"name":        name,
			"description": desc,
			"input_hint":  inputHint,
		})
	}

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload:   map[string]any{"items": items},
	}, nil
}

func (a *Adapter) handleModelRead(ctx context.Context, session *SessionState) (core.Result, error) {
	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, err
	}
	resumed, err := a.rpc(ctx, "session.resume", map[string]any{
		"session_id":    session.SessionID,
		"lazy":          true,
		"omit_messages": true,
	})
	if err != nil {
		return core.Result{}, err
	}

	info, _ := resumed["info"].(map[string]any)
	model, _ := info["model"].(string)
	provider, _ := info["provider"].(string)

	a.mu.Lock()
	if model == "" && session.Model != "" {
		model = session.Model
	}
	if provider == "" && session.Provider != "" {
		provider = session.Provider
	}
	a.mu.Unlock()

	reasoningRes, _ := a.rpc(ctx, "config.get", map[string]any{
		"session_id": handle,
		"key":        "reasoning",
	})
	effort, _ := reasoningRes["value"].(string)

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload: map[string]any{
			"model":    model,
			"provider": provider,
			"effort":   effort,
		},
	}, nil
}

func (a *Adapter) handleModelSet(ctx context.Context, session *SessionState, req core.Request) (core.Result, error) {
	provider, _ := req.Fields["provider"].(string)
	model, _ := req.Fields["model"].(string)
	if provider == "" || model == "" {
		return core.Result{}, fmt.Errorf("provider and model are required")
	}
	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, err
	}

	_, err = a.rpc(ctx, "config.set", map[string]any{
		"session_id":              handle,
		"key":                     "model",
		"value":                   fmt.Sprintf("%s --provider %s --session", model, provider),
		"confirm_expensive_model": true,
	})
	if err != nil {
		return core.Result{}, fmt.Errorf("config.set model failed: %w", err)
	}

	confirmed, err := a.handleModelRead(ctx, session)
	if err == nil && confirmed.Payload != nil {
		readModel, _ := confirmed.Payload["model"].(string)
		readProvider, _ := confirmed.Payload["provider"].(string)
		if readModel != "" && readModel != model && !strings.Contains(readModel, model) && !strings.Contains(model, readModel) {
			return core.Result{}, fmt.Errorf("native model readback did not confirm requested binding")
		}
		if provider != "" && readProvider != "" && readProvider != provider && !strings.Contains(readProvider, provider) && !strings.Contains(provider, readProvider) {
			return core.Result{}, fmt.Errorf("native model readback did not confirm requested binding")
		}
	}
	a.mu.Lock()
	session.Model = model
	session.Provider = provider
	a.mu.Unlock()

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload: map[string]any{
			"provider": provider,
			"model":    model,
		},
	}, nil
}

func (a *Adapter) handleReasoningSet(ctx context.Context, session *SessionState, req core.Request) (core.Result, error) {
	effort, _ := req.Fields["effort"].(string)
	if effort == "" {
		return core.Result{}, fmt.Errorf("effort is required")
	}
	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, err
	}

	_, err = a.rpc(ctx, "config.set", map[string]any{
		"session_id": handle,
		"key":        "reasoning",
		"value":      effort,
	})
	if err != nil {
		return core.Result{}, fmt.Errorf("config.set reasoning failed: %w", err)
	}

	readback, err := a.rpc(ctx, "config.get", map[string]any{"session_id": handle, "key": "reasoning"})
	if err != nil {
		return core.Result{}, err
	}
	if readback["value"] != effort {
		return core.Result{}, fmt.Errorf("native reasoning readback did not confirm requested effort")
	}
	a.mu.Lock()
	session.Effort = effort
	a.mu.Unlock()

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload:   map[string]any{"effort": effort},
	}, nil
}

var hermesApprovalMap = map[string]string{
	"manual":      "manual",
	"auto":        "smart",
	"full_access": "off",
}

var hermesApprovalReverse = map[string]string{
	"manual": "manual",
	"smart":  "auto",
	"off":    "full_access",
}

func (a *Adapter) handleApprovalRead(ctx context.Context, session *SessionState) (core.Result, error) {
	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, err
	}

	res, err := a.rpc(ctx, "config.get", map[string]any{
		"session_id": handle,
		"key":        "approvals.mode",
	})
	if err != nil {
		return core.Result{}, err
	}

	val, _ := res["value"].(string)
	mode := hermesApprovalReverse[val]
	if mode == "" {
		return core.Result{SessionID: session.SessionID, Status: "idle", Payload: map[string]any{"mode": "", "raw_unknown": val}}, nil
	}

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload:   map[string]any{"mode": mode},
	}, nil
}

func (a *Adapter) handleApprovalSet(ctx context.Context, session *SessionState, req core.Request) (core.Result, error) {
	mode, _ := req.Fields["mode"].(string)
	nativeVal, ok := hermesApprovalMap[mode]
	if !ok {
		return core.Result{}, fmt.Errorf("unsupported approval mode: %s; choose manual, auto, full_access", mode)
	}
	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, err
	}

	_, err = a.rpc(ctx, "config.set", map[string]any{
		"session_id": handle,
		"key":        "approvals.mode",
		"value":      nativeVal,
	})
	if err != nil {
		return core.Result{}, fmt.Errorf("config.set approvals.mode failed: %w", err)
	}
	readback, err := a.rpc(ctx, "config.get", map[string]any{"session_id": handle, "key": "approvals.mode"})
	if err != nil {
		return core.Result{}, err
	}
	if readback["value"] != nativeVal {
		return core.Result{}, fmt.Errorf("native approval mode readback did not confirm requested value")
	}

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload:   map[string]any{"mode": mode},
	}, nil
}

func (a *Adapter) handleApprove(ctx context.Context, session *SessionState, req core.Request) (core.Result, error) {
	approvalID, _ := req.Fields["approval_id"].(string)
	if approvalID == "" {
		return core.Result{}, fmt.Errorf("approval_id is required")
	}
	choice, _ := req.Fields["choice"].(string)
	if choice == "" {
		choice = "once"
	}

	a.mu.Lock()
	record, exists := a.pendingApprovals[approvalID]
	if !exists || record == nil {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("approval is not pending")
	}
	if record.SessionID != session.SessionID {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("approval is not owned by this session")
	}
	if record.Resolving {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("approval decision is in progress")
	}
	record.Resolving = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); record.Resolving = false; a.mu.Unlock() }()

	var nativeID any
	if exists && record != nil {
		nativeID = record.NativeID
	} else {
		nativeID = approvalID
	}

	if record.Method == "approval" || strings.HasPrefix(fmt.Sprint(nativeID), "srq-") {
		err := a.sendRaw(map[string]any{
			"jsonrpc": "2.0",
			"id":      nativeID,
			"result":  map[string]any{"choice": choice},
		})
		if err != nil {
			return core.Result{}, err
		}
	} else {
		handle, err := a.ensureHandle(ctx, session)
		if err != nil {
			return core.Result{}, err
		}
		_, err = a.rpc(ctx, "approval.respond", map[string]any{
			"session_id": handle,
			"request_id": nativeID,
			"choice":     choice,
		})
		if err != nil {
			return core.Result{}, err
		}
	}

	a.mu.Lock()
	delete(a.pendingApprovals, approvalID)
	if session.Status == "waiting_approval" {
		session.Status = "running"
		for _, pending := range a.pendingApprovals {
			if pending.SessionID == session.SessionID {
				session.Status = "waiting_approval"
				break
			}
		}
	}
	currentStatus := session.Status
	a.mu.Unlock()
	if a.config.Emit != nil {
		state := "approved"
		if choice == "deny" {
			state = "rejected"
		}
		commandID, _ := req.Fields["command_id"].(string)
		if err := a.config.Emit(session.SessionID, "hermes.approval_resolved", map[string]any{"agent_id": a.config.AgentID, "session_id": session.SessionID, "approval_id": approvalID, "native_id": nativeID, "command_id": commandID, "state": state}, currentStatus); err != nil {
			return core.Result{}, err
		}
	}

	return core.Result{
		SessionID: session.SessionID,
		Status:    currentStatus,
		Payload:   map[string]any{"accepted": true},
	}, nil
}

func (a *Adapter) handleHistoryPage(ctx context.Context, session *SessionState, req core.Request) (core.Result, error) {
	limit := 0
	switch value := req.Fields["limit"].(type) {
	case int:
		limit = value
	case float64:
		if value == float64(int(value)) {
			limit = int(value)
		}
	}
	if limit < 1 || limit > 200 {
		return core.Result{}, fmt.Errorf("history limit must be an integer between 1 and 200")
	}
	before, validCursor := req.Fields["before"].(string)
	if value, present := req.Fields["before"]; present && value != nil && !validCursor {
		return core.Result{}, fmt.Errorf("invalid history cursor")
	}
	if len(before) > 512 {
		return core.Result{}, fmt.Errorf("invalid history cursor")
	}

	dbPath := a.historyDBPath()

	if dbPath != "" && !strings.HasPrefix(before, "hermes-rpc:") {
		page, err := readNativePage(ctx, dbPath, session.SessionID, "hermes-local", before, limit)
		if err == nil {
			if before == "" {
				a.mu.RLock()
				handle := session.Handle
				a.mu.RUnlock()
				if handle != "" {
					a.refreshGoal(ctx, handle)
				}
			}
			return core.Result{
				SessionID: session.SessionID,
				Status:    "idle",
				Payload:   page,
			}, nil
		}
	}

	handle, err := a.ensureHandle(ctx, session)
	if err != nil {
		return core.Result{}, err
	}
	histRes, err := a.rpc(ctx, "session.history", map[string]any{
		"session_id": handle,
	})
	if err != nil {
		return core.Result{}, fmt.Errorf("session.history failed: %w", err)
	}

	page, err := gatewayHistoryPage(session.SessionID, histRes, before, limit)
	if err != nil {
		return core.Result{}, err
	}
	if before == "" {
		a.refreshGoal(ctx, handle)
		if usage, usageErr := a.rpc(ctx, "session.usage", map[string]any{"session_id": handle}); usageErr == nil {
			a.handleGatewayEvent(map[string]any{"type": "session.usage", "session_id": handle, "payload": map[string]any{"usage": usage}})
		}
	}
	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload:   page,
	}, nil
}

func (a *Adapter) Snapshot() map[string]core.Result {
	a.mu.RLock()
	defer a.mu.RUnlock()
	res := make(map[string]core.Result, len(a.sessions))
	for id, s := range a.sessions {
		payload := map[string]any{
			"agent_id":   a.config.AgentID,
			"title":      s.Title,
			"workspace":  s.Workspace,
			"session_id": s.SessionID,
		}
		for k, v := range s.Metadata {
			payload[k] = v
		}
		res[id] = core.Result{
			SessionID: id,
			Status:    s.Status,
			Payload:   payload,
		}
	}
	return res
}

func (a *Adapter) Close() error {
	a.mu.Lock()
	if a.closed {
		err := a.cleanupErr
		a.mu.Unlock()
		return err
	}
	a.closed = true
	a.eventStopOnce.Do(func() { close(a.eventStop) })
	cmd := a.cmd
	owner := a.owner
	sshSess := a.sshSession
	stdin := a.stdin
	processDone := a.processDone
	a.cmd = nil
	a.sshSession = nil
	a.stdin = nil
	a.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if sshSess != nil {
		_ = sshSess.Close()
	}
	if processDone != nil {
		select {
		case <-processDone:
		case <-time.After(1 * time.Second):
			if owner != nil {
				_ = owner.Close()
			} else if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			select {
			case <-processDone:
			case <-time.After(time.Second):
				a.mu.Lock()
				a.cleanupErr = fmt.Errorf("native process exit was not confirmed")
				a.mu.Unlock()
			}
		}
	}
	a.mu.RLock()
	err := a.cleanupErr
	a.mu.RUnlock()
	return err
}
