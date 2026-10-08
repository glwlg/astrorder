package hermes

import (
	nativeprocess "astrorder.dev/session-daemon/internal/process"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  map[string]any  `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErrorObject `json:"error,omitempty"`
}

type rpcErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcErrorObject) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

type pendingRequest struct {
	ch chan *rpcMessage
}

type approvalRecord struct {
	Resolving bool
	NativeID  any
	SessionID string
	Method    string
	Params    map[string]any
}

func (a *Adapter) startProcess(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	if a.closed || a.failed {
		a.mu.Unlock()
		return fmt.Errorf("hermes adapter is closed")
	}
	if a.cmd != nil && a.cmd.Process != nil {
		ready, done := a.readyCh, a.processDone
		a.mu.Unlock()
		return waitGatewayReady(ctx, ready, done)
	}
	if a.sshSession != nil {
		ready, done := a.readyCh, a.processDone
		a.mu.Unlock()
		return waitGatewayReady(ctx, ready, done)
	}

	if a.sshStarter != nil {
		spec := ssh.CommandSpec{
			Executable: a.config.Executable,
			Args:       a.config.Arguments,
			Dir:        a.config.Workspace,
		}
		// Close retires the owned transport independently of this request.
		sess, err := a.sshStarter(context.WithoutCancel(ctx), spec)
		if err != nil {
			a.mu.Unlock()
			return fmt.Errorf("failed to start remote hermes over ssh: %w", err)
		}
		a.sshSession = sess
		a.stdin = sess.Stdin()
		a.stdout = sess.Stdout()
		a.readyCh = make(chan struct{})
		a.processDone = make(chan struct{})
		a.mu.Unlock()

		go a.readStdout(sess.Stdout())
		go func() {
			_, _ = sess.Wait()
			a.mu.Lock()
			select {
			case <-a.processDone:
			default:
				close(a.processDone)
			}
			a.failed = true
			a.mu.Unlock()
		}()

		return waitGatewayReady(ctx, a.readyCh, a.processDone)
	}

	if a.config.Executable == "" {
		a.mu.Unlock()
		return fmt.Errorf("hermes executable is not configured")
	}

	cmd := exec.Command(a.config.Executable, a.config.Arguments...)
	if a.config.Workspace != "" {
		cmd.Dir = a.config.Workspace
	}
	cmd.Env = nativeprocess.NativeEnvironment(a.config.Environment)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("stdin pipe failed: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		a.mu.Unlock()
		return fmt.Errorf("stdout pipe failed: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		a.mu.Unlock()
		return fmt.Errorf("stderr pipe failed: %w", err)
	}

	owner, err := nativeprocess.StartOwnedCommand(cmd)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		a.mu.Unlock()
		return fmt.Errorf("failed to start hermes process: %w", err)
	}

	a.cmd = cmd
	a.owner = owner
	a.stdin = stdin
	a.stdout = stdout
	a.stderr = stderr
	a.readyCh = make(chan struct{})
	a.processDone = make(chan struct{})
	a.mu.Unlock()

	go a.drainStderr(stderr)
	go a.readStdout(stdout)
	go a.waitProcess(cmd)

	return waitGatewayReady(ctx, a.readyCh, a.processDone)
}

func waitGatewayReady(ctx context.Context, ready, done <-chan struct{}) error {
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return fmt.Errorf("hermes transport exited before readiness")
	case <-ready:
		select {
		case <-done:
			return fmt.Errorf("hermes transport is unavailable")
		default:
			return nil
		}
	case <-timer.C:
		return fmt.Errorf("hermes gateway readiness timed out")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Adapter) drainStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		a.mu.Lock()
		if len(line) > 300 {
			line = line[len(line)-300:]
		}
		a.stderrBuf = line
		a.mu.Unlock()
	}
}

func (a *Adapter) lastStderr() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.stderrBuf
}

func (a *Adapter) waitProcess(cmd *exec.Cmd) {
	a.mu.RLock()
	owner := a.owner
	a.mu.RUnlock()
	var cleanupErr error
	if owner != nil {
		cleanupErr = owner.WaitAndClose(cmd)
	} else {
		_ = cmd.Wait()
	}
	a.mu.Lock()
	a.cleanupErr = cleanupErr
	close(a.processDone)
	for id, req := range a.pending {
		close(req.ch)
		delete(a.pending, id)
	}
	for _, s := range a.sessions {
		if s.Status == "running" || s.Status == "waiting_approval" {
			s.Status = "error"
		}
	}
	a.mu.Unlock()
}

func (a *Adapter) readStdout(r io.Reader) {
	defer a.failTransport(fmt.Errorf("native Hermes stdout closed"))
	a.eventOnce.Do(func() { go a.dispatchEvents() })
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}

		var msg rpcMessage
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&msg); err != nil {
			continue
		}

		// 1. Response frame with matching ID
		if msg.ID != nil && msg.Method == "" {
			idStr := fmt.Sprint(msg.ID)
			a.mu.Lock()
			waiter, ok := a.pending[idStr]
			if ok {
				delete(a.pending, idStr)
			}
			a.mu.Unlock()
			if ok {
				waiter.ch <- &msg
				continue
			}
		}

		// 2. Event notification: method == "event"
		if msg.Method == "event" && msg.Params != nil {
			if !a.queueEvent(&msg) {
				return
			}
			continue
		}

		// 3. Server-initiated request (e.g. approval, clarify)
		if msg.Method != "" && msg.ID != nil {
			if !a.queueEvent(&msg) {
				return
			}
			continue
		}
	}
}

func (a *Adapter) handleGatewayEvent(params map[string]any) {
	a.mu.RLock()
	unavailable := a.closed || a.failed
	a.mu.RUnlock()
	if unavailable {
		return
	}
	eventType, _ := params["type"].(string)
	if eventType != "gateway.ready" {
		sid, _ := params["session_id"].(string)
		a.mu.RLock()
		id := a.handleToSession[sid]
		if id == "" {
			id = sid
		}
		owned := a.sessions[id] != nil
		a.mu.RUnlock()
		if !owned {
			return
		}
	}
	if strings.HasPrefix(eventType, "hermes.") && eventType != "hermes.compaction" {
		sid, _ := params["session_id"].(string)
		a.mu.RLock()
		id := a.handleToSession[sid]
		if id == "" {
			id = sid
		}
		s := a.sessions[id]
		if s == nil || a.closed || a.failed {
			a.mu.RUnlock()
			return
		}
		status := s.Status
		a.mu.RUnlock()
		if a.config.Emit != nil {
			payload := map[string]any{"agent_id": a.config.AgentID, "session_id": id, "frame": params}
			if value, ok := params["payload"].(map[string]any); ok {
				for k, v := range value {
					payload[k] = v
				}
			}
			if err := a.config.Emit(id, eventType, payload, status); err != nil {
				a.failCloseSession(id, err)
			}
		}
		return
	}

	switch eventType {
	case "gateway.ready":
		a.mu.Lock()
		select {
		case <-a.readyCh:
		default:
			close(a.readyCh)
		}
		a.mu.Unlock()

	case "message.complete":
		sid, _ := params["session_id"].(string)
		a.mu.Lock()
		durableID := a.handleToSession[sid]
		if durableID == "" {
			durableID = sid
		}
		session := a.sessions[durableID]
		if session == nil || a.closed || a.failed {
			a.mu.Unlock()
			return
		}
		if session != nil {
			session.Status = "idle"
		}
		emit := a.config.Emit
		agentID := a.config.AgentID
		a.mu.Unlock()

		if emit != nil && durableID != "" {
			payload := map[string]any{
				"agent_id":   agentID,
				"session_id": durableID,
			}
			if p, ok := params["payload"].(map[string]any); ok {
				for k, v := range p {
					payload[k] = v
				}
			}
			if err := emit(durableID, "hermes.command_complete", payload, "idle"); err != nil {
				a.failCloseSession(durableID, err)
			}
		}

	case "hermes.compaction":
		sid, _ := params["session_id"].(string)
		a.mu.Lock()
		durableID := a.handleToSession[sid]
		if durableID == "" {
			durableID = sid
		}
		emit := a.config.Emit
		agentID := a.config.AgentID
		a.mu.Unlock()

		if emit != nil && durableID != "" {
			payload := map[string]any{
				"agent_id":   agentID,
				"session_id": durableID,
			}
			if p, ok := params["payload"].(map[string]any); ok {
				for k, v := range p {
					payload[k] = v
				}
			}
			if err := emit(durableID, "hermes.compaction", payload, "running"); err != nil {
				a.failCloseSession(durableID, err)
			}
		}
	}
}

func (a *Adapter) handleServerRequest(msg *rpcMessage) {
	method := msg.Method
	params := msg.Params
	nativeID := msg.ID

	if method == "approval" {
		sid, _ := params["session_id"].(string)
		a.mu.Lock()
		durableID := a.handleToSession[sid]
		if durableID == "" {
			durableID = sid
		}
		approvalKey := fmt.Sprint(nativeID)
		if a.sessions[durableID] == nil || a.closed || a.failed {
			a.mu.Unlock()
			return
		}
		if reqID, ok := params["request_id"].(string); ok && reqID != "" {
			approvalKey = reqID
		}

		a.pendingApprovals[approvalKey] = &approvalRecord{
			NativeID:  nativeID,
			SessionID: durableID,
			Method:    method,
			Params:    params,
		}

		if session := a.sessions[durableID]; session != nil {
			session.Status = "waiting_approval"
		}
		emit := a.config.Emit
		a.mu.Unlock()

		if emit != nil && durableID != "" {
			err := emit(durableID, "hermes.approval_request", map[string]any{
				"approval_id": approvalKey,
				"native_id":   nativeID,
				"method":      method,
				"params":      params,
			}, "waiting_approval")
			if err != nil {
				a.failCloseSession(durableID, err)
			}
		}
	} else {
		_ = a.sendRaw(map[string]any{
			"jsonrpc": "2.0",
			"id":      nativeID,
			"error": map[string]any{
				"code":    -32601,
				"message": "server request is not implemented",
			},
		})
	}
}

func (a *Adapter) failCloseSession(sessionID string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[sessionID]; ok {
		s.Status = "error"
		s.Metadata["last_error"] = err.Error()
	}
}

func (a *Adapter) sendRaw(frame map[string]any) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	a.mu.RLock()
	stdin := a.stdin
	closed := a.closed || a.failed
	a.mu.RUnlock()

	if closed || stdin == nil {
		return fmt.Errorf("hermes runtime is closed")
	}

	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = stdin.Write(data)
	return err
}

var nextReqID atomic.Int64

func (a *Adapter) rpc(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	idNum := nextReqID.Add(1)
	idStr := fmt.Sprintf("req-%d", idNum)

	ch := make(chan *rpcMessage, 1)
	a.mu.Lock()
	if a.closed || a.failed {
		a.mu.Unlock()
		return nil, fmt.Errorf("hermes runtime is closed")
	}
	a.pending[idStr] = pendingRequest{ch: ch}
	a.mu.Unlock()

	frame := map[string]any{
		"jsonrpc": "2.0",
		"id":      idStr,
		"method":  method,
		"params":  params,
	}

	if err := a.sendRaw(frame); err != nil {
		a.mu.Lock()
		delete(a.pending, idStr)
		a.mu.Unlock()
		return nil, err
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return nil, fmt.Errorf("hermes process connection closed")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		if resMap, ok := resp.Result.(map[string]any); ok {
			return resMap, nil
		}
		return map[string]any{"result": resp.Result}, nil

	case <-ctx.Done():
		a.mu.Lock()
		delete(a.pending, idStr)
		a.mu.Unlock()
		return nil, ctx.Err()

	case <-a.processDone:
		a.mu.Lock()
		delete(a.pending, idStr)
		a.mu.Unlock()
		return nil, fmt.Errorf("hermes process terminated: %s", a.lastStderr())
	}
}
