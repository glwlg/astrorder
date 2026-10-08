package grok

import (
	nativeprocess "astrorder.dev/session-daemon/internal/process"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	platform "runtime"
	"strings"
	"sync"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

var approvalMethods = map[string]struct{}{
	"item/permissions/requestApproval":      {},
	"item/commandExecution/requestApproval": {},
	"item/fileChange/requestApproval":       {},
	"session/request_permission":            {},
}

type EmitFunc func(sessionID, event string, payload map[string]any, status string) error

type Config struct {
	Executable string
	Arguments  []string
	Workspace  string
	Allowed    []string
	AgentID    string
	AgentName  string
	Emit       func(sessionID, event string, payload map[string]any, status string) error
}

type pendingRequest struct {
	response chan map[string]any
	err      chan error
}

type client struct {
	command         *exec.Cmd
	owner           *nativeprocess.CommandOwner
	processDone     chan struct{}
	cleanupErr      error
	closeOnce       sync.Once
	closeErr        error
	onClosed        func()
	stdin           io.WriteCloser
	writeMu         sync.Mutex
	mu              sync.Mutex
	pending         map[int]pendingRequest
	nextID          int
	closed          bool
	transportFailed bool
	onNotification  func(frame map[string]any)
	onFailure       func(error)
	events          chan func()
	eventStop       chan struct{}
	eventDone       chan struct{}
	stopOnce        sync.Once
	done            chan struct{}
}

func newClient(cmd *exec.Cmd, stdin io.WriteCloser, onNotification func(map[string]any)) *client {
	c := &client{
		command:        cmd,
		stdin:          stdin,
		pending:        make(map[int]pendingRequest),
		nextID:         1,
		onNotification: onNotification,
		done:           make(chan struct{}),
		events:         make(chan func(), 256), eventStop: make(chan struct{}), eventDone: make(chan struct{}),
	}
	go c.dispatchEvents()
	return c
}

func (c *client) request(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	c.mu.Lock()
	if c.closed || c.transportFailed || c.stdin == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("grok runtime is closed")
	}
	id := c.nextID
	c.nextID++
	waiter := pendingRequest{response: make(chan map[string]any, 1), err: make(chan error, 1)}
	c.pending[id] = waiter
	c.mu.Unlock()

	frame := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.send(frame); err != nil {
		c.removePending(id)
		return nil, err
	}

	select {
	case response := <-waiter.response:
		return response, nil
	case err := <-waiter.err:
		return nil, err
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	}
}

func (c *client) removePending(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *client) send(frame map[string]any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	if c.closed || c.transportFailed || c.stdin == nil {
		c.mu.Unlock()
		return fmt.Errorf("grok runtime is closed")
	}
	stdin := c.stdin
	c.mu.Unlock()
	return json.NewEncoder(stdin).Encode(frame)
}

func (c *client) failPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int]pendingRequest)
	c.mu.Unlock()
	for _, waiter := range pending {
		waiter.err <- err
	}
}

func (c *client) readLoop(stdout io.Reader) {
	decoder := json.NewDecoder(stdout)
	for {
		var frame map[string]any
		if err := decoder.Decode(&frame); err != nil {
			c.fail(err)
			return
		}

		// Response to a request sent by client
		if _, hasMethod := frame["method"].(string); !hasMethod {
			if idVal, hasID := frame["id"]; hasID {
				id := toInt(idVal)
				c.mu.Lock()
				waiter, exists := c.pending[id]
				delete(c.pending, id)
				c.mu.Unlock()

				if exists && waiter.response != nil {
					if rpcErr, ok := frame["error"]; ok {
						waiter.err <- fmt.Errorf("grok RPC error: %v", rpcErr)
						continue
					}
					if rawResult, ok := frame["result"]; ok {
						if resMap, isMap := rawResult.(map[string]any); isMap {
							waiter.response <- resMap
						} else {
							waiter.response <- map[string]any{"value": rawResult}
						}
						continue
					}
					waiter.err <- fmt.Errorf("grok response missing result")
				}
				continue
			}
		} else {
			// Method is present: either a server request (has id) or a notification (no id)
			if c.onNotification != nil {
				if !c.queueEvent(func() { c.onNotification(frame) }) {
					return
				}
			}
			continue
		}
	}
}

