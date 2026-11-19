package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"astrorder.dev/session-daemon/internal/process"
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

type acpRPCError struct {
	Code    int
	Message string
}

func (e *acpRPCError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("Antigravity ACP error (%d): %s", e.Code, e.Message)
	}
	return fmt.Sprintf("Antigravity ACP error (%d)", e.Code)
}

type acpCallResult struct {
	result map[string]any
	err    error
}

type acpClient struct {
	adapter *Adapter

	cmd   *exec.Cmd
	owner *process.CommandOwner
	ssh   SSHSession
	stdin io.WriteCloser
	done  chan struct{}

	writeMu sync.Mutex

	mu            sync.Mutex
	nextID        int64
	pending       map[string]chan acpCallResult
	closed        bool
	failedReason  string
	exitConfirmed bool
	exitErr       error

	promptImage    bool
	promptEmbedded bool
	hasResume      bool
	hasLoad        bool
	hasClose       bool
	hasList        bool
}

func cleanACPArgs(raw []string) []string {
	out := make([]string, 0, len(raw)+1)
	for i := 0; i < len(raw); i++ {
		arg := raw[i]
		switch {
		case arg == "--dangerously-skip-permissions",
			strings.HasPrefix(arg, "--dangerously-skip-permissions="),
			strings.HasPrefix(arg, "--input-format="),
			strings.HasPrefix(arg, "--output-format="),
			strings.HasPrefix(arg, "--csrf_token="),
			strings.HasPrefix(arg, "--conversation="),
			strings.HasPrefix(arg, "--model="),
			strings.HasPrefix(arg, "--effort="):
			continue
		case arg == "--input-format",
			arg == "--output-format",
			arg == "--csrf_token",
			arg == "--conversation",
			arg == "--model",
			arg == "--effort":
			i++
			continue
		default:
			out = append(out, arg)
		}
	}
	if len(out) == 0 || out[len(out)-1] != "acp" {
		out = append(out, "acp")
	}
	return out
}

func (a *Adapter) ensureClient(ctx context.Context) (*acpClient, error) {
	a.acpMu.Lock()
	defer a.acpMu.Unlock()

	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("Antigravity adapter closed")
	}

	if a.client != nil && !a.client.exited() {
		return a.client, nil
	}

	startDir := ""
	if a.sshStarter != nil {
		if ws, err := a.workspace(a.config.Workspace); err == nil {
			startDir = ws
		}
	} else if ws, err := a.workspace(""); err == nil {
		startDir = ws
	}

	c, err := a.startACPClient(ctx, startDir)
	if err != nil && a.sshStarter != nil && startDir != "" && startDir != "/" && ctx.Err() == nil {
		c, err = a.startACPClient(ctx, "")
	}
	if err != nil {
		return nil, err
	}
	a.client = c
	return c, nil
}

func (a *Adapter) startACPClient(ctx context.Context, dir string) (*acpClient, error) {
	args := cleanACPArgs(a.config.Arguments)
	c := &acpClient{
		adapter: a,
		done:    make(chan struct{}),
		pending: make(map[string]chan acpCallResult),
	}

	var stdout io.ReadCloser
	if a.sshStarter != nil {
		sshSess, err := a.sshStarter(context.WithoutCancel(ctx), ssh.CommandSpec{
			Executable: a.config.Executable,
			Args:       args,
			Dir:        dir,
		})
		if err != nil {
			return nil, err
		}
		c.ssh = sshSess
		c.stdin = sshSess.Stdin()
		stdout = sshSess.Stdout()
	} else {
		cmd := exec.Command(a.config.Executable, args...)
		if dir != "" {
			cmd.Dir = dir
		}
		cmd.Env = process.NativeEnvironment(a.config.Environment)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		outPipe, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		cmd.Stderr = io.Discard
		owner, err := process.StartOwnedCommand(cmd)
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		c.cmd = cmd
		c.owner = owner
		c.stdin = stdin
		stdout = outPipe
	}

	go c.readLoop(stdout)

	initResp, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo": map[string]any{
			"name":    "Astrorder",
			"version": "1.0",
		},
		"capabilities": map[string]any{},
	})
	if err != nil {
		_ = c.forceClose()
		<-c.done
		return nil, fmt.Errorf("Antigravity ACP initialize failed: %w", err)
	}

	c.applyInitializeCapabilities(initResp)
	return c, nil
}

