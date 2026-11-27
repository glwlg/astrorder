package desktopcontrol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	mcpProtocolVersion = "2024-11-05"
	maxStderrBuffer    = 8192
	maxResponseBytes   = 32 << 20 // 32 MiB cap for high-DPI screenshots
)

type mcpToolResult struct {
	Text    string
	Images  []ImageBlock
	IsError bool
}

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MCPStdioClient is a minimal, zero-dependency JSON-RPC 2.0 MCP client over stdio.
// Calls are serialized and bounded by context deadlines; any transport error or
// context cancellation tears down the subprocess so callers can cleanly respawn.
type MCPStdioClient struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	stderrTail *tailBuffer

	serverInfo mcpServerInfo
	nextID     atomic.Int64
	callMu     sync.Mutex

	closeOnce sync.Once
	closed    atomic.Bool
	waitDone  chan struct{}
	waitErr   error
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}

// StartMCPStdio launches command args... and completes the MCP initialize handshake.
func StartMCPStdio(ctx context.Context, command string, args []string, extraEnv []string) (*MCPStdioClient, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("mcp command is required")
	}

	cmd := exec.Command(command, args...)
	configureHiddenCommand(cmd)
	env := os.Environ()
	env = append(env, "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	env = append(env, extraEnv...)
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create mcp stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create mcp stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create mcp stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", command, err)
	}

	client := &MCPStdioClient{
		cmd:        cmd,
		stdin:      stdin,
		stdout:     bufio.NewReaderSize(stdoutPipe, 64*1024),
		stderrTail: &tailBuffer{max: maxStderrBuffer},
		waitDone:   make(chan struct{}),
	}

	go func() {
		_, _ = io.Copy(client.stderrTail, stderrPipe)
	}()
	go func() {
		client.waitErr = cmd.Wait()
		close(client.waitDone)
	}()

	initParams := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "astrorder-desktop-control",
			"version": "1.0.0",
		},
	}
	rawInit, err := client.request(ctx, "initialize", initParams)
	if err != nil {
		_ = client.Close()
		return nil, client.decorateError("mcp initialize failed", err)
	}
	var initResp struct {
		ServerInfo mcpServerInfo `json:"serverInfo"`
	}
	_ = json.Unmarshal(rawInit, &initResp)
	client.serverInfo = initResp.ServerInfo

	if err := client.notify("notifications/initialized", map[string]any{}); err != nil {
		_ = client.Close()
		return nil, client.decorateError("mcp initialized notification failed", err)
	}

	return client, nil
}

// ServerInfo returns the name and version reported by the MCP server during handshake.
func (c *MCPStdioClient) ServerInfo() mcpServerInfo {
	return c.serverInfo
}

// Alive reports whether the child MCP process is still running.
func (c *MCPStdioClient) Alive() bool {
	if c == nil || c.closed.Load() {
		return false
	}
	select {
	case <-c.waitDone:
		return false
	default:
		return true
	}
}

// CallTool invokes a named MCP tool and extracts text & image content blocks.
func (c *MCPStdioClient) CallTool(ctx context.Context, toolName string, arguments map[string]any) (*mcpToolResult, error) {
	if c == nil || c.closed.Load() {
		return nil, errors.New("mcp client is closed")
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	raw, err := c.request(ctx, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": arguments,
	})
	if err != nil {
		return nil, c.decorateError(fmt.Sprintf("mcp tool %q failed", toolName), err)
	}
	return parseMCPToolResult(raw)
}

// Close gracefully closes stdin and terminates the child MCP process.
func (c *MCPStdioClient) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		select {
		case <-c.waitDone:
		case <-time.After(1500 * time.Millisecond):
			if c.cmd != nil && c.cmd.Process != nil {
				_ = c.cmd.Process.Kill()
			}
			select {
			case <-c.waitDone:
			case <-time.After(1500 * time.Millisecond):
			}
		}
	})
	return nil
}