func (c *client) close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		stdin, owner, done := c.stdin, c.owner, c.processDone
		c.mu.Unlock()
		c.stopOnce.Do(func() { close(c.eventStop) })
		close(c.done)
		c.failPending(fmt.Errorf("grok runtime is closed"))
		if owner != nil {
			c.closeErr = owner.Close()
		} else if stdin != nil {
			_ = stdin.Close()
		}
		if done != nil {
			select {
			case <-done:
				c.mu.Lock()
				c.closeErr = errors.Join(c.closeErr, c.cleanupErr)
				c.mu.Unlock()
			case <-time.After(5 * time.Second):
				c.closeErr = errors.Join(c.closeErr, fmt.Errorf("grok native process exit was not confirmed"))
			}
		}
		select {
		case <-c.eventDone:
		case <-time.After(time.Second):
			c.closeErr = errors.Join(c.closeErr, fmt.Errorf("grok projection dispatcher did not exit"))
		}
		if c.closeErr == nil && c.onClosed != nil {
			c.onClosed()
		}
	})
	return c.closeErr
}

type ownedSession struct {
	failed           bool
	closed           bool
	sessionID        string
	workspace        string
	client           *client
	mu               sync.Mutex
	status           string
	promptCancel     context.CancelFunc
	promptRunning    bool
	models           []map[string]any
	model            string
	effort           string
	commands         []map[string]any
	lastError        string
	pendingApprovals map[string]any
	approvalKinds    map[string]string
}

type Adapter struct {
	clients    map[*client]bool
	closeOnce  sync.Once
	closeErr   error
	config     Config
	mu         sync.RWMutex
	sessions   map[string]*ownedSession
	sshStarter SSHStarter
	closed     bool
}

func New(config Config) *Adapter {
	if config.AgentID == "" {
		config.AgentID = "local-grok"
	}
	if config.AgentName == "" {
		config.AgentName = "Grok Build"
	}
	return &Adapter{
		config:   config,
		sessions: make(map[string]*ownedSession),
		clients:  make(map[*client]bool),
	}
}

func (a *Adapter) Create(ctx context.Context, req core.Request) (core.Result, error) {
	rawCwd := ""
	if req.Fields != nil {
		if c, ok := req.Fields["cwd"].(string); ok && c != "" {
			rawCwd = c
		} else if w, ok := req.Fields["workspace"].(string); ok && w != "" {
			rawCwd = w
		}
	}
	workspace, err := a.workspace(rawCwd)
	if err != nil {
		return core.Result{}, err
	}

	initClient, err := a.startClient(ctx, workspace, nil)
	if err != nil {
		return core.Result{}, err
	}

	initResp, err := initClient.request(ctx, "initialize", initializeParams())
	if err != nil {
		return core.Result{}, err
	}

	newResp, err := initClient.request(ctx, "session/new", map[string]any{
		"cwd":        workspace,
		"mcpServers": []any{},
	})
	if err != nil {
		return core.Result{}, err
	}

	sessionID, _ := newResp["sessionId"].(string)
	if sessionID == "" {
		initClient.close()
		return core.Result{}, fmt.Errorf("grok did not confirm a session ID")
	}

	_ = initClient.close()

	owned, err := a.openSession(ctx, sessionID, workspace, true)
	if err != nil {
		return core.Result{}, err
	}

	currModel := currentModel(initResp)
	if currModel != "" {
		owned.mu.Lock()
		if owned.model == "" {
			owned.model = currModel
		}
		owned.mu.Unlock()
	}

	owned.mu.Lock()
	res := core.Result{
		SessionID: sessionID,
		Status:    "idle",
		Payload: map[string]any{
			"session_id": sessionID,
			"status":     "idle",
			"model":      owned.model,
			"effort":     owned.effort,
		},
	}
	owned.mu.Unlock()
	return res, nil
}

