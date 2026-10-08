package codex

import (
	nativeprocess "astrorder.dev/session-daemon/internal/process"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
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
)

var approvalMethods = map[string]struct{}{
	"item/commandExecution/requestApproval": {},
	"item/fileChange/requestApproval":       {},
	"item/permissions/requestApproval":      {},
}

type Notification struct {
	CommandID  string
	SessionID  string
	Method     string
	ApprovalID string
	Status     string
	Frame      map[string]any
}

type Config struct {
	Environment    []string
	Executable     string
	Arguments      []string
	Workspace      string
	Allowed        []string
	AgentID        string
	OnNotification func(Notification)
}

type Request struct {
	ParentSessionID string
	Title           string
	Params          map[string]any
	Cwd             string
	Env             []string
	SessionID       string
}

type Command struct {
	CommandID   string
	Input       []map[string]any
	Options     map[string]any
	Action      string
	SessionID   string
	TurnID      string
	Text        string
	ApprovalID  string
	Decision    string
	Permissions map[string]any
}

type Result struct {
	Native    map[string]any
	SessionID string
	TurnID    string
	Status    string
	Accepted  bool
}

type pendingRequest struct {
	response chan map[string]any
	err      chan error
}

type Runtime struct {
	config Config

	mu               sync.Mutex
	writeMu          sync.Mutex
	approvalMu       sync.Mutex
	transportFailed  bool
	command          *exec.Cmd
	sshSession       SSHSession
	sshStarter       SSHStarter
	stdin            io.WriteCloser
	pending          map[int]pendingRequest
	pendingHooks     map[string]bool
	pendingApprovals map[string]any
	approvalKinds    map[string]string
	notifications    chan Notification
	done             chan struct{}
	nextID           int
	sessionID        string
	activeTurnID     string
	completedTurns   map[string]string
	starting         bool
	status           string
	closed           bool
	initialized      map[string]any
	pendingCommandID string
	turnCommands     map[string]string
}

func New(config Config) *Runtime {
	r := &Runtime{
		config: config, pending: map[int]pendingRequest{},
		completedTurns:   map[string]string{},
		turnCommands:     map[string]string{},
		pendingApprovals: map[string]any{}, nextID: 1, status: "idle",
		approvalKinds: map[string]string{}, notifications: make(chan Notification, 256), done: make(chan struct{}),
	}
	go r.dispatchNotifications()
	return r
}

func (runtime *Runtime) LocalPID() int {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.sshSession != nil || runtime.command == nil || runtime.command.Process == nil {
		return 0
	}
	return runtime.command.Process.Pid
}

func (runtime *Runtime) dispatchNotifications() {
	for {
		select {
		case notification := <-runtime.notifications:
			if runtime.config.OnNotification != nil {
				runtime.config.OnNotification(notification)
			}
		case <-runtime.done:
			return
		}
	}
}

func (runtime *Runtime) Create(ctx context.Context, request Request) (Result, error) {
	workspace, err := runtime.workspace(request.Cwd)
	if err != nil {
		return Result{}, err
	}
	if err := runtime.start(ctx, workspace, request.Env); err != nil {
		return Result{}, err
	}
	params := map[string]any{
		"cwd": workspace, "ephemeral": false, "persistExtendedHistory": true,
	}
	for k, v := range request.Params {
		params[k] = v
	}
	ephemeral, _ := params["ephemeral"].(bool)
	method := "thread/start"
	params["persistExtendedHistory"] = !ephemeral
	if request.ParentSessionID != "" {
		method = "thread/fork"
		params["threadId"] = request.ParentSessionID
		params["excludeTurns"] = true
		delete(params, "persistExtendedHistory")
		if !ephemeral {
			params["deferGoalContinuation"] = true
		}
	}
	response, err := runtime.request(ctx, method, params)
	if err != nil {
		runtime.Close()
		return Result{}, err
	}
	sessionID := threadID(response)
	if sessionID == "" {
		runtime.Close()
		return Result{}, fmt.Errorf("codex did not return a thread id")
	}
	runtime.setSession(sessionID, "idle")
	if request.Title != "" && !ephemeral {
		if _, err := runtime.request(ctx, "thread/name/set", map[string]any{"threadId": sessionID, "name": request.Title}); err != nil {
			runtime.Close()
			return Result{}, err
		}
		verified, err := runtime.request(ctx, "thread/read", map[string]any{"threadId": sessionID, "includeTurns": false})
		thread, _ := verified["thread"].(map[string]any)
		if err != nil || thread["id"] != sessionID || thread["name"] != request.Title {
			runtime.Close()
			return Result{}, fmt.Errorf("Codex title was not confirmed by native state")
		}
		response["thread"] = thread
	}
	return Result{SessionID: sessionID, Status: runtime.Status(), Native: response}, nil
}

