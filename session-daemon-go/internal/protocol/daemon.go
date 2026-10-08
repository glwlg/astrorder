package protocol

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"astrorder.dev/session-daemon/internal/events"
	"astrorder.dev/session-daemon/internal/modelconfig"
	"astrorder.dev/session-daemon/internal/parity"
	core "astrorder.dev/session-daemon/internal/runtime"
	"github.com/coder/websocket"
)

type Daemon struct {
	modelConfig       *modelconfig.Service
	modelWake         chan struct{}
	modelStop         context.CancelFunc
	modelDone         chan struct{}
	modelReloadError  string
	shutdownOnce      sync.Once
	shutdownRequested chan struct{}
	closeOnce         sync.Once
	closeErr          error
	closers           []func() error
	registry          *core.Registry
	secret            string
	connectorSecret   string
	daemonID          string
	journal           *events.Journal
	connectors        map[string]map[string]any
	agentControls     map[string]string           // agentID -> controlSessionID
	pendingEvents     map[string][]map[string]any // agentID -> buffered events
	mu                sync.Mutex
}

func New(secret string) *Daemon {
	identity := make([]byte, 16)
	_, _ = rand.Read(identity)
	return &Daemon{
		shutdownRequested: make(chan struct{}),
		secret:            secret,
		daemonID:          hex.EncodeToString(identity),
		journal:           events.New(2000),
		registry:          core.NewRegistry(),
		connectors:        map[string]map[string]any{},
		agentControls:     map[string]string{},
		pendingEvents:     map[string][]map[string]any{},
	}
}

// ShutdownRequested is signaled after an accepted shutdown reply is written.
func (daemon *Daemon) ShutdownRequested() <-chan struct{} {
	return daemon.shutdownRequested
}

func (daemon *Daemon) AddCloser(closer func() error) {
	if closer == nil {
		return
	}
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	daemon.closers = append(daemon.closers, closer)
}

func (daemon *Daemon) SetConnectorSecret(secret string) {
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	daemon.connectorSecret = secret
}

// Match the Python daemon's IPC limit, including long prompts and attachments.
const maxMessageBytes = 16_000_000

func (daemon *Daemon) Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if path == "/ws/v1/connector" {
			daemon.serveConnector(response, request)
			return
		}
		socket, err := websocket.Accept(response, request, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer socket.CloseNow()
		socket.SetReadLimit(maxMessageBytes)
		daemon.serve(request.Context(), socket)
	})
}

func (daemon *Daemon) serve(ctx context.Context, socket *websocket.Conn) {
	daemon.serveStream(ctx, socket)
}

func (daemon *Daemon) handle(payload []byte, authenticated *bool) (map[string]any, bool) {
	return daemon.handleContext(context.Background(), payload, authenticated)
}

func (daemon *Daemon) handleContext(ctx context.Context, payload []byte, authenticated *bool) (map[string]any, bool) {
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil || request == nil {
		return map[string]any{"action": "error", "detail": "invalid JSON"}, true
	}
	requestID := request["request_id"]
	action, _ := request["action"].(string)
	if !*authenticated {
		if action != "daemon.handshake" {
			return errorResponse(requestID, "daemon handshake is required"), true
		}
		supplied, _ := request["secret"].(string)
		if subtle.ConstantTimeCompare([]byte(supplied), []byte(daemon.secret)) != 1 {
			return errorResponse(requestID, "daemon authentication failed"), true
		}
		*authenticated = true
		return map[string]any{
			"action":     "daemon.handshake.result",
			"request_id": requestID,
			"daemon_id":  daemon.daemonID,
			"operations": daemon.journal.OperationCapability(),
		}, true
	}
	if action == "session.sync" {
		return daemon.sync(request), true
	}
	if action == "daemon.status" {
		return daemon.status(requestID), true
	}
	if action == "session.event" {
		return daemon.recordEvent(request), true
	}
	if action == "daemon.shutdown" {
		return daemon.shutdown(request), true
	}
	if action == "operation.read" {
		return daemon.readOperation(request), true
	}
	return daemon.control(ctx, request), true
}