func (a *Adapter) Spawn(ctx context.Context, req core.Request) (core.Result, error) {
	sessionID := req.SessionID
	if sessionID == "" && req.Fields != nil {
		if s, ok := req.Fields["session_id"].(string); ok {
			sessionID = s
		}
	}
	if sessionID == "" {
		return core.Result{}, fmt.Errorf("grok session ID is required")
	}

	rawCwd := ""
	if req.Fields != nil {
		if c, ok := req.Fields["cwd"].(string); ok && c != "" {
			rawCwd = c
		} else if w, ok := req.Fields["workspace"].(string); ok && w != "" {
			rawCwd = w
		}
	}
	workspace, err := a.workspace(rawCwd)
	if err != nil {
		return core.Result{}, err
	}

	a.mu.RLock()
	existing := a.sessions[sessionID]
	a.mu.RUnlock()
	if existing != nil {
		existing.mu.Lock()
		defer existing.mu.Unlock()
		return core.Result{
			SessionID: sessionID,
			Status:    existing.status,
			Payload: map[string]any{
				"session_id": sessionID,
				"status":     existing.status,
				"model":      existing.model,
				"effort":     existing.effort,
			},
		}, nil
	}

	owned, err := a.openSession(ctx, sessionID, workspace, true)
	if err != nil {
		return core.Result{}, err
	}

	owned.mu.Lock()
	res := core.Result{
		SessionID: sessionID,
		Status:    "idle",
		Payload: map[string]any{
			"session_id": sessionID,
			"status":     "idle",
			"model":      owned.model,
			"effort":     owned.effort,
		},
	}
	owned.mu.Unlock()
	return res, nil
}

func (a *Adapter) Command(ctx context.Context, req core.Request) (core.Result, error) {
	sessionID := req.SessionID
	if sessionID == "" && req.Fields != nil {
		if s, ok := req.Fields["session_id"].(string); ok {
			sessionID = s
		}
	}
	if sessionID == "" {
		return core.Result{}, fmt.Errorf("grok session ID is required")
	}

	a.mu.RLock()
	owned := a.sessions[sessionID]
	a.mu.RUnlock()
	if owned == nil {
		return core.Result{}, fmt.Errorf("grok session is not daemon-owned")
	}
	owned.mu.Lock()
	unavailable := owned.failed || owned.closed
	owned.mu.Unlock()
	if unavailable && req.Action != "session.close" && req.Action != "session.delete" {
		return core.Result{}, fmt.Errorf("grok session transport is unavailable")
	}

	action := req.Action
	if action == "" && req.Fields != nil {
		if act, ok := req.Fields["action"].(string); ok {
			action = act
		}
	}

	switch action {
	case "session.send":
		return a.handleSend(owned, sessionID, req)
	case "session.interrupt":
		return a.handleInterrupt(owned, sessionID)
	case "session.models":
		owned.mu.Lock()
		items := make([]map[string]any, len(owned.models))
		copy(items, owned.models)
		status := owned.status
		owned.mu.Unlock()
		return core.Result{
			SessionID: sessionID,
			Status:    status,
			Payload:   map[string]any{"items": items},
		}, nil
	case "session.commands":
		owned.mu.Lock()
		items := make([]map[string]any, len(owned.commands))
		copy(items, owned.commands)
		status := owned.status
		owned.mu.Unlock()
		return core.Result{
			SessionID: sessionID,
			Status:    status,
			Payload:   map[string]any{"items": items},
		}, nil
	case "session.model.read":
		owned.mu.Lock()
		model, effort, status := owned.model, owned.effort, owned.status
		owned.mu.Unlock()
		return core.Result{
			SessionID: sessionID,
			Status:    status,
			Payload: map[string]any{
				"provider": "grok",
				"model":    model,
				"effort":   effort,
			},
		}, nil
	case "session.model.set":
		return a.handleModelSet(ctx, owned, sessionID, req)
	case "session.reasoning.set":
		return a.handleReasoningSet(ctx, owned, sessionID, req)
	case "session.approve":
		return a.handleApprove(owned, sessionID, req)
	case "session.close":
		owned.mu.Lock()
		owned.closed = true
		if owned.promptCancel != nil {
			owned.promptCancel()
		}
		c := owned.client
		owned.mu.Unlock()
		if c != nil {
			if err := c.close(); err != nil {
				owned.mu.Lock()
				owned.status = "error"
				owned.failed = true
				owned.lastError = err.Error()
				owned.mu.Unlock()
				return core.Result{}, err
			}
		}
		a.mu.Lock()
		if a.sessions[sessionID] == owned {
			delete(a.sessions, sessionID)
		}
		a.mu.Unlock()
		return core.Result{SessionID: sessionID, Status: "idle", Payload: map[string]any{"status": "idle", "closed": true}}, nil
	default:
		return core.Result{}, fmt.Errorf("grok runtime action is unsupported")
	}
}

