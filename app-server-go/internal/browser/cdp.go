package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// CDPClient manages a WebSocket connection to a CDP target or browser.
type CDPClient struct {
	ws        *websocket.Conn
	requestID atomic.Int64
	pending   sync.Map // int64 -> chan cdpResponse
	eventsMu  sync.RWMutex
	onEvent   func(method string, params json.RawMessage)
	closed    atomic.Bool
	closeOnce sync.Once
	done      chan struct{}
}

type cdpRequest struct {
	ID     int64          `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type cdpResponse struct {
	ID     int64           `json:"id"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// DialCDP connects to a CDP WebSocket URL with a context timeout.
func DialCDP(ctx context.Context, wsURL string) (*CDPClient, error) {
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial cdp ws %s: %w", wsURL, err)
	}

	client := &CDPClient{
		ws:   conn,
		done: make(chan struct{}),
	}
	go client.readLoop()
	return client, nil
}

// SetEventHandler registers a handler for non-correlated event messages.
func (c *CDPClient) SetEventHandler(handler func(method string, params json.RawMessage)) {
	c.eventsMu.Lock()
	defer c.eventsMu.Unlock()
	c.onEvent = handler
}

// Call sends a CDP command and waits for the correlated response.
func (c *CDPClient) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, errors.New("浏览器页面连接中断，可以在原窗口继续任务")
	}

	id := c.requestID.Add(1)
	req := cdpRequest{
		ID:     id,
		Method: method,
		Params: params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal cdp request: %w", err)
	}

	respChan := make(chan cdpResponse, 1)
	c.pending.Store(id, respChan)
	defer c.pending.Delete(id)

	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		return nil, fmt.Errorf("浏览器页面连接中断，可以在原窗口继续任务: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("浏览器页面连接中断，可以在原窗口继续任务")
	case resp := <-respChan:
		if resp.Error != nil {
			msg := resp.Error.Message
			if msg == "" {
				msg = fmt.Sprintf("CDP 调用失败：%s", method)
			}
			return nil, errors.New(msg)
		}
		return resp.Result, nil
	}
}

func (c *CDPClient) readLoop() {
	defer func() {
		c.closed.Store(true)
		close(c.done)
		// Drain all pending calls
		c.pending.Range(func(key, value any) bool {
			if ch, ok := value.(chan cdpResponse); ok {
				ch <- cdpResponse{
					Error: &cdpError{Message: "浏览器页面连接中断，可以在原窗口继续任务"},
				}
			}
			return true
		})
	}()

	for {
		_, msg, err := c.ws.Read(context.Background())
		if err != nil {
			return
		}

		var resp cdpResponse
		dec := json.NewDecoder(strings.NewReader(string(msg)))
		dec.UseNumber()
		if err := dec.Decode(&resp); err != nil {
			continue
		}

		if resp.ID != 0 {
			if ch, ok := c.pending.Load(resp.ID); ok {
				if respChan, valid := ch.(chan cdpResponse); valid {
					respChan <- resp
				}
			}
		} else if resp.Method != "" {
			c.eventsMu.RLock()
			handler := c.onEvent
			c.eventsMu.RUnlock()
			if handler != nil {
				handler(resp.Method, resp.Params)
			}
		}
	}
}

// Close closes the WebSocket connection.
func (c *CDPClient) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		_ = c.ws.Close(websocket.StatusNormalClosure, "closed")
	})
	return nil
}

// CDPEndpoints represents DevTools HTTP discovery discovery.
type CDPEndpoints struct {
	BaseURL    string
	HTTPClient *http.Client
}

// VersionInfo holds browser version info from /json/version.
type VersionInfo struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	UserAgent            string `json:"User-Agent"`
	V8Version            string `json:"V8-Version"`
	WebKitVersion        string `json:"WebKit-Version"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// PageTarget holds CDP target info from /json/list.
type PageTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	DevtoolsFrontendURL  string `json:"devtoolsFrontendUrl"`
}

// GetVersion fetches /json/version.
func (e *CDPEndpoints) GetVersion(ctx context.Context) (*VersionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.BaseURL+"/json/version", nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var info VersionInfo
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ListPages fetches /json/list and filters type == "page".
func (e *CDPEndpoints) ListPages(ctx context.Context) ([]PageTarget, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.BaseURL+"/json/list", nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var targets []PageTarget
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&targets); err != nil {
		return nil, err
	}

	var pages []PageTarget
	for _, t := range targets {
		if t.Type == "page" {
			pages = append(pages, t)
		}
	}
	return pages, nil
}

// ClosePage calls /json/close/{target_id}.
func (e *CDPEndpoints) ClosePage(ctx context.Context, targetID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.BaseURL+"/json/close/"+url.PathEscape(targetID), nil)
	if err != nil {
		return err
	}
	resp, err := e.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
