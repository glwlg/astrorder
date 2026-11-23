// Package search serves authenticated cross-session transcript full-text search.
package search

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type MessageSearcher interface {
	SearchMessages(context.Context, string, int) ([]store.MessageSearchHit, error)
}

type Options struct {
	Store      MessageSearcher
	Authorizer BrowserAuthorizer
}

type Handler struct {
	options Options
}

func NewHandler(options Options) *Handler {
	return &Handler{options: options}
}

func Matches(method, path string) bool {
	return method == http.MethodGet && path == "/api/v1/sessions/search"
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if h.options.Authorizer == nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"detail": "Authentication is not configured"})
		return
	}
	if failure := h.options.Authorizer.AuthorizeBrowser(request); failure != nil {
		writeJSON(response, failure.Status, map[string]string{"detail": failure.Detail})
		return
	}
	if h.options.Store == nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"detail": "Search store is not configured"})
		return
	}
	if request.Method != http.MethodGet {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]string{"detail": "Method Not Allowed"})
		return
	}

	q := strings.TrimSpace(request.URL.Query().Get("q"))
	limit := 20
	if rawLimit := strings.TrimSpace(request.URL.Query().Get("limit")); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if q == "" {
		writeJSON(response, http.StatusOK, map[string]any{"query": "", "items": []store.MessageSearchHit{}})
		return
	}

	items, err := h.options.Store.SearchMessages(request.Context(), q, limit)
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"query": q, "items": items})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