func (a *Adapter) handleSend(owned *ownedSession, sessionID string, req core.Request) (core.Result, error) {
	owned.mu.Lock()
	defer owned.mu.Unlock()

	var promptStr string
	var hasPrompt bool
	if req.Fields != nil {
		if p, ok := req.Fields["prompt"].(string); ok {
			promptStr = p
			hasPrompt = true
		}
	}
	var rawAttachments []any
	if req.Fields != nil {
		if att, ok := req.Fields["attachments"].([]any); ok {
			rawAttachments = att
		}
	}

	if !hasPrompt && len(rawAttachments) == 0 {
		return core.Result{}, fmt.Errorf("grok prompt must be non-empty")
	}

	if owned.promptRunning {
		return core.Result{}, fmt.Errorf("grok session already has an active turn")
	}

	commandID := ""
	if req.Fields != nil {
		if cid, ok := req.Fields["command_id"].(string); ok {
			commandID = cid
		}
	}
	if commandID == "" {
		return core.Result{}, fmt.Errorf("grok command ID is required")
	}

	owned.status = "running"
	owned.promptRunning = true
	owned.lastError = ""

	promptCtx, cancel := context.WithCancel(context.Background())
	owned.promptCancel = cancel

	go a.executePrompt(promptCtx, owned, sessionID, commandID, promptStr, rawAttachments)

	return core.Result{
		SessionID: sessionID,
		Status:    "running",
		Payload:   map[string]any{"status": "running", "accepted": true},
	}, nil
}

func (a *Adapter) executePrompt(ctx context.Context, owned *ownedSession, sessionID, commandID, prompt string, rawAttachments []any) {
	promptBlocks := make([]map[string]any, 0)
	if prompt != "" {
		promptBlocks = append(promptBlocks, map[string]any{"type": "text", "text": prompt})
	}
	for _, attVal := range rawAttachments {
		att, ok := attVal.(map[string]any)
		if !ok {
			continue
		}
		mediaType, _ := att["media_type"].(string)
		contentBase64, _ := att["content_base64"].(string)
		if strings.HasPrefix(mediaType, "image/") && contentBase64 != "" {
			promptBlocks = append(promptBlocks, map[string]any{
				"type":     "image",
				"data":     contentBase64,
				"mimeType": mediaType,
			})
		} else {
			name, _ := att["name"].(string)
			if name == "" {
				name = "attachment"
			}
			text, _ := att["text"].(string)
			promptBlocks = append(promptBlocks, map[string]any{
				"type": "text",
				"text": strings.TrimSpace(fmt.Sprintf("\n\n[Attachment: %s]\n%s", name, text)),
			})
		}
	}
	if len(promptBlocks) == 0 {
		promptBlocks = append(promptBlocks, map[string]any{"type": "text", "text": ""})
	}

	result, err := owned.client.request(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    promptBlocks,
	})

	var status string
	var payload map[string]any

	if ctx.Err() != nil {
		status = "idle"
		payload = map[string]any{"command_id": commandID, "cancelled": true}
	} else if err != nil {
		status = "error"
		owned.mu.Lock()
		errMsg := owned.lastError
		owned.mu.Unlock()
		if errMsg == "" {
			errMsg = err.Error()
		}
		if len(errMsg) > 1000 {
			errMsg = errMsg[:1000]
		}
		payload = map[string]any{"command_id": commandID, "error": errMsg}
	} else {
		status = "idle"
		payload = map[string]any{"command_id": commandID, "result": result}
		if result["stopReason"] == "cancelled" {
			payload["cancelled"] = true
		}
	}

	owned.client.queueEvent(func() {
		owned.mu.Lock()
		if owned.closed || owned.failed {
			owned.mu.Unlock()
			return
		}
		owned.status = status
		owned.promptRunning = false
		cancel := owned.promptCancel
		owned.promptCancel = nil
		owned.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		completedPayload := map[string]any{"agent_id": a.config.AgentID}
		for k, v := range payload {
			completedPayload[k] = v
		}
		if a.config.Emit != nil {
			if err := a.config.Emit(sessionID, "grok.completed", completedPayload, status); err != nil {
				owned.client.fail(err)
			}
		}
	})
}

func (a *Adapter) handleInterrupt(owned *ownedSession, sessionID string) (core.Result, error) {
	if err := owned.client.send(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": sessionID}}); err != nil {
		return core.Result{}, err
	}
	// ACP cancellation is a notification, not an acknowledgement of termination.
	// Only the pending prompt's native response may clear the running lease.
	owned.mu.Lock()
	status := owned.status
	owned.mu.Unlock()
	return core.Result{SessionID: sessionID, Status: status, Payload: map[string]any{"status": status, "interrupted": true}}, nil
}

