package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// ExtensionMessage represents a message exchanged with a browser extension.
type ExtensionMessage struct {
	ID     string         `json:"id,omitempty"`
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
	Result any            `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// ExtensionConn represents a connected browser extension WebSocket client.
type ExtensionConn struct {
	ws        *websocket.Conn
	sessionID string
	sendMu    sync.Mutex
	closed    atomic.Bool
}

// ExtensionProvider handles browser extension WebSocket connections if the extension connects to the backend.
type ExtensionProvider struct {
	connectionsMu sync.RWMutex
	connections   map[string]*ExtensionConn
}

// NewExtensionProvider creates a new ExtensionProvider.
func NewExtensionProvider() *ExtensionProvider {
	return &ExtensionProvider{
		connections: make(map[string]*ExtensionConn),
	}
}

// HandleExtensionWebSocket upgrades and serves an extension WebSocket connection if supported.
func (p *ExtensionProvider) HandleExtensionWebSocket(w http.ResponseWriter, r *http.Request) error {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = r.Header.Get("X-Session-ID")
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return fmt.Errorf("accept extension ws: %w", err)
	}

	extConn := &ExtensionConn{
		ws:        conn,
		sessionID: sessionID,
	}

	if sessionID != "" {
		p.connectionsMu.Lock()
		p.connections[sessionID] = extConn
		p.connectionsMu.Unlock()
		defer func() {
			p.connectionsMu.Lock()
			delete(p.connections, sessionID)
			p.connectionsMu.Unlock()
		}()
	}

	defer conn.Close(websocket.StatusNormalClosure, "closed")

	for {
		_, msg, err := conn.Read(r.Context())
		if err != nil {
			return err
		}
		var req ExtensionMessage
		if err := json.Unmarshal(msg, &req); err != nil {
			continue
		}
		// Echo or acknowledge
		if req.ID != "" {
			resp := ExtensionMessage{
				ID:     req.ID,
				Action: req.Action,
				Result: map[string]any{"status": "ok"},
			}
			data, _ := json.Marshal(resp)
			_ = conn.Write(r.Context(), websocket.MessageText, data)
		}
	}
}

// Send sends a command to a connected extension for a given session.
func (p *ExtensionProvider) Send(ctx context.Context, sessionID string, action string, params map[string]any) (any, error) {
	p.connectionsMu.RLock()
	conn := p.connections[sessionID]
	p.connectionsMu.RUnlock()

	if conn == nil {
		return nil, errors.New("extension not connected for session")
	}

	msg := ExtensionMessage{
		ID:     fmt.Sprintf("req-%d", time.Now().UnixNano()),
		Action: action,
		Params: params,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	conn.sendMu.Lock()
	defer conn.sendMu.Unlock()
	if err := conn.ws.Write(ctx, websocket.MessageText, data); err != nil {
		return nil, err
	}
	return map[string]any{"status": "sent"}, nil
}