func (runtime *Runtime) Resume(ctx context.Context, request Request) (Result, error) {
	if request.SessionID == "" || len(request.SessionID) > 256 {
		return Result{}, fmt.Errorf("codex session id is invalid")
	}
	workspace, err := runtime.workspace(request.Cwd)
	if err != nil {
		return Result{}, err
	}
	if err := runtime.start(ctx, workspace, request.Env); err != nil {
		return Result{}, err
	}
	response, err := runtime.request(ctx, "thread/resume", map[string]any{
		"threadId": request.SessionID, "excludeTurns": true, "cwd": workspace,
	})
	if err != nil {
		runtime.Close()
		return Result{}, err
	}
	if threadID(response) != request.SessionID {
		runtime.Close()
		return Result{}, fmt.Errorf("codex did not confirm the resumed thread id")
	}
	runtime.setSession(request.SessionID, "idle")
	return Result{SessionID: request.SessionID, Status: "idle", Native: response}, nil
}

func (runtime *Runtime) start(ctx context.Context, workspace string, environment []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.mu.Lock()
	if runtime.command != nil || runtime.sshSession != nil || runtime.starting || runtime.closed {
		runtime.mu.Unlock()
		return fmt.Errorf("codex runtime already owns a transport")
	}
	runtime.starting = true
	runtime.mu.Unlock()
	defer func() {
		runtime.mu.Lock()
		runtime.starting = false
		runtime.mu.Unlock()
	}()
	arguments := runtime.config.Arguments
	if len(arguments) == 0 {
		arguments = []string{"app-server", "--listen", "stdio://"}
	}

	if runtime.sshStarter != nil {
		spec := ssh.CommandSpec{
			Executable: runtime.config.Executable,
			Args:       arguments,
			Dir:        workspace,
		}
		// The adapter owns transport lifetime; request cancellation only ends RPC waits.
		sess, err := runtime.sshStarter(context.WithoutCancel(ctx), spec)
		if err != nil {
			return err
		}
		runtime.mu.Lock()
		if runtime.closed {
			runtime.mu.Unlock()
			_ = sess.Close()
			return fmt.Errorf("codex runtime closed during startup")
		}
		runtime.sshSession, runtime.stdin, runtime.closed = sess, sess.Stdin(), false
		runtime.mu.Unlock()
		go runtime.readLoop(sess.Stdout())
	} else {
		command := exec.Command(runtime.config.Executable, arguments...)
		command.Dir = workspace
		command.Env = nativeprocess.NativeEnvironment(runtime.config.Environment, environment)
		stdin, err := command.StdinPipe()
		if err != nil {
			return err
		}
		stdout, err := command.StdoutPipe()
		if err != nil {
			return err
		}
		if err := command.Start(); err != nil {
			return err
		}
		runtime.mu.Lock()
		if runtime.closed {
			runtime.mu.Unlock()
			_ = command.Process.Kill()
			_ = command.Wait()
			return fmt.Errorf("codex runtime closed during startup")
		}
		runtime.command, runtime.stdin, runtime.closed = command, stdin, false
		runtime.mu.Unlock()
		go runtime.readLoop(stdout)
	}
	initialized, err := runtime.request(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "astrorder-daemon", "title": "Astrorder Session Daemon", "version": "0.1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil {
		runtime.Close()
		return err
	}
	runtime.mu.Lock()
	runtime.initialized = initialized
	runtime.mu.Unlock()
	if err := runtime.notify("initialized", map[string]any{}); err != nil {
		runtime.Close()
		return err
	}
	return nil
}