func (a *Adapter) handleModelSet(ctx context.Context, owned *ownedSession, sessionID string, req core.Request) (core.Result, error) {
	var model string
	if req.Fields != nil {
		model, _ = req.Fields["model"].(string)
	}
	if model == "" {
		return core.Result{}, fmt.Errorf("grok model is unavailable")
	}

	owned.mu.Lock()
	available := false
	for _, m := range owned.models {
		if m["model"] == model {
			available = true
			break
		}
	}
	owned.mu.Unlock()

	if !available {
		return core.Result{}, fmt.Errorf("grok model is unavailable")
	}

	_, err := owned.client.request(ctx, "session/set_model", map[string]any{
		"sessionId": sessionID,
		"modelId":   model,
	})
	if err != nil {
		return core.Result{}, err
	}

	owned.mu.Lock()
	owned.model = model
	status := owned.status
	owned.mu.Unlock()

	return core.Result{
		SessionID: sessionID,
		Status:    status,
		Payload: map[string]any{
			"provider": "grok",
			"model":    model,
		},
	}, nil
}

func (a *Adapter) handleReasoningSet(ctx context.Context, owned *ownedSession, sessionID string, req core.Request) (core.Result, error) {
	var effort string
	if req.Fields != nil {
		effort, _ = req.Fields["effort"].(string)
	}
	if effort == "" {
		return core.Result{}, fmt.Errorf("grok reasoning effort is unavailable")
	}

	owned.mu.Lock()
	var choices []string
	for _, m := range owned.models {
		if m["model"] == owned.model {
			if effList, ok := m["efforts"].([]string); ok {
				choices = effList
			}
			break
		}
	}
	owned.mu.Unlock()

	valid := false
	for _, ch := range choices {
		if ch == effort {
			valid = true
			break
		}
	}
	if !valid {
		return core.Result{}, fmt.Errorf("grok reasoning effort is unavailable")
	}

	_, err := owned.client.request(ctx, "session/set_config_option", map[string]any{
		"sessionId": sessionID,
		"configId":  "reasoning_effort",
		"value":     effort,
	})
	if err != nil {
		return core.Result{}, err
	}

	owned.mu.Lock()
	owned.effort = effort
	status := owned.status
	owned.mu.Unlock()

	return core.Result{
		SessionID: sessionID,
		Status:    status,
		Payload:   map[string]any{"effort": effort},
	}, nil
}

func (a *Adapter) handleApprove(owned *ownedSession, sessionID string, req core.Request) (core.Result, error) {
	var approvalID, decision string
	var permissions map[string]any
	if req.Fields != nil {
		approvalID, _ = req.Fields["approval_id"].(string)
		decision, _ = req.Fields["decision"].(string)
		permissions, _ = req.Fields["permissions"].(map[string]any)
	}

	if approvalID == "" || (decision != "accept" && decision != "decline") {
		return core.Result{}, fmt.Errorf("grok approval is invalid")
	}

	owned.mu.Lock()
	nativeID, ok := owned.pendingApprovals[approvalID]
	kind := owned.approvalKinds[approvalID]
	if !ok {
		owned.mu.Unlock()
		return core.Result{}, fmt.Errorf("grok approval is not pending for this session")
	}

	var result map[string]any
	if kind == "item/permissions/requestApproval" || kind == "session/request_permission" {
		perms := map[string]any{}
		if decision == "accept" {
			if permissions == nil {
				owned.mu.Unlock()
				return core.Result{}, fmt.Errorf("permission approval requires explicit granted permissions")
			}
			perms = permissions
		}
		result = map[string]any{"permissions": perms, "scope": "turn"}
	} else {
		result = map[string]any{"decision": decision}
	}

	delete(owned.pendingApprovals, approvalID)
	delete(owned.approvalKinds, approvalID)
	if len(owned.pendingApprovals) == 0 {
		if owned.promptRunning {
			owned.status = "running"
		} else {
			owned.status = "idle"
		}
	}
	status := owned.status
	owned.mu.Unlock()

	if err := owned.client.send(map[string]any{"id": nativeID, "result": result}); err != nil {
		return core.Result{}, err
	}

	return core.Result{
		SessionID: sessionID,
		Status:    status,
		Payload:   map[string]any{"status": status, "accepted": true},
	}, nil
}