func (c *acpClient) applyInitializeCapabilities(initResp map[string]any) {
	caps, _ := initResp["agentCapabilities"].(map[string]any)
	if caps == nil {
		return
	}
	if loadVal, ok := caps["loadSession"].(bool); ok && loadVal {
		c.hasLoad = true
	}
	if promptCaps, ok := caps["promptCapabilities"].(map[string]any); ok {
		c.promptImage, _ = promptCaps["image"].(bool)
		c.promptEmbedded, _ = promptCaps["embeddedContext"].(bool)
	}
	if sessCaps, ok := caps["sessionCapabilities"].(map[string]any); ok {
		_, c.hasResume = sessCaps["resume"]
		if _, ok := sessCaps["load"]; ok {
			c.hasLoad = true
		}
		_, c.hasClose = sessCaps["close"]
		_, c.hasList = sessCaps["list"]
	}
}

func (c *acpClient) pid() int {
	if c != nil && c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Pid
	}
	return 0
}

func (c *acpClient) exited() bool {
	if c == nil {
		return true
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *acpClient) remoteExitConfirmed() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitConfirmed
}

func (c *acpClient) writeFrame(payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.stdin == nil {
		return fmt.Errorf("Antigravity ACP stdin unavailable")
	}
	_, err = c.stdin.Write(raw)
	return err
}

func (c *acpClient) call(ctx context.Context, method string, params any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	c.mu.Lock()
	if c.closed || c.exited() {
		c.mu.Unlock()
		return nil, fmt.Errorf("Antigravity ACP transport is closed")
	}
	c.nextID++
	id := c.nextID
	key := fmt.Sprint(id)
	ch := make(chan acpCallResult, 1)
	c.pending[key] = ch
	c.mu.Unlock()

	if err := c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case res := <-ch:
		return res.result, res.err
	case <-c.done:
		c.mu.Lock()
		reason := c.failedReason
		exitErr := c.exitErr
		c.mu.Unlock()
		if reason != "" {
			return nil, fmt.Errorf("%s", reason)
		}
		if exitErr != nil {
			return nil, fmt.Errorf("Antigravity ACP transport exited: %w", exitErr)
		}
		return nil, fmt.Errorf("Antigravity ACP transport exited unexpectedly")
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *acpClient) notify(method string, params any) error {
	if params == nil {
		params = map[string]any{}
	}
	return c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *acpClient) respond(rawID any, result any) error {
	if result == nil {
		result = map[string]any{}
	}
	return c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"id":      rawID,
		"result":  result,
	})
}

