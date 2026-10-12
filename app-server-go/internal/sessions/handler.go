package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

// Publisher broadcasts committed or server events to subscribers.
type Publisher interface {
	Publish(ctx context.Context, eventType string, data map[string]any) error
}

// Options provides required and optional dependencies for the sessions service.
type Options struct {
	Store             *store.Store
	Client            *daemonclient.Client
	Authorizer        *auth.Authenticator
	Publisher         Publisher
	DefaultWorkspace  string
	AttachmentsDir    string
	MaxAttachmentSize int64
	Resolver          *targetfs.Resolver
}

type taskState struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type backgroundTaskManager struct {
	mu    sync.Mutex
	tasks map[string]*taskState
}

func newBackgroundTaskManager() *backgroundTaskManager {
	return &backgroundTaskManager{
		tasks: make(map[string]*taskState),
	}
}

func (m *backgroundTaskManager) start(id string, cancel context.CancelFunc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tasks[id]; exists {
		return fmt.Errorf("task %s already exists", id)
	}
	m.tasks[id] = &taskState{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	return nil
}

func (m *backgroundTaskManager) cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return false
	}
	t.cancel()
	return true
}

func (m *backgroundTaskManager) finish(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		close(t.done)
		delete(m.tasks, id)
	}
}

// Handler dispatches all session, project, command, and bootstrap endpoints.
type Handler struct {
	store             *store.Store
	client            *daemonclient.Client
	authorizer        *auth.Authenticator
	publisher         Publisher
	bgTasks           *backgroundTaskManager
	defaultWorkspace  string
	attachmentsDir    string
	maxAttachmentSize int64
	resolver          *targetfs.Resolver
	nativeGuard
}

// NewHandler constructs a verified HTTP handler for all sessions endpoints.
func NewHandler(options Options) (http.Handler, error) {
	if options.Store == nil || options.Authorizer == nil || options.Publisher == nil {
		return nil, errors.New("store, authentication and publisher are required")
	}
	return &Handler{
		store:             options.Store,
		client:            options.Client,
		authorizer:        options.Authorizer,
		publisher:         options.Publisher,
		bgTasks:           newBackgroundTaskManager(),
		defaultWorkspace:  options.DefaultWorkspace,
		attachmentsDir:    options.AttachmentsDir,
		maxAttachmentSize: options.MaxAttachmentSize,
		resolver:          options.Resolver,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action, params := parseRoute(r.Method, r.URL.Path)
	if action == "" {
		writeJSONError(w, http.StatusNotFound, "Not Found")
		return
	}

	// Browser authorization check
	if h.authorizer != nil {
		if authErr := h.authorizer.AuthorizeBrowser(r); authErr != nil {
			writeJSONError(w, authErr.Status, authErr.Detail)
			return
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 16000000)

	switch action {
	// Bootstrap & Presence
	case "bootstrap":
		h.handleBootstrap(w, r)

	case "open_sessions":
		h.handleOpenSessions(w, r)
	case "presence":
		h.handlePresence(w, r)
	case "user_activity":
		h.handleUserActivity(w, r)
	case "refresh_activity":
		h.handleRefreshActivity(w, r)
	case "sync_session":
		h.handleSyncSession(w, r, params.SessionID)
	case "list_messages":
		h.handleListMessages(w, r, params.SessionID)
	case "session_models":
		h.handleSessionModels(w, r, params.SessionID)
	case "session_agent_commands":
		h.handleSessionAgentCommands(w, r, params.SessionID)
	case "session_agent_mentions":
		h.handleSessionAgentMentions(w, r, params.SessionID)
	case "get_session_model":
		h.handleGetSessionModel(w, r, params.SessionID)
	case "set_session_model":
		h.handleSetSessionModel(w, r, params.SessionID)
	case "set_session_reasoning":
		h.handleSetSessionReasoning(w, r, params.SessionID)
	case "get_session_approval_mode":
		h.handleGetSessionApprovalMode(w, r, params.SessionID)
	case "set_session_approval_mode":
		h.handleSetSessionApprovalMode(w, r, params.SessionID)

	// Session CRUD
	case "list_sessions":
		h.handleListSessions(w, r)
	case "create_session":
		h.handleCreateSession(w, r)
	case "update_session":
		h.handleUpdateSession(w, r, params.SessionID)
	case "delete_session":
		h.handleDeleteSession(w, r, params.SessionID)
	case "batch_delete_sessions":
		h.handleBatchDeleteSessions(w, r)
	case "create_project":
		h.handleCreateProject(w, r)
	case "delete_project":
		h.handleDeleteProject(w, r)

	// Command & Task & Queue
	case "create_command":
		h.handleCreateCommand(w, r)
	case "list_commands":
		h.handleListCommands(w, r, params.SessionID)
	case "list_tasks":
		h.handleListTasks(w, r, params.SessionID)
	case "edit_queued_command":
		h.handleEditQueuedCommand(w, r, params.CommandID)
	case "delete_queued_command":
		h.handleDeleteQueuedCommand(w, r, params.CommandID)
	case "send_queued_command":
		h.handleSendQueuedCommand(w, r, params.CommandID)

	default:
		writeJSONError(w, http.StatusNotFound, "Not Found")
	}
}

func (h *Handler) publishEvent(ctx context.Context, eventType string, data map[string]any) {
	if h.publisher != nil {
		_ = h.publisher.Publish(ctx, eventType, data)
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

func decodeJSON(r io.Reader, dst any) error {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("trailing request JSON")
	}
	return nil
}