func (a *Adapter) openSession(ctx context.Context, sessionID, workspace string, load bool) (*ownedSession, error) {
	owned := &ownedSession{
		sessionID:        sessionID,
		workspace:        workspace,
		status:           "idle",
		pendingApprovals: make(map[string]any),
		approvalKinds:    make(map[string]string),
	}

	onNotif := func(frame map[string]any) {
		a.handleFrameNotification(owned, sessionID, frame)
	}

	client, err := a.startClient(ctx, workspace, onNotif, func(err error) { a.failSession(owned, err) })
	if err != nil {
		return nil, err
	}
	owned.client = client

	initResp, err := client.request(ctx, "initialize", initializeParams())
	if err != nil {
		client.close()
		return nil, err
	}

	var loadedResp map[string]any
	if load {
		loadedResp, err = client.request(ctx, "session/load", map[string]any{
			"sessionId":  sessionID,
			"cwd":        workspace,
			"mcpServers": []any{},
		})
		if err != nil {
			client.close()
			return nil, err
		}
		if sid, _ := loadedResp["sessionId"].(string); sid != "" && sid != sessionID {
			client.close()
			return nil, fmt.Errorf("grok loaded a different session")
		}
	}

	models := parseModels(initResp)
	defaultMod := currentModel(initResp)
	model, effort := parseLoadedSettings(loadedResp, defaultMod)

	// Resolve model by matching model ID or label
	for _, m := range models {
		if model == m["model"] || model == m["label"] {
			if modStr, ok := m["model"].(string); ok {
				model = modStr
			}
			break
		}
	}

	owned.mu.Lock()
	owned.models = models
	owned.model = model
	owned.effort = effort
	owned.mu.Unlock()

	a.mu.Lock()
	a.sessions[sessionID] = owned
	a.mu.Unlock()

	if err := nativeprocess.ReportOwner(a.config.Emit, sessionID, "idle", clientPID(owned.client)); err != nil {
		owned.client.close()
		a.mu.Lock()
		delete(a.sessions, sessionID)
		a.mu.Unlock()
		return nil, err
	}

	return owned, nil
}

func clientPID(c *client) int {
	if c == nil || c.command == nil || c.command.Process == nil {
		return 0
	}
	return c.command.Process.Pid
}

func (a *Adapter) handleFrameNotification(owned *ownedSession, sessionID string, frame map[string]any) {
	method, _ := frame["method"].(string)
	params, _ := frame["params"].(map[string]any)
	if sid, _ := params["sessionId"].(string); sid != "" && sid != sessionID {
		return
	}
	if sid, _ := params["threadId"].(string); sid != "" && sid != sessionID {
		return
	}
	owned.mu.Lock()
	if owned.closed || owned.failed {
		owned.mu.Unlock()
		return
	}
	nativeID, hasID := frame["id"]
	_, isApproval := approvalMethods[method]
	status := ""
	if hasID && isApproval {
		approvalID := "grok:" + nativeRequestID(nativeID)
		owned.pendingApprovals[approvalID] = nativeID
		owned.approvalKinds[approvalID] = method
		owned.status = "waiting_approval"
		status = owned.status
	}
	update, _ := params["update"].(map[string]any)
	if update["sessionUpdate"] == "available_commands_update" {
		owned.commands = parseCommands(update)
	}
	if update["sessionUpdate"] == "retry_state" && update["type"] == "failed" {
		if msg, ok := update["message"].(string); ok {
			owned.lastError = msg
		}
	}
	owned.mu.Unlock()
	if hasID && !isApproval {
		if err := owned.client.send(map[string]any{"jsonrpc": "2.0", "id": nativeID, "error": map[string]any{"code": -32601, "message": "server request is not implemented by this runtime"}}); err != nil {
			owned.client.fail(err)
			return
		}
	}
	if a.config.Emit != nil {
		if err := a.config.Emit(sessionID, "grok.notification", map[string]any{"agent_id": a.config.AgentID, "frame": frame}, status); err != nil {
			owned.client.fail(err)
		}
	}
}