func (runtime *Runtime) Command(ctx context.Context, command Command) (Result, error) {
	if err := runtime.requireSession(command.SessionID); err != nil {
		return Result{}, err
	}
	switch command.Action {
	case "session.send":
		if len(command.Input) == 0 && command.Text == "" {
			return Result{}, fmt.Errorf("codex prompt must be non-empty")
		}
		inputs := command.Input
		if len(inputs) == 0 {
			inputs = []map[string]any{{"type": "text", "text": command.Text}}
		}
		params := map[string]any{"threadId": command.SessionID, "input": inputs}
		for key, value := range command.Options {
			params[key] = value
		}
		runtime.mu.Lock()
		runtime.pendingCommandID = command.CommandID
		runtime.mu.Unlock()
		defer func() { runtime.mu.Lock(); runtime.pendingCommandID = ""; runtime.mu.Unlock() }()
		response, err := runtime.request(ctx, "turn/start", params)
		if err != nil {
			return Result{}, err
		}
		turn, _ := response["turn"].(map[string]any)
		turnID, _ := turn["id"].(string)
		if turnID == "" {
			return Result{}, fmt.Errorf("codex did not return a turn id")
		}
		runtime.mu.Lock()
		if command.CommandID != "" && runtime.completedTurns[turnID] == "" {
			runtime.turnCommands[turnID] = command.CommandID
		}
		status := runtime.completedTurns[turnID]
		if status == "" {
			status = "running"
			runtime.activeTurnID = turnID
			if len(runtime.pendingApprovals) > 0 {
				status = "waiting_approval"
			}
		}
		runtime.status = status
		runtime.mu.Unlock()
		return Result{SessionID: command.SessionID, TurnID: turnID, Status: status, Accepted: true}, nil
	case "session.interrupt":
		if command.TurnID == "" {
			return Result{}, fmt.Errorf("codex interrupt requires a turn id")
		}
		if _, err := runtime.request(ctx, "turn/interrupt", map[string]any{"threadId": command.SessionID, "turnId": command.TurnID}); err != nil {
			return Result{}, err
		}
		runtime.setStatus("idle")
		return Result{SessionID: command.SessionID, Status: "idle", Accepted: true}, nil
	case "session.approve":
		return runtime.approve(command)
	case "session.delete":
		runtime.mu.Lock()
		active := runtime.status == "running" || runtime.status == "waiting_approval"
		runtime.mu.Unlock()
		if active {
			return Result{}, fmt.Errorf("codex session is active")
		}
		if _, err := runtime.request(ctx, "thread/delete", map[string]any{"threadId": command.SessionID}); err != nil {
			return Result{}, err
		}
		runtime.setSession("", "idle")
		return Result{SessionID: command.SessionID, Status: "idle", Accepted: true}, nil
	default:
		return Result{}, fmt.Errorf("codex action is unsupported")
	}
}

func (runtime *Runtime) approve(command Command) (Result, error) {
	runtime.approvalMu.Lock()
	defer runtime.approvalMu.Unlock()
	if command.ApprovalID == "" || (command.Decision != "accept" && command.Decision != "decline") {
		return Result{}, fmt.Errorf("codex approval is invalid")
	}
	runtime.mu.Lock()
	nativeID, ok := runtime.pendingApprovals[command.ApprovalID]
	kind := runtime.approvalKinds[command.ApprovalID]
	runtime.mu.Unlock()
	if !ok {
		return Result{}, fmt.Errorf("codex approval is not pending for this session")
	}
	result := map[string]any{"decision": command.Decision}
	if kind == "item/permissions/requestApproval" {
		permissions := map[string]any{}
		if command.Decision == "accept" {
			if command.Permissions == nil {
				return Result{}, fmt.Errorf("permission approval requires explicit granted permissions")
			}
			permissions = command.Permissions
		}
		result = map[string]any{"permissions": permissions, "scope": "turn"}
	}
	if err := runtime.send(map[string]any{"id": nativeID, "result": result}); err != nil {
		return Result{}, err
	}
	runtime.mu.Lock()
	delete(runtime.pendingApprovals, command.ApprovalID)
	delete(runtime.approvalKinds, command.ApprovalID)
	runtime.mu.Unlock()
	// Native serverRequest/resolved confirms the state transition.
	return Result{SessionID: command.SessionID, Status: "waiting_approval", Accepted: true}, nil
}

func (runtime *Runtime) workspace(raw string) (string, error) {
	if raw == "" {
		raw = runtime.config.Workspace
	}
	if runtime.sshStarter != nil {
		if raw == "" || strings.ContainsRune(raw, 0) || !strings.HasPrefix(raw, "/") {
			return "", fmt.Errorf("remote codex workspace must be an absolute POSIX path")
		}
		clean := path.Clean(raw)
		if !runtime.allowedRemote(clean) {
			return "", fmt.Errorf("codex workspace is outside the allowlist: %s not in %v", clean, runtime.config.Allowed)
		}
		return clean, nil
	}
	workspace, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("codex workspace is invalid")
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", err
	}
	if !runtime.allowed(workspace) {
		return "", fmt.Errorf("codex workspace is outside the allowlist")
	}
	return workspace, nil
}

func (runtime *Runtime) allowedRemote(dir string) bool {
	if len(runtime.config.Allowed) == 0 {
		return true
	}
	for _, root := range runtime.config.Allowed {
		cleanRoot := path.Clean(root)
		if cleanRoot == "/" {
			return true
		}
		if dir == cleanRoot || strings.HasPrefix(dir, cleanRoot+"/") {
			return true
		}
	}
	return false
}

