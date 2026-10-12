package agentapi

import (
	"context"
	"errors"

	"net/http"
	"strings"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/blackboard"
	"astrorder.dev/app-server/internal/browser"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/swarm"
)

// Publisher interface for publishing application events.
type Publisher interface {
	Publish(ctx context.Context, eventType string, data map[string]any) error
}

// Authorizer authorizes agent and browser HTTP requests.
type Authorizer interface {
	AuthorizeAgent(r *http.Request) (auth.Principal, *auth.Error)
	AuthorizeBrowser(r *http.Request) *auth.Error
}

// BrowserManager interface for browser capability delegation.
type BrowserManager interface {
	RunTask(ctx context.Context, input map[string]any) (map[string]any, error)
	CaptureScreenshot(ctx context.Context, sessionKey string, requireOwner bool, targetID string) (*browser.Snapshot, error)
	CallCDP(ctx context.Context, sessionKey string, method string, params map[string]any, targetID string) (map[string]any, error)
}

// Options configure the Agent API gateway and MCP handlers.
type Options struct {
	Store            *store.Store
	Client           *daemonclient.Client
	Authorizer       Authorizer
	Publisher        Publisher
	BrowserManager   BrowserManager
	BrowserInvoke    func(context.Context, string, map[string]any) (any, error)
	CommandSubmit func(context.Context,map[string]any)(map[string]any,error)
	DefaultWorkspace string
}

// AgentApiError represents structured Agent API errors.
type AgentApiError struct {
	Code    string
	Message string
}

func (e *AgentApiError) Error() string {
	return e.Message
}

func NewAgentApiError(code, message string) *AgentApiError {
	return &AgentApiError{Code: code, Message: message}
}

// Handler handles /api/v1/agent/catalog, /api/v1/agent/invoke, and /api/v1/agent/mcp.
type Handler struct {
	options    Options
	blackboard *blackboard.Service
	swarm      *swarm.Service
}

// NewHandler constructs a complete Agent API handler.
func NewHandler(options Options) (http.Handler, error) {
	if options.Store == nil {
		return nil, errors.New("store is required")
	}

	h := &Handler{
		options: options,
	}

	h.blackboard = blackboard.NewService(options.Store, options.Publisher)
	h.swarm = swarm.NewService(options.Store, options.Publisher, h)

	return h, nil
}

// Matches returns true if method and path match the Agent API gateway routes.
func Matches(method, path string) bool {
	clean := strings.TrimRight(path, "/")
	switch clean {
	case "/api/v1/agent/catalog":
		return method == http.MethodGet
	case "/api/v1/agent/invoke", "/api/v1/agent/mcp":
		return method == http.MethodPost
	case "/api/v1/blackboard":
		return method == http.MethodGet
	default:
		// Also match /api/v1/agents/{agent_id}/mcp
		if strings.HasPrefix(clean, "/api/v1/agents/") && strings.HasSuffix(clean, "/mcp") {
			return method == http.MethodGet || method == http.MethodPost
		}
		return false
	}
}