func (c *MCPStdioClient) notify(method string, params any) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	if c.closed.Load() {
		return errors.New("mcp client closed")
	}
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	return c.writeLineLocked(msg)
}

func (c *MCPStdioClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()

	if c.closed.Load() {
		return nil, errors.New("mcp client closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	reqID := c.nextID.Add(1)
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  method,
		"params":  params,
	}
	if err := c.writeLineLocked(msg); err != nil {
		_ = c.Close()
		return nil, err
	}

	type readOutcome struct {
		result json.RawMessage
		err    error
	}
	ch := make(chan readOutcome, 1)
	go func() {
		res, err := c.readMatchingResponseLocked(reqID)
		ch <- readOutcome{result: res, err: err}
	}()

	select {
	case outcome := <-ch:
		if outcome.err != nil {
			// If transport broke, close so next caller respawns cleanly.
			var rpcErr *jsonRPCError
			if !errors.As(outcome.err, &rpcErr) {
				_ = c.Close()
			}
		}
		return outcome.result, outcome.err
	case <-ctx.Done():
		// Terminate wedged process so background reader unblocks and exits.
		_ = c.Close()
		<-ch
		return nil, ctx.Err()
	}
}

func (c *MCPStdioClient) writeLineLocked(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mcp request: %w", err)
	}
	raw = append(raw, '\n')
	_, err = c.stdin.Write(raw)
	return err
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
}

func (c *MCPStdioClient) readMatchingResponseLocked(expectedID int64) (json.RawMessage, error) {
	for {
		line, err := readLineBounded(c.stdout, maxResponseBytes)
		if err != nil {
			return nil, err
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			// Ignore blank lines or incidental stdout banners before JSON-RPC frames.
			continue
		}
		var env rpcEnvelope
		if err := json.Unmarshal(trimmed, &env); err != nil {
			continue
		}
		if len(bytes.TrimSpace(env.ID)) == 0 {
			// Server-initiated notification (e.g. notifications/message) — skip.
			continue
		}
		gotID, ok := parseRPCID(env.ID)
		if !ok || gotID != expectedID {
			continue
		}
		if env.Error != nil {
			return nil, env.Error
		}
		return env.Result, nil
	}
}

func readLineBounded(r *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > limit {
			return nil, fmt.Errorf("mcp response frame exceeds %d bytes", limit)
		}
		if err == nil {
			return buf, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(buf) > 0 {
			return buf, nil
		}
		return nil, err
	}
}

func parseRPCID(raw json.RawMessage) (int64, bool) {
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if parsed, ok := coerceInt(s); ok {
			return int64(parsed), true
		}
	}
	return 0, false
}

func parseMCPToolResult(raw json.RawMessage) (*mcpToolResult, error) {
	var envelope struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
			MimeAlt  string `json:"mime_type"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode mcp tool result: %w", err)
	}
	res := &mcpToolResult{
		IsError: envelope.IsError,
	}
	var texts []string
	for _, block := range envelope.Content {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "text":
			if block.Text != "" {
				texts = append(texts, block.Text)
			}
		case "image":
			if strings.TrimSpace(block.Data) != "" {
				mt := strings.TrimSpace(block.MimeType)
				if mt == "" {
					mt = strings.TrimSpace(block.MimeAlt)
				}
				if mt == "" {
					mt = "image/png"
				}
				res.Images = append(res.Images, ImageBlock{
					MimeType:   mt,
					DataBase64: block.Data,
				})
			}
		}
	}
	res.Text = strings.Join(texts, "\n")
	return res, nil
}

func (c *MCPStdioClient) decorateError(prefix string, err error) error {
	if c == nil || c.stderrTail == nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if tail := c.stderrTail.String(); tail != "" {
		return fmt.Errorf("%s: %w (stderr: %s)", prefix, err, truncateForError(tail, 600))
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func truncateForError(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return "..." + s[len(s)-maxLen:]
}