func (runtime *Runtime) requireSession(sessionID string) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if sessionID == "" || sessionID != runtime.sessionID {
		return fmt.Errorf("codex session is not owned by this runtime")
	}
	return nil
}

func (runtime *Runtime) setSession(sessionID, status string) {
	runtime.mu.Lock()
	runtime.sessionID = sessionID
	if runtime.closed || runtime.transportFailed {
		runtime.status = "error"
	} else if len(runtime.pendingApprovals) > 0 || len(runtime.pendingHooks) > 0 {
		runtime.status = "waiting_approval"
	} else if runtime.activeTurnID != "" {
		runtime.status = "running"
	} else {
		runtime.status = status
	}
	runtime.mu.Unlock()
}

func (runtime *Runtime) Status() string {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.status
}

func (runtime *Runtime) setStatus(status string) {
	runtime.mu.Lock()
	runtime.status = status
	runtime.mu.Unlock()
}

func (runtime *Runtime) allowed(workspace string) bool {
	for _, root := range runtime.config.Allowed {
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

func (runtime *Runtime) request(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	runtime.mu.Lock()
	if runtime.closed || runtime.transportFailed || runtime.stdin == nil {
		runtime.mu.Unlock()
		return nil, fmt.Errorf("codex runtime is closed")
	}
	id := runtime.nextID
	runtime.nextID++
	waiter := pendingRequest{response: make(chan map[string]any, 1), err: make(chan error, 1)}
	runtime.pending[id] = waiter
	runtime.mu.Unlock()
	err := runtime.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		runtime.removePending(id)
		return nil, err
	}
	select {
	case response := <-waiter.response:
		return response, nil
	case err := <-waiter.err:
		return nil, err
	case <-ctx.Done():
		runtime.removePending(id)
		return nil, ctx.Err()
	}
}

func (runtime *Runtime) removePending(id int) {
	runtime.mu.Lock()
	delete(runtime.pending, id)
	runtime.mu.Unlock()
}

func (runtime *Runtime) readLoop(stdout io.Reader) {
	decoder := json.NewDecoder(stdout)
	decoder.UseNumber()
	for {
		var frame map[string]any
		if err := decoder.Decode(&frame); err != nil {
			runtime.mu.Lock()
			runtime.transportFailed = true
			if !runtime.closed {
				runtime.status = "error"
			}
			runtime.mu.Unlock()
			runtime.failPending(err)
			return
		}
		if method, ok := frame["method"].(string); ok {
			runtime.handleNotification(method, frame)
			continue
		}
		idValue, ok := frame["id"].(json.Number)
		if !ok {
			continue
		}
		parsed, err := idValue.Int64()
		if err != nil || parsed <= 0 || int64(int(parsed)) != parsed {
			continue
		}
		id := int(parsed)
		runtime.mu.Lock()
		waiter := runtime.pending[id]
		delete(runtime.pending, id)
		runtime.mu.Unlock()
		if waiter.response == nil {
			continue
		}
		if rpcError, ok := frame["error"]; ok {
			waiter.err <- fmt.Errorf("codex RPC error: %v", rpcError)
			continue
		}
		if raw, exists := frame["result"]; exists {
			result, ok := raw.(map[string]any)
			if !ok {
				result = map[string]any{"value": raw}
			}
			waiter.response <- result
			continue
		}
		waiter.err <- fmt.Errorf("codex rejected request")
	}
}

func (runtime *Runtime) handleNotification(method string, frame map[string]any) {
	runtime.mu.Lock()
	sessionID := runtime.sessionID
	if params, ok := frame["params"].(map[string]any); ok {
		if candidate, ok := params["threadId"].(string); ok && candidate != "" {
			if runtime.sessionID != "" && candidate != runtime.sessionID {
				runtime.mu.Unlock()
				return
			}
			sessionID = candidate
		}
	}
	approvalID := ""
	if _, needsApproval := approvalMethods[method]; needsApproval {
		if nativeID, ok := frame["id"]; ok {
			approvalID = "codex:" + nativeRequestID(nativeID)
			runtime.pendingApprovals[approvalID] = nativeID
			runtime.approvalKinds[approvalID] = method
			runtime.status = "waiting_approval"
		}
	}
	params, _ := frame["params"].(map[string]any)
	turn, _ := params["turn"].(map[string]any)
	turnID, _ := turn["id"].(string)
	if turnID == "" {
		turnID, _ = params["turnId"].(string)
	}
	commandID := runtime.turnCommands[turnID]
	if commandID == "" && turnID != "" && runtime.pendingCommandID != "" {
		commandID = runtime.pendingCommandID
		runtime.turnCommands[turnID] = commandID
	}
	switch method {
	case "turn/started":
		runtime.activeTurnID, runtime.status = turnID, "running"
	case "hook/started", "hook/completed":
		run, _ := params["run"].(map[string]any)
		id, _ := run["id"].(string)
		if run["eventName"] == "permissionRequest" && id != "" {
			if runtime.pendingHooks == nil {
				runtime.pendingHooks = map[string]bool{}
			}
			if method == "hook/started" {
				runtime.pendingHooks[id] = true
			} else {
				delete(runtime.pendingHooks, id)
			}
			if len(runtime.pendingHooks) > 0 || len(runtime.pendingApprovals) > 0 {
				runtime.status = "waiting_approval"
			} else if runtime.activeTurnID != "" {
				runtime.status = "running"
			}
		}
	case "turn/completed":
		runtime.pendingHooks = nil
		runtime.activeTurnID, runtime.status = "", "idle"
		if turn["status"] == "failed" {
			runtime.status = "error"
		}
		if len(runtime.completedTurns) >= 32 {
			runtime.completedTurns = map[string]string{}
		}
		runtime.completedTurns[turnID] = runtime.status
		delete(runtime.turnCommands, turnID)
	case "serverRequest/resolved":
		if id, ok := params["requestId"]; ok {
			delete(runtime.pendingApprovals, "codex:"+nativeRequestID(id))
			delete(runtime.approvalKinds, "codex:"+nativeRequestID(id))
		}
		if len(runtime.pendingApprovals) == 0 && len(runtime.pendingHooks) == 0 {
			runtime.status = "idle"
			if runtime.activeTurnID != "" {
				runtime.status = "running"
			}
		}
	}
	status, callback := runtime.status, runtime.config.OnNotification
	runtime.mu.Unlock()
	if id, isRequest := frame["id"]; isRequest && approvalID == "" {
		_ = runtime.send(map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "server request is not implemented by this runtime"}})
	}
	if callback != nil {
		notification := Notification{CommandID: commandID, SessionID: sessionID, Method: method, ApprovalID: approvalID, Status: status, Frame: frame}
		select {
		case runtime.notifications <- notification:
		case <-runtime.done:
		default:
			runtime.failPending(fmt.Errorf("codex notification overflow; resync required"))
			go runtime.Close()
		}
	}
}