func (a *Adapter) startClient(ctx context.Context, workspace string, onNotification func(map[string]any), onFailure ...func(error)) (*client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.RLock()
	if a.closed {
		a.mu.RUnlock()
		return nil, fmt.Errorf("grok adapter is closed")
	}
	a.mu.RUnlock()

	executable := a.config.Executable
	args := a.config.Arguments
	if len(args) == 0 {
		args = []string{"agent", "stdio"}
	}

	if a.sshStarter != nil {
		spec := ssh.CommandSpec{
			Executable: executable,
			Args:       args,
			Dir:        workspace,
		}
		// Close retires the owned transport independently of this request.
		sess, err := a.sshStarter(context.WithoutCancel(ctx), spec)
		if err != nil {
			return nil, err
		}
		c := newClient(nil, sess.Stdin(), onNotification)
		c.processDone = make(chan struct{})
		if len(onFailure) > 0 {
			c.onFailure = onFailure[0]
		}
		c.onClosed = func() {
			_ = sess.Close()
			a.mu.Lock()
			delete(a.clients, c)
			a.mu.Unlock()
		}
		go c.readLoop(sess.Stdout())
		go func() {
			_, waitErr := sess.Wait()
			c.mu.Lock()
			c.cleanupErr = waitErr
			closed := c.closed
			c.mu.Unlock()
			close(c.processDone)
			if !closed {
				c.fail(fmt.Errorf("grok native process exited"))
			}
		}()
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			return nil, errors.Join(fmt.Errorf("grok adapter closed during startup"), c.close())
		}
		a.clients[c] = true
		a.mu.Unlock()
		return c, nil
	}

	cmd := exec.Command(executable, args...)
	cmd.Dir = workspace
	cmd.Env = nativeprocess.NativeEnvironment()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}

	owner, err := nativeprocess.StartOwnedCommand(cmd)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}

	c := newClient(cmd, stdin, onNotification)
	c.owner = owner
	c.processDone = make(chan struct{})
	if len(onFailure) > 0 {
		c.onFailure = onFailure[0]
	}
	c.onClosed = func() { a.mu.Lock(); delete(a.clients, c); a.mu.Unlock() }
	go c.readLoop(stdout)
	go func() {
		err := owner.WaitAndClose(cmd)
		c.mu.Lock()
		c.cleanupErr = err
		c.mu.Unlock()
		close(c.processDone)
		c.fail(fmt.Errorf("grok native process exited"))
	}()
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, errors.Join(fmt.Errorf("grok adapter closed during startup"), c.close())
	}
	a.clients[c] = true
	a.mu.Unlock()
	return c, nil
}

func (a *Adapter) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		clients := make(map[*client]bool, len(a.clients))
		for c := range a.clients {
			clients[c] = true
		}
		sessions := a.sessions
		a.mu.Unlock()
		for _, s := range sessions {
			s.mu.Lock()
			s.closed = true
			if s.promptCancel != nil {
				s.promptCancel()
			}
			c := s.client
			s.mu.Unlock()
			if c != nil {
				clients[c] = true
			}
		}
		for c := range clients {
			a.closeErr = errors.Join(a.closeErr, c.close())
		}
	})
	return a.closeErr
}

func (a *Adapter) Snapshot() map[string]core.Result {
	a.mu.RLock()
	defer a.mu.RUnlock()
	res := make(map[string]core.Result, len(a.sessions))
	for sid, s := range a.sessions {
		s.mu.Lock()
		res[sid] = core.Result{
			SessionID: sid,
			Status:    s.status,
			Payload: map[string]any{
				"session_id": sid,
				"status":     s.status,
				"model":      s.model,
				"effort":     s.effort,
				"workspace":  s.workspace,
			},
		}
		s.mu.Unlock()
	}
	return res
}

func (a *Adapter) workspace(raw string) (string, error) {
	if raw == "" {
		raw = a.config.Workspace
	}
	if a.sshStarter != nil {
		if raw == "" || strings.ContainsRune(raw, 0) || !strings.HasPrefix(raw, "/") {
			return "", fmt.Errorf("remote grok workspace must be an absolute POSIX path")
		}
		clean := path.Clean(raw)
		if !a.allowedRemote(clean) {
			return "", fmt.Errorf("grok workspace is not allowlisted")
		}
		return clean, nil
	}
	if raw == "" {
		raw = "."
	}
	workspace, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("grok workspace is invalid")
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", err
	}
	if !a.allowed(workspace) {
		return "", fmt.Errorf("grok workspace is not allowlisted")
	}
	return workspace, nil
}

