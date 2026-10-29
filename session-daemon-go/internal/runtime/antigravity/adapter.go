// Package antigravity hosts the native agy stream-json transport. Conversation
// bodies remain in Antigravity's own SQLite store; only live events are emitted.
package antigravity

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
)

type Config struct {
	Executable             string
	Arguments, Environment []string
	Workspace              string
	Allowed                []string
	AgentID                string
	Home                   string
	Emit                   func(string, string, map[string]any, string) error
}
type Adapter struct {
	mu       sync.Mutex
	config   Config
	sessions map[string]*session
	opening  map[string]bool
	closed   bool
	catalog  map[string]string
}
type session struct {
	mu                                       sync.Mutex
	id, cwd, status, model, effort, command  string
	permission, requestedPermission          string
	nativeURL, nativeToken                   string
	control, closed, readonly, deletePending bool
	cmd                                      *exec.Cmd
	owner                                    *process.CommandOwner
	input                                    io.WriteCloser
	done                                     chan struct{}
	texts                                    map[int]string
}

func New(c Config) *Adapter {
	if c.AgentID == "" {
		c.AgentID = "local-antigravity"
	}
	if c.Home == "" {
		c.Home, _ = os.UserHomeDir()
	}
	return &Adapter{config: c, sessions: map[string]*session{}, opening: map[string]bool{}, catalog: map[string]string{}}
}
func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func options(r core.Request) map[string]any {
	p, _ := r.Fields["params"].(map[string]any)
	if p == nil {
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
func (a *Adapter) workspace(raw string) (string, error) {
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
func (a *Adapter) result(s *session) core.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "owned"
	if s.readonly {
		state = "readonly"
	}
	return core.Result{SessionID: s.id, Status: s.status, Payload: map[string]any{"session_id": s.id, "source_session_id": s.id, "agent_id": a.config.AgentID, "source_id": a.config.AgentID, "runtime_id": a.config.AgentID, "name": "Antigravity", "runtime_control": s.control, "control_state": state, "workspace": s.cwd, "model": s.model, "effort": s.effort}}
}
func (a *Adapter) emit(id, kind string, p map[string]any, status string) error {
	if a.config.Emit == nil {
		return nil
	}
	return a.config.Emit(id, kind, p, status)
}
func (a *Adapter) event(s *session, kind string, data map[string]any, status string) error {
	return a.emit(s.id, "connector.event", map[string]any{"agent_id": a.config.AgentID, "session_id": s.id, "type": kind, "data": data}, status)
}
func (a *Adapter) announce(s *session) error {
	if s.control {
		return nil
	}
	s.mu.Lock()
	model, effort := s.model, s.effort
	s.mu.Unlock()
	return a.event(s, "session.upsert", map[string]any{"history_state": "available", "native_kind": "rpc", "model": model, "effort": effort, "provider": "antigravity", "control_state": "owned", "id": s.id, "agent_id": a.config.AgentID, "source_id": a.config.AgentID, "source_session_id": s.id, "workspace": s.cwd, "status": a.result(s).Status, "created_at": time.Now().UTC().Format(time.RFC3339Nano)}, "")
}
func (a *Adapter) Create(ctx context.Context, r core.Request) (core.Result, error) {
	p := options(r)
	if str(p, "parent_session_id") != "" {
		return a.fork(ctx, p)
	}
	return a.open(ctx, "", p)
}
func (a *Adapter) Spawn(ctx context.Context, r core.Request) (core.Result, error) {
	p := options(r)
	if rc, _ := p["runtime_control"].(bool); rc {
		if r.SessionID == "" {
			return core.Result{}, fmt.Errorf("control identity required")
		}
		if _, err := a.models(ctx); err != nil {
			return core.Result{}, err
		}
		s := &session{id: r.SessionID, cwd: a.config.Workspace, status: "idle", control: true}
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
	if _, err := os.Stat(filepath.Join(a.historyDir(), r.SessionID+".db")); os.IsNotExist(err) {
		dir := filepath.Join(a.config.Home, ".gemini", "antigravity", "conversations")
		if _, err := os.Stat(filepath.Join(dir, r.SessionID+".db")); err == nil {
			s := &session{id: r.SessionID, cwd: a.config.Workspace, status: "idle", readonly: true}
			a.mu.Lock()
			if a.closed || a.sessions[s.id] != nil {
				a.mu.Unlock()
				return core.Result{}, fmt.Errorf("Antigravity attach unavailable")
			}
			a.sessions[s.id] = s
			a.mu.Unlock()
			return a.result(s), nil
		}
	}
	a.mu.Lock()
	previous := a.sessions[r.SessionID]
	a.mu.Unlock()
	if previous != nil && !previous.transportExited() {
		return core.Result{}, fmt.Errorf("Antigravity transport has not exited; recovery is unsafe")
	}
	if err := a.validateResumeState(ctx, r.SessionID, previous != nil); err != nil {
		return core.Result{}, err
	}
	return a.openReplacing(ctx, r.SessionID, p, previous)
}

// done closes only after the reader has reaped the native process. A native
// turn error alone does not authorize replacing a transport that is still live.
func (s *session) transportExited() bool {
	if s.control || s.readonly || s.done == nil {
		return false
	}
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (a *Adapter) open(ctx context.Context, id string, p map[string]any) (core.Result, error) {
	return a.openReplacing(ctx, id, p, nil)
}
func (a *Adapter) openReplacing(ctx context.Context, id string, p map[string]any, previous *session) (core.Result, error) {
	cwd, err := a.workspace(str(p, "cwd"))
	if err != nil {
		return core.Result{}, err
	}
	model, effort := str(p, "model"), str(p, "effort")
	a.mu.Lock()
	if a.closed || a.opening[id] {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("Antigravity attach unavailable")
	}
	if old := a.sessions[id]; old != previous {
		a.mu.Unlock()
		return core.Result{}, fmt.Errorf("Antigravity conversation already attached")
	}
	a.opening[id] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.opening, id); a.mu.Unlock() }()
	args := append([]string{}, a.config.Arguments...)
	if permission := str(p, "permission_mode"); permission != "" {
		filtered := args[:0]
		for _, arg := range args {
			if arg != "--dangerously-skip-permissions" && !strings.HasPrefix(arg, "--dangerously-skip-permissions=") {
				filtered = append(filtered, arg)
			}
		}
		args = filtered
		if permission == "always-proceed" {
			args = append(args, "--dangerously-skip-permissions")
		}
	}
	nativeToken := fmt.Sprintf("%x", rand.Text())
	args = append(args, "--csrf_token="+nativeToken)
	args = append(args, "--input-format", "stream-json", "--output-format", "stream-json")
	if id != "" {
		args = append(args, "--conversation", id)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	cmd := exec.Command(a.config.Executable, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), a.config.Environment...)
	input, err := cmd.StdinPipe()
	if err != nil {
		return core.Result{}, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return core.Result{}, err
	}
	// Drain diagnostics without persisting raw output or exposing local auth data.
	cmd.Stderr = io.Discard
	owner, err := process.StartOwnedCommand(cmd)
	if err != nil {
		input.Close()
		return core.Result{}, err
	}
	s := &session{id: id, cwd: cwd, status: "idle", model: model, effort: effort, requestedPermission: str(p, "permission_mode"), nativeToken: nativeToken, cmd: cmd, owner: owner, input: input, done: make(chan struct{}), texts: map[int]string{}}
	ready := make(chan error, 1)
	go a.read(s, out, ready)
	select {
	case err = <-ready:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		_ = owner.Close()
		<-s.done
		return core.Result{}, err
	}
	a.mu.Lock()
	if a.closed || a.sessions[s.id] != previous {
		a.mu.Unlock()
		_ = owner.Close()
		<-s.done
		return core.Result{}, fmt.Errorf("Antigravity identity already owned or adapter closed")
	}
	a.sessions[s.id] = s
	a.mu.Unlock()
	if err = a.announce(s); err != nil {
		_ = owner.Close()
		<-s.done
		return core.Result{}, err
	}
	return a.result(s), nil
}
func (a *Adapter) read(s *session, out io.Reader, ready chan<- error) {
	defer close(s.done)
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 65536), 16_000_000)
	initialized := false
	for scan.Scan() {
		var frame map[string]any
		if err := json.Unmarshal(scan.Bytes(), &frame); err != nil {
			if !initialized {
				ready <- fmt.Errorf("invalid Antigravity init frame")
			}
			a.fail(s, "invalid Antigravity stream JSON")
			_ = s.owner.Close()
			_ = s.cmd.Wait()
			return
		}
		event := str(frame, "event")
		if !initialized {
			id := str(frame, "conversation_id")
			if event != "init" || !validID(id) || (s.id != "" && s.id != id) {
				ready <- fmt.Errorf("Antigravity did not confirm native conversation identity")
				_ = s.owner.Close()
				_ = s.cmd.Wait()
				return
			}
			s.mu.Lock()
			init, _ := frame["init"].(map[string]any)
			confirmed := str(init, "model")
			if s.model != "" && confirmed != s.model {
				s.mu.Unlock()
				ready <- fmt.Errorf("Antigravity did not confirm requested model")
				_ = s.owner.Close()
				_ = s.cmd.Wait()
				return
			}
			s.model = confirmed
			s.permission = str(init, "permission_mode")
			if requested := s.requestedPermission; requested != "" && s.permission != requested {
				s.mu.Unlock()
				ready <- fmt.Errorf("Antigravity did not confirm requested permission mode")
				_ = s.owner.Close()
				_ = s.cmd.Wait()
				return
			}
			s.id = id
			initialized = true
			s.mu.Unlock()
			ready <- nil
			continue
		}
		var err error
		switch event {
		case "step_update":
			p, _ := frame[event].(map[string]any)
			if str(p, "conversation_id") != s.id {
				err = fmt.Errorf("Antigravity stream identity mismatch")
			} else {
				err = a.step(s, p)
			}
		case "result":
			p, _ := frame[event].(map[string]any)
			if str(p, "conversation_id") != s.id {
				err = fmt.Errorf("Antigravity result identity mismatch")
			} else {
				err = a.finish(s, p)
			}
		default:
			err = fmt.Errorf("unsupported Antigravity stream event %q", event)
		}
		if err != nil {
			a.fail(s, err.Error())
			_ = s.owner.Close()
			break
		}
	}
	waitErr := s.cmd.Wait()
	if !initialized {
		ready <- fmt.Errorf("Antigravity exited before initialization: %v", waitErr)
		return
	}
	s.mu.Lock()
	closed, busy := s.closed, s.status == "running"
	s.mu.Unlock()
	if !closed {
		if busy || waitErr != nil || scan.Err() != nil {
			a.fail(s, "Antigravity transport ended without a confirmed idle session")
		} else {
			a.fail(s, "Antigravity transport closed")
		}
	}
}
func (a *Adapter) step(s *session, p map[string]any) error {
	n, ok := p["step_index"].(float64)
	if !ok || n < 0 || n != float64(int(n)) {
		return fmt.Errorf("invalid Antigravity step index")
	}
	idx := int(n)
	kind := str(p, "step_type")
	state := str(p, "state")
	switch state {
	case "ACTIVE", "DONE", "ERROR", "INVALID", "CANCELED", "INTERRUPTED", "HALTED", "CLEARED", "WAITING", "PENDING", "QUEUED", "RUNNING", "GENERATING":
	default:
		return fmt.Errorf("invalid Antigravity step state %q", state)
	}
	item := map[string]any{"id": itemID(s.id, idx, ""), "role": "assistant", "kind": "message", "attachments": []any{}}
	switch kind {
	case "agent_response":
		s.mu.Lock()
		s.texts[idx] += str(p, "text_delta")
		item["text"] = s.texts[idx]
		s.mu.Unlock()
	case "tool":
		info, _ := p["tool_info"].(map[string]any)
		tool := map[string]any{"id": item["id"], "name": str(p, "tool_name"), "status": "running"}
		if state == "DONE" {
			tool["status"] = "completed"
		}
		for src, dst := range map[string]string{"name": "name", "parameters": "input", "output": "output", "error": "error"} {
			if info[src] != nil {
				tool[dst] = info[src]
			}
		}
		if nativeError, ok := tool["error"].(map[string]any); ok {
			tool["error"] = str(nativeError, "message")
		}
		if state == "ERROR" || state == "INVALID" || state == "CANCELED" || state == "INTERRUPTED" || state == "HALTED" || info["error"] != nil {
			tool["status"] = "error"
		}
		item["role"] = "tool"
		item["kind"] = "tool"
		item["tool"] = tool
		item["text"] = ""
	case "user_input":
		items, _, err := readHistory(context.Background(), a.historyDir(), s.id, fmt.Sprint(idx+1), 1)
		if err != nil {
			return err
		}
		if len(items) > 0 && items[0]["role"] == "user" && items[0]["id"] == itemID(s.id, idx, "") {
			return a.event(s, "message.upsert", items[0], "")
		}
		return nil
	case "checkpoint":
		return nil
	default:
		return nil
	}
	return a.event(s, "message.upsert", item, "")
}
func (a *Adapter) finish(s *session, p map[string]any) error {
	terminal := str(p, "status")
	if terminal != "SUCCESS" && terminal != "CANCELED" && terminal != "INTERRUPTED" && terminal != "ERROR" && terminal != "INVALID" {
		return fmt.Errorf("Antigravity returned nonterminal status %q", terminal)
	}
	s.mu.Lock()
	if s.status != "running" {
		s.mu.Unlock()
		return fmt.Errorf("unexpected Antigravity result without active turn")
	}
	command := s.command
	s.command = ""
	s.status = "idle"
	if terminal == "ERROR" || terminal == "INVALID" {
		s.status = "error"
	}
	status := s.status
	s.mu.Unlock()
	state := "completed"
	if terminal != "SUCCESS" {
		state = "failed"
	}
	if command != "" {
		if err := a.event(s, "command.upsert", map[string]any{"id": command, "state": state, "error": p["error"]}, ""); err != nil {
			return err
		}
	}
	if err := a.emitUsage(s, p); err != nil {
		return err
	}
	return a.emit(s.id, "runtime.owner", map[string]any{"agent_id": a.config.AgentID, "session_id": s.id, "owner_pid": s.cmd.Process.Pid, "runtime_owned": true, "status": status}, status)
}
func (a *Adapter) fail(s *session, reason string) {
	s.mu.Lock()
	s.status = "error"
	command := s.command
	s.command = ""
	id := s.id
	s.mu.Unlock()
	if id == "" {
		return
	}
	if command != "" {
		_ = a.event(s, "command.upsert", map[string]any{"id": command, "state": "unknown", "error": reason}, "")
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = a.emit(id, "runtime.owner", map[string]any{"agent_id": a.config.AgentID, "session_id": id, "owner_pid": s.cmd.Process.Pid, "runtime_owned": true, "status": "error", "error": reason}, "error")
	}
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
		return a.interrupt(ctx, s)
	case "session.models":
		items, err := a.models(ctx)
		result.Payload["items"] = items
		return result, err
	case "session.model.read":
		if s.transportExited() {
			return core.Result{}, core.ErrSessionNotOwned
		}
		result.Payload["provider"] = "antigravity"
		result.Payload["model_switch_supported"] = !s.control && !s.readonly
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
	case "session.history_page":
		if s.control {
			return core.Result{}, fmt.Errorf("control binding has no history")
		}
		limit := 50
		if v, ok := r.Fields["limit"].(int); ok {
			limit = v
		}
		dir := a.historyDir()
		if s.readonly {
			dir = filepath.Join(a.config.Home, ".gemini", "antigravity", "conversations")
		}
		items, next, err := readHistory(ctx, dir, s.id, str(r.Fields, "before"), limit)
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
		p := options(r)
		for _, key := range []string{"attachments", "input"} {
			if vals, ok := p[key].([]any); ok {
				for _, v := range vals {
					m, ok := v.(map[string]any)
					if key == "attachments" || !ok || str(m, "type") != "text" {
						return core.Result{}, core.RejectBeforeDispatch("Antigravity stream input supports text only")
					}
				}
			}
		}
		text := str(p, "text")
		if text == "" {
			return core.Result{}, core.RejectBeforeDispatch("Antigravity text input required")
		}
		for _, key := range []string{"model", "effort", "provider"} {
			if str(p, key) != "" {
				return core.Result{}, core.RejectBeforeDispatch(fmt.Sprintf("Antigravity cannot change %s during a streaming conversation", key))
			}
		}
		raw, _ := json.Marshal(map[string]any{"event": "user", "message": map[string]any{"content": text}})
		raw = append(raw, '\n')
		s.mu.Lock()
		if s.closed || s.status != "idle" {
			s.mu.Unlock()
			return core.Result{}, core.RejectBeforeDispatch("Antigravity conversation is not idle")
		}
		s.status = "running"
		s.command = str(p, "command_id")
		_, err := s.input.Write(raw)
		s.mu.Unlock()
		if err != nil {
			a.fail(s, "Antigravity input write failed; delivery is uncertain")
			return core.Result{}, err
		}
		result = a.result(s)
		result.Payload["accepted"] = true
		return result, nil
	case "session.close":
		s.mu.Lock()
		if s.status != "idle" {
			s.mu.Unlock()
			return core.Result{}, fmt.Errorf("Antigravity active or ambiguous session cannot close")
		}
		s.closed = true
		s.mu.Unlock()
		if s.owner != nil {
			if err := s.owner.Close(); err != nil {
				return core.Result{}, err
			}
			<-s.done
		}
		a.mu.Lock()
		delete(a.sessions, s.id)
		a.mu.Unlock()
		result.Payload["closed"] = true
		return result, nil
	default:
		return core.Result{}, core.RejectBeforeDispatch(fmt.Sprintf("Antigravity stream transport does not support %s", r.Action))
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
	sessions := a.sessions
	a.mu.Unlock()
	var errs error
	for _, s := range sessions {
		if s.owner != nil {
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
			errs = errors.Join(errs, s.owner.Close())
			<-s.done
		}
	}
	return errs
}
func (a *Adapter) Shutdown(ctx context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.opening) > 0 {
		return nil, fmt.Errorf("Antigravity attach in progress")
	}
	for _, s := range a.sessions {
		if a.result(s).Status != "idle" {
			return nil, fmt.Errorf("Antigravity active or ambiguous session cannot disconnect")
		}
	}
	ids := []string{}
	for id, s := range a.sessions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		if s.owner != nil {
			if err := s.owner.Close(); err != nil {
				return nil, err
			}
			<-s.done
		}
		delete(a.sessions, id)
		ids = append(ids, id)
	}
	return ids, nil
}