func (c *acpClient) respondError(rawID any, code int, message string) error {
	return c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"id":      rawID,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (c *acpClient) readLoop(stdout io.Reader) {
	defer close(c.done)
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 65536), 64_000_000)

	var protoErr error
	for scan.Scan() {
		line := bytes.TrimSpace(scan.Bytes())
		if len(line) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var msg map[string]any
		if err := dec.Decode(&msg); err != nil {
			protoErr = fmt.Errorf("invalid Antigravity ACP JSON-RPC frame")
			_ = c.forceClose()
			break
		}

		method := str(msg, "method")
		rawID, hasID := msg["id"]
		switch {
		case method != "" && hasID && rawID != nil:
			params, _ := msg["params"].(map[string]any)
			c.adapter.routeServerRequest(c, rawID, method, params)
		case method != "":
			params, _ := msg["params"].(map[string]any)
			c.adapter.routeNotification(c, method, params)
		case hasID && rawID != nil:
			key := fmt.Sprint(rawID)
			var callRes acpCallResult
			if errObj, ok := msg["error"].(map[string]any); ok && errObj != nil {
				code, _ := intVal(errObj["code"])
				msgStr := str(errObj, "message")
				if dataStr := str(errObj, "data"); dataStr != "" && msgStr != "" {
					msgStr = msgStr + ": " + dataStr
				} else if dataMap, ok := errObj["data"].(map[string]any); ok {
					if detail := firstNonEmptyStr(dataMap, "details", "message", "error"); detail != "" {
						msgStr = msgStr + ": " + detail
					}
				}
				callRes.err = &acpRPCError{Code: code, Message: msgStr}
			} else {
				resMap, _ := msg["result"].(map[string]any)
				if resMap == nil {
					resMap = map[string]any{}
				}
				callRes.result = resMap
			}
			c.mu.Lock()
			ch := c.pending[key]
			delete(c.pending, key)
			c.mu.Unlock()
			if ch != nil {
				ch <- callRes
			}
		}
	}

	if scanErr := scan.Err(); scanErr != nil && protoErr == nil {
		protoErr = scanErr
	}

	exitConfirmed, waitErr := c.reapTransport()
	c.mu.Lock()
	c.exitConfirmed = exitConfirmed
	if protoErr != nil {
		c.failedReason = protoErr.Error()
		c.exitErr = protoErr
	} else {
		c.exitErr = waitErr
	}
	wasClosed := c.closed
	pending := c.pending
	c.pending = make(map[string]chan acpCallResult)
	c.mu.Unlock()

	failErr := protoErr
	if failErr == nil && waitErr != nil {
		failErr = waitErr
	}
	if failErr == nil {
		failErr = fmt.Errorf("Antigravity ACP transport ended")
	}
	for _, ch := range pending {
		ch <- acpCallResult{err: failErr}
	}

	if !wasClosed {
		reason := "Antigravity ACP transport closed"
		if protoErr != nil {
			reason = protoErr.Error()
		} else if waitErr != nil {
			reason = "Antigravity ACP transport ended without a confirmed idle session"
		}
		c.adapter.handleClientExit(c, reason)
	}
}

func (c *acpClient) reapTransport() (bool, error) {
	if c.ssh != nil {
		st, err := c.ssh.Wait()
		confirmed := err == nil && st.ConfirmedRemoteExit && !st.LocalKilled
		if err != nil {
			return false, err
		}
		if !st.ConfirmedRemoteExit {
			return false, fmt.Errorf("Antigravity remote ACP transport ended without confirmed exit")
		}
		if st.ExitCode != 0 {
			return confirmed, fmt.Errorf("Antigravity remote ACP process exited with status %d", st.ExitCode)
		}
		return confirmed, nil
	}
	if c.cmd != nil {
		return true, c.cmd.Wait()
	}
	return true, nil
}

func (c *acpClient) forceClose() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.writeMu.Lock()
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	c.writeMu.Unlock()
	if c.ssh != nil {
		return c.ssh.Close()
	}
	if c.owner != nil {
		return c.owner.Close()
	}
	return nil
}

func (c *acpClient) closeIdle(ctx context.Context) error {
	if c == nil || c.exited() {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	if c.ssh != nil {
		c.writeMu.Lock()
		var stdinErr error
		if c.stdin != nil {
			stdinErr = c.stdin.Close()
		}
		c.writeMu.Unlock()
		if stdinErr != nil {
			_ = c.ssh.Close()
			return fmt.Errorf("Antigravity remote ACP stdin EOF failed: %w", stdinErr)
		}
		select {
		case <-c.done:
		case <-ctx.Done():
			_ = c.ssh.Close()
			return ctx.Err()
		}
		if !c.remoteExitConfirmed() {
			_ = c.ssh.Close()
			return fmt.Errorf("Antigravity remote ACP exit was not confirmed")
		}
		_ = c.ssh.Close()
		return nil
	}

	c.writeMu.Lock()
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	c.writeMu.Unlock()
	if c.owner != nil {
		if err := c.owner.Close(); err != nil {
			return err
		}
		select {
		case <-c.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func mapRPCErrorToPreDispatch(err error) error {
	var rpcErr *acpRPCError
	if err != nil {
		if ok := asACPRPCError(err, &rpcErr); ok {
			return core.RejectBeforeDispatch(rpcErr.Message)
		}
	}
	return err
}

func asACPRPCError(err error, target **acpRPCError) bool {
	if e, ok := err.(*acpRPCError); ok {
		*target = e
		return true
	}
	return false
}