func nativeRequestID(value any) string {
	if number, ok := value.(float64); ok && number == float64(int64(number)) {
		return fmt.Sprintf("%d", int64(number))
	}
	return fmt.Sprint(value)
}

func threadID(response map[string]any) string {
	thread, _ := response["thread"].(map[string]any)
	if thread == nil {
		if nested, ok := response["result"].(map[string]any); ok {
			thread, _ = nested["thread"].(map[string]any)
		}
	}
	sessionID, _ := thread["id"].(string)
	return sessionID
}

func (runtime *Runtime) failPending(err error) {
	runtime.mu.Lock()
	pending := runtime.pending
	runtime.pending = map[int]pendingRequest{}
	runtime.mu.Unlock()
	for _, waiter := range pending {
		waiter.err <- err
	}
}

func (runtime *Runtime) notify(method string, params map[string]any) error {
	return runtime.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (runtime *Runtime) send(frame map[string]any) error {
	runtime.writeMu.Lock()
	defer runtime.writeMu.Unlock()
	runtime.mu.Lock()
	if runtime.closed || runtime.transportFailed || runtime.stdin == nil {
		runtime.mu.Unlock()
		return fmt.Errorf("codex runtime is closed")
	}
	stdin := runtime.stdin
	runtime.mu.Unlock()
	return json.NewEncoder(stdin).Encode(frame)
}

func (runtime *Runtime) Close() {
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return
	}
	runtime.closed = true
	close(runtime.done)
	command, sshSess, stdin := runtime.command, runtime.sshSession, runtime.stdin
	runtime.command, runtime.sshSession, runtime.stdin = nil, nil, nil
	runtime.mu.Unlock()
	runtime.failPending(fmt.Errorf("codex runtime is closed"))
	if stdin != nil {
		_ = stdin.Close()
	}
	if sshSess != nil {
		_ = sshSess.Close()
	}
	if command != nil && command.Process != nil {
		exited := make(chan struct{})
		go func() { _ = command.Wait(); close(exited) }()
		select {
		case <-exited:
		case <-time.After(time.Second):
			_ = command.Process.Kill()
			<-exited
		}
	}
}