func (daemon *Daemon) status(requestID any) map[string]any {
	var manifest struct {
		RuntimeTypes []string `json:"runtime_types"`
	}
	_ = json.Unmarshal(parity.Manifest, &manifest)
	runtimes := map[string]any{}
	for _, name := range manifest.RuntimeTypes {
		runtimes[name] = map[string]any{"registered": false, "sessions": 0}
	}
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	sessions := map[string]any{}
	for sessionID, state := range daemon.journal.Statuses() {
		sessions[sessionID] = state
	}
	for kind, state := range daemon.registry.Statuses() {
		runtimes[kind] = state
	}
	for id, result := range daemon.registry.Sessions() {
		state, _ := sessions[id].(map[string]any)
		if state == nil {
			state = map[string]any{"min_seq_id": int64(0), "max_seq_id": int64(0)}
			state["status"] = result.Status
		}
		sessions[id] = state
	}
	connectors := make([]any, 0, len(daemon.connectors))
	for _, c := range daemon.connectors {
		connectors = append(connectors, c)
	}
	pending, pendingErr := daemon.journal.PendingOperations()
	var pendingValue any = pending
	if pendingErr != nil {
		pendingValue = nil
	}
	return map[string]any{
		"pending_operations": pendingValue,
		"operations":         daemon.journal.OperationCapability(),
		"action":             "daemon.status.result",
		"request_id":         requestID,
		"daemon_id":          daemon.daemonID,
		"sessions":           sessions,
		"connectors":         connectors,
		"replay":             map[string]any{"batch_limit": 128, "explicit_handoff": true},
		"storage":            daemon.journal.Health(),
		"runtimes":           runtimes,
		"model_config":       map[string]any{"configured": daemon.modelConfig != nil, "last_reload_error": daemon.modelReloadError},
	}
}

func (daemon *Daemon) recordEvent(request map[string]any) map[string]any {
	sessionID, _ := request["session_id"].(string)
	event, _ := request["event"].(string)
	status, _ := request["status"].(string)
	payload, _ := request["payload"].(map[string]any)
	if sessionID == "" || event == "" || status == "" {
		return errorResponse(request["request_id"], "session event is invalid")
	}
	timestamp := float64(time.Now().UnixMicro()) / 1e6
	if raw, exists := request["timestamp"]; exists {
		var ok bool
		timestamp, ok = raw.(float64)
		if !ok {
			return errorResponse(request["request_id"], "timestamp must be numeric")
		}
	}
	frame, err := daemon.journal.CommitAt(sessionID, event, payload, status, timestamp)
	if err != nil {
		return errorResponse(request["request_id"], "event persistence failed: "+err.Error())
	}
	return map[string]any{
		"action":     "session.event.result",
		"request_id": request["request_id"],
		"session_id": frame.SessionID,
		"seq_id":     frame.SeqID,
	}
}

func (daemon *Daemon) shutdown(request map[string]any) map[string]any {
	confirm, _ := request["confirm_active"].(bool)
	if pending, err := daemon.journal.PendingOperations(); !confirm && (err != nil || pending > 0) {
		return errorResponse(request["request_id"], "daemon has pending or uncertain durable operations; explicit confirmation is required")
	}
	if daemon.registry.Busy() && !confirm {
		return errorResponse(request["request_id"], "daemon has active runtime operations; explicit confirmation is required")
	}
	for _, state := range daemon.journal.Statuses() {
		status, _ := state["status"].(string)
		if status != "idle" && !confirm {
			return errorResponse(request["request_id"], "daemon has active sessions; explicit confirmation is required")
		}
	}
	return map[string]any{"action": "daemon.shutdown.result", "request_id": request["request_id"], "daemon_id": daemon.daemonID, "result": map[string]any{"stopping": true}}
}

func errorResponse(requestID any, detail string) map[string]any {
	response := map[string]any{"action": "error", "detail": detail}
	if requestID != nil {
		response["request_id"] = requestID
	}
	return response
}

func SecureEqual(left, right string) bool {
	return hmac.Equal([]byte(left), []byte(right))
}
