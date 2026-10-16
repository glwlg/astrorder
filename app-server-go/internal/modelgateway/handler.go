package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/daemonclient"

	"astrorder.dev/app-server/internal/store"
)

// Publisher broadcasts domain events.
type Publisher interface {
	Publish(ctx context.Context, eventType string, payload map[string]any) error
}

// BrowserAuthorizer checks request browser authentication.
type BrowserAuthorizer interface {
	AuthorizeBrowser(request *http.Request) *auth.Error
}

// Options configures the model gateway handler.
type Options struct {
	Store      *store.Store
	Client     *daemonclient.Client
	Authorizer BrowserAuthorizer
	Publisher  Publisher
	HTTPClient *http.Client
}

// Handler handles model gateway, analytics, quota, and model sync routes.
type Handler struct {
	store      *store.Store
	client     *daemonclient.Client
	authorizer BrowserAuthorizer
	publisher  Publisher
	httpClient *http.Client
	jobs       *jobManager
}

// NewHandler constructs a new model gateway HTTP handler.
func NewHandler(opts Options) (http.Handler, error) {
	if opts.Store == nil {
		return nil, errors.New("store is required")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	}
	h := &Handler{

		store:      opts.Store,
		client:     opts.Client,
		authorizer: opts.Authorizer,
		publisher:  opts.Publisher,
		httpClient: httpClient,
		jobs:       newJobManager(),
	}
	return h, nil
}

// Matches checks if the method and path belong to the model gateway / analytics / model sync handler.
func Matches(method, path string) bool {
	clean := strings.TrimRight(path, "/")
	switch clean {
	case "/api/v1/analytics/config":
		return method == http.MethodGet || method == http.MethodPut
	case "/api/v1/analytics/usage", "/api/v1/analytics/model-quota", "/api/v1/analytics/quotas":
		return method == http.MethodGet
	case "/api/v1/model-sync/targets":
		return method == http.MethodGet
	case "/api/v1/model-sync/preview":
		return method == http.MethodPost
	case "/api/v1/model-sync/jobs":
		return method == http.MethodGet || method == http.MethodPost
	}

	if strings.HasPrefix(clean, "/api/v1/model-sync/jobs/") && strings.HasSuffix(clean, "/cancel") {
		return method == http.MethodPost
	}
	if strings.HasPrefix(clean, "/api/v1/sessions/") && strings.HasSuffix(clean, "/usage") {
		return method == http.MethodGet
	}
	return false
}

// Close gracefully drains in-flight background jobs within the provided context.
func (h *Handler) Close(ctx context.Context) error {
	if h.jobs != nil {
		return h.jobs.Close(ctx)
	}
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authorizer != nil {
		if failure := h.authorizer.AuthorizeBrowser(r); failure != nil {
			writeError(w, failure.Status, "unauthorized", failure.Detail)
			return
		}
	}

	clean := strings.TrimRight(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && clean == "/api/v1/analytics/config":
		h.handleGetAnalyticsConfig(w, r)
	case r.Method == http.MethodPut && clean == "/api/v1/analytics/config":
		h.handleUpdateAnalyticsConfig(w, r)
	case r.Method == http.MethodGet && clean == "/api/v1/analytics/usage":
		h.handleGetAnalyticsUsage(w, r)
	case r.Method == http.MethodGet && clean == "/api/v1/analytics/quotas":
		h.handleGetMagpieQuotas(w, r)
	case r.Method == http.MethodGet && clean == "/api/v1/analytics/model-quota":
		h.handleGetModelQuota(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(clean, "/api/v1/sessions/") && strings.HasSuffix(clean, "/usage"):
		h.handleGetSessionUsage(w, r)
	case r.Method == http.MethodGet && clean == "/api/v1/model-sync/targets":
		h.handleGetModelSyncTargets(w, r)
	case r.Method == http.MethodPost && clean == "/api/v1/model-sync/preview":
		h.handlePreviewModelSync(w, r)
	case r.Method == http.MethodPost && clean == "/api/v1/model-sync/jobs":
		h.handleStartModelSync(w, r)
	case r.Method == http.MethodGet && clean == "/api/v1/model-sync/jobs":
		h.handleGetModelSyncJobs(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(clean, "/api/v1/model-sync/jobs/") && strings.HasSuffix(clean, "/cancel"):
		h.handleCancelModelSync(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{
		"error":  code,
		"detail": detail,
	})
}