func (a *Adapter) allowedRemote(dir string) bool {
	if len(a.config.Allowed) == 0 {
		return true
	}
	for _, root := range a.config.Allowed {
		cleanRoot := path.Clean(root)
		if cleanRoot == "/" || dir == cleanRoot || strings.HasPrefix(dir, cleanRoot+"/") {
			return true
		}
	}
	return false
}

func (a *Adapter) allowed(workspace string) bool {
	if len(a.config.Allowed) == 0 {
		return true
	}
	for _, root := range a.config.Allowed {
		absolute, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		absolute, err = filepath.EvalSymlinks(absolute)
		if err != nil {
			continue
		}
		candidate := workspace
		if platform.GOOS == "windows" {
			candidate, absolute = strings.ToLower(candidate), strings.ToLower(absolute)
		}
		relative, err := filepath.Rel(absolute, candidate)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative) {
			return true
		}
	}
	return false
}

func initializeParams() map[string]any {
	return map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
	}
}

func currentModel(initialized map[string]any) string {
	meta, _ := initialized["_meta"].(map[string]any)
	if meta == nil {
		return ""
	}
	state, _ := meta["modelState"].(map[string]any)
	if state == nil {
		return ""
	}
	model, _ := state["currentModelId"].(string)
	return model
}

func parseModels(initialized map[string]any) []map[string]any {
	meta, _ := initialized["_meta"].(map[string]any)
	if meta == nil {
		return nil
	}
	state, _ := meta["modelState"].(map[string]any)
	if state == nil {
		return nil
	}
	rows, _ := state["availableModels"].([]any)
	result := make([]map[string]any, 0, len(rows))
	for _, rowVal := range rows {
		row, ok := rowVal.(map[string]any)
		if !ok {
			continue
		}
		modelID, ok := row["modelId"].(string)
		if !ok || modelID == "" {
			continue
		}
		name, _ := row["name"].(string)
		if name == "" {
			name = modelID
		}
		var efforts []string
		rowMeta, _ := row["_meta"].(map[string]any)
		if rowMeta != nil {
			reasoningEfforts, _ := rowMeta["reasoningEfforts"].([]any)
			for _, effVal := range reasoningEfforts {
				eff, isMap := effVal.(map[string]any)
				if isMap {
					if v, hasV := eff["value"].(string); hasV && v != "" {
						efforts = append(efforts, v)
					}
				}
			}
		}
		result = append(result, map[string]any{
			"provider": "grok",
			"model":    modelID,
			"label":    name,
			"efforts":  efforts,
		})
	}
	return result
}

func parseLoadedSettings(loaded map[string]any, defaultModel string) (string, string) {
	if loaded == nil {
		return defaultModel, ""
	}
	values := make(map[string]any)
	if options, ok := loaded["configOptions"].([]any); ok {
		for _, optVal := range options {
			if opt, ok := optVal.(map[string]any); ok {
				if id, ok := opt["id"].(string); ok {
					values[id] = opt["currentValue"]
				}
			}
		}
	}
	var loadedModel string
	if models, ok := loaded["models"].(map[string]any); ok {
		loadedModel, _ = models["currentModelId"].(string)
	}

	model := defaultModel
	if mVal, ok := values["model"].(string); ok && mVal != "" {
		model = mVal
	} else if loadedModel != "" {
		model = loadedModel
	}

	effort, _ := values["reasoning_effort"].(string)
	return model, effort
}

func parseCommands(update map[string]any) []map[string]any {
	rows, _ := update["availableCommands"].([]any)
	if rows == nil {
		rows, _ = update["available_commands"].([]any)
	}
	result := make([]map[string]any, 0, len(rows))
	for _, rowVal := range rows {
		row, ok := rowVal.(map[string]any)
		if !ok {
			continue
		}
		name, _ := row["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := row["description"].(string)
		var hint string
		if inputSpec, ok := row["input"].(map[string]any); ok {
			hint, _ = inputSpec["hint"].(string)
		}
		cmdMap := map[string]any{
			"name":        strings.TrimPrefix(name, "/"),
			"description": desc,
		}
		if hint != "" {
			cmdMap["input_hint"] = hint
		} else {
			cmdMap["input_hint"] = nil
		}
		result = append(result, cmdMap)
	}
	return result
}

func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	default:
		return 0
	}
}

func nativeRequestID(value any) string {
	if number, ok := value.(float64); ok && number == float64(int64(number)) {
		return fmt.Sprintf("%d", int64(number))
	}
	return fmt.Sprint(value)
}
