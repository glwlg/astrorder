// Package reviewrelay serves the review relay control plane from the App
// Server store: durable A/B bindings and durable review runs. These routes
// only read and write control-plane state; they never dispatch Agent commands,
// send messages, or start reviews. POST /validate (workspace snapshot
// comparison) remains owned by the legacy router until the next batch.
package reviewrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	maxJSONBody    = 2 << 20
	maxIDLength    = 256
	maxWorkspace   = 4096
	maxCommentText = 200_000
	maxErrorText   = 2000
	maxBaselineIDs = 1000
)

var runStatuses = map[string]bool{
	"validating": true, "reviewing": true, "forwarding": true,
	"draft_ready": true, "empty": true, "failed": true,
}

// Registration describes one route owned by this package. It mirrors the
// per-domain registration shape used by the other Go product packages.
type Registration struct {
	Kind   string `json:"kind"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Auth   string `json:"auth"`
	Owner  string `json:"owner"`
}

func Registrations() []Registration {
	return []Registration{
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/review-relay/bindings/{source_agent_id}/{source_session_id}", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPut, Path: "/api/v1/review-relay/bindings/{source_agent_id}/{source_session_id}", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodDelete, Path: "/api/v1/review-relay/bindings/{source_agent_id}/{source_session_id}", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/review-relay/runs", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/review-relay/runs", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/review-relay/runs/{run_id}", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPatch, Path: "/api/v1/review-relay/runs/{run_id}", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/review-relay/validate", Auth: "browser", Owner: "go-app"},
	}
}

const (
	bindingsPrefix = "/api/v1/review-relay/bindings/"
	runsPath       = "/api/v1/review-relay/runs"
	runsPrefix     = "/api/v1/review-relay/runs/"
	validatePath   = "/api/v1/review-relay/validate"
)

// Matches reports whether the App Server must dispatch this request to the
// review relay handler instead of the transitional migration proxy.
func Matches(method, path string) bool {
	switch {
	case path == validatePath:
		return method == http.MethodPost
	case path == runsPath:
		return method == http.MethodGet || method == http.MethodPost
	case strings.HasPrefix(path, runsPrefix):
		runID := strings.TrimPrefix(path, runsPrefix)
		return runID != "" && !strings.Contains(runID, "/") && (method == http.MethodGet || method == http.MethodPatch)
	case strings.HasPrefix(path, bindingsPrefix):
		parts := strings.Split(strings.TrimPrefix(path, bindingsPrefix), "/")
		return len(parts) == 2 && parts[0] != "" && parts[1] != "" &&
			(method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete)
	default:
		return false
	}
}

// BrowserAuthorizer is the browser credential boundary shared with the other
// Go product handlers.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// Store is the exact persistence surface owned by store.Store for the review
// relay control plane. The interface is intentionally narrow so tests can
// substitute fakes without touching the real schema.
type Store interface {
	GetReviewBinding(context.Context, string, string) (map[string]any, error)
	UpsertReviewBinding(context.Context, map[string]any) (map[string]any, error)
	DeleteReviewBinding(context.Context, string, string) (bool, error)
	CreateReviewRun(context.Context, map[string]any) (map[string]any, error)
	GetReviewRun(context.Context, string) (map[string]any, error)
	ListReviewRuns(context.Context, string, string, bool) ([]map[string]any, error)
	UpdateReviewRun(context.Context, string, map[string]any) (map[string]any, error)
	GetSession(context.Context, string, string) (map[string]any, error)
	GetAgent(context.Context, string) (map[string]any, error)
	GetSSHConnection(context.Context, string) (map[string]any, error)
}

type Options struct {
	Store      Store
	Authorizer BrowserAuthorizer
	Resolver   *targetfs.Resolver
}

type Handler struct{ options Options }

func NewHandler(options Options) http.Handler { return &Handler{options: options} }

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	action, sourceAgentID, sourceSessionID, runID := parseRoute(request.Method, request.URL.Path)
	if action == "" {
		writeError(response, http.StatusNotFound, "Not Found")
		return
	}
	if h.options.Authorizer == nil {
		writeError(response, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := h.options.Authorizer.AuthorizeBrowser(request); failure != nil {
		writeError(response, failure.Status, failure.Detail)
		return
	}
	if h.options.Store == nil {
		writeError(response, http.StatusServiceUnavailable, "Review relay storage is not configured")
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	var err error
	switch action {
	case "get_binding":
		err = h.getBinding(response, request, sourceAgentID, sourceSessionID)
	case "put_binding":
		err = h.putBinding(response, request, sourceAgentID, sourceSessionID)
	case "delete_binding":
		err = h.deleteBinding(response, request, sourceAgentID, sourceSessionID)
	case "create_run":
		err = h.createRun(response, request)
	case "list_runs":
		err = h.listRuns(response, request)
	case "get_run":
		err = h.getRun(response, request, runID)
	case "patch_run":
		err = h.patchRun(response, request, runID)
	case "validate":
		err = h.validate(response, request)
	}
	if err != nil {
		writeTargetError(response, err)
	}
}

func parseRoute(method, path string) (string, string, string, string) {
	switch {
	case path == validatePath:
		if method == http.MethodPost {
			return "validate", "", "", ""
		}
	case path == runsPath:
		if method == http.MethodPost {
			return "create_run", "", "", ""
		}
		if method == http.MethodGet {
			return "list_runs", "", "", ""
		}
	case strings.HasPrefix(path, runsPrefix):
		runID := strings.TrimPrefix(path, runsPrefix)
		if runID == "" || strings.Contains(runID, "/") {
			return "", "", "", ""
		}
		if method == http.MethodGet {
			return "get_run", "", "", runID
		}
		if method == http.MethodPatch {
			return "patch_run", "", "", runID
		}
	case strings.HasPrefix(path, bindingsPrefix):
		parts := strings.Split(strings.TrimPrefix(path, bindingsPrefix), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", "", ""
		}
		if method == http.MethodGet {
			return "get_binding", parts[0], parts[1], ""
		}
		if method == http.MethodPut {
			return "put_binding", parts[0], parts[1], ""
		}
		if method == http.MethodDelete {
			return "delete_binding", parts[0], parts[1], ""
		}
	}
	return "", "", "", ""
}

func (h *Handler) getBinding(response http.ResponseWriter, request *http.Request, sourceAgentID, sourceSessionID string) error {
	row, err := h.options.Store.GetReviewBinding(request.Context(), sourceAgentID, sourceSessionID)
	if err != nil {
		return storageError(err)
	}
	writeJSON(response, http.StatusOK, row)
	return nil
}

func (h *Handler) putBinding(response http.ResponseWriter, request *http.Request, sourceAgentID, sourceSessionID string) error {
	var payload struct {
		ReviewAgentID   *string `json:"review_agent_id"`
		ReviewSessionID *string `json:"review_session_id"`
		Workspace       *string `json:"workspace"`
		Enabled         *bool   `json:"enabled"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	reviewAgentID, err := requiredText(payload.ReviewAgentID, maxIDLength)
	if err != nil {
		return err
	}
	reviewSessionID, err := requiredText(payload.ReviewSessionID, maxIDLength)
	if err != nil {
		return err
	}
	workspace, err := requiredText(payload.Workspace, maxWorkspace)
	if err != nil {
		return err
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	row, err := h.options.Store.UpsertReviewBinding(request.Context(), map[string]any{
		"source_agent_id": sourceAgentID, "source_session_id": sourceSessionID,
		"review_agent_id": reviewAgentID, "review_session_id": reviewSessionID,
		"workspace": workspace, "enabled": enabled,
	})
	if err != nil {
		return storageError(err)
	}
	writeJSON(response, http.StatusOK, row)
	return nil
}

func (h *Handler) deleteBinding(response http.ResponseWriter, request *http.Request, sourceAgentID, sourceSessionID string) error {
	if _, err := h.options.Store.DeleteReviewBinding(request.Context(), sourceAgentID, sourceSessionID); err != nil {
		return storageError(err)
	}
	response.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) createRun(response http.ResponseWriter, request *http.Request) error {
	var payload struct {
		SourceAgentID   *string  `json:"source_agent_id"`
		SourceSessionID *string  `json:"source_session_id"`
		ReviewAgentID   *string  `json:"review_agent_id"`
		ReviewSessionID *string  `json:"review_session_id"`
		CommandID       *string  `json:"command_id"`
		BaselineIDs     []string `json:"baseline_ids"`
		Status          *string  `json:"status"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	sourceAgentID, err := requiredText(payload.SourceAgentID, maxIDLength)
	if err != nil {
		return err
	}
	sourceSessionID, err := requiredText(payload.SourceSessionID, maxIDLength)
	if err != nil {
		return err
	}
	reviewAgentID, err := requiredText(payload.ReviewAgentID, maxIDLength)
	if err != nil {
		return err
	}
	reviewSessionID, err := requiredText(payload.ReviewSessionID, maxIDLength)
	if err != nil {
		return err
	}
	commandID, err := requiredText(payload.CommandID, maxIDLength)
	if err != nil {
		return err
	}
	if payload.Status == nil || !runStatuses[*payload.Status] {
		return invalidRequest()
	}
	baselineIDs := payload.BaselineIDs
	if baselineIDs == nil {
		baselineIDs = []string{}
	}
	if len(baselineIDs) > maxBaselineIDs {
		return invalidRequest()
	}
	row, err := h.options.Store.CreateReviewRun(request.Context(), map[string]any{
		"source_agent_id": sourceAgentID, "source_session_id": sourceSessionID,
		"review_agent_id": reviewAgentID, "review_session_id": reviewSessionID,
		"command_id": commandID, "baseline_ids": baselineIDs, "status": *payload.Status,
	})
	if err != nil {
		return storageError(err)
	}
	writeJSON(response, http.StatusOK, row)
	return nil
}

func (h *Handler) listRuns(response http.ResponseWriter, request *http.Request) error {
	query := request.URL.Query()
	sourceAgentID, err := queryText(query.Get("source_agent_id"))
	if err != nil {
		return err
	}
	sourceSessionID, err := queryText(query.Get("source_session_id"))
	if err != nil {
		return err
	}
	activeOnly := false
	if raw, present := query["active_only"]; present {
		if len(raw) != 1 {
			return invalidRequest()
		}
		activeOnly, err = strconv.ParseBool(raw[0])
		if err != nil {
			return invalidRequest()
		}
	}
	rows, err := h.options.Store.ListReviewRuns(request.Context(), sourceAgentID, sourceSessionID, activeOnly)
	if err != nil {
		return storageError(err)
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": rows})
	return nil
}

func (h *Handler) getRun(response http.ResponseWriter, request *http.Request, runID string) error {
	row, err := h.options.Store.GetReviewRun(request.Context(), runID)
	if err != nil {
		return storageError(err)
	}
	if row == nil {
		writeError(response, http.StatusNotFound, "review run not found")
		return nil
	}
	writeJSON(response, http.StatusOK, row)
	return nil
}

func (h *Handler) patchRun(response http.ResponseWriter, request *http.Request, runID string) error {
	var payload struct {
		Status      *string `json:"status"`
		CommentText *string `json:"comment_text"`
		Error       *string `json:"error"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	if payload.Status != nil && !runStatuses[*payload.Status] {
		return invalidRequest()
	}
	if payload.CommentText != nil && utf8.RuneCountInString(*payload.CommentText) > maxCommentText {
		return invalidRequest()
	}
	if payload.Error != nil && utf8.RuneCountInString(*payload.Error) > maxErrorText {
		return invalidRequest()
	}
	update := map[string]any{}
	if payload.Status != nil {
		update["status"] = *payload.Status
	}
	if payload.CommentText != nil {
		update["comment_text"] = *payload.CommentText
	}
	if payload.Error != nil {
		update["error"] = *payload.Error
	}
	row, err := h.options.Store.UpdateReviewRun(request.Context(), runID, update)
	if err != nil {
		return storageError(err)
	}
	if row == nil {
		writeError(response, http.StatusNotFound, "review run not found")
		return nil
	}
	writeJSON(response, http.StatusOK, row)
	return nil
}

// decodeJSON enforces the Pydantic-equivalent contract: unknown fields, type
// mismatches, a null body, and trailing JSON are all rejected with 422, and
// oversized bodies fail instead of being silently truncated.
func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maxJSONBody))
	if err != nil {
		return invalidRequest()
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return invalidRequest()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalidRequest()
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return invalidRequest()
	}
	return nil
}

func requiredText(value *string, limit int) (string, error) {
	if value == nil {
		return "", invalidRequest()
	}
	text := *value
	if text == "" || utf8.RuneCountInString(text) > limit {
		return "", invalidRequest()
	}
	return text, nil
}

func queryText(value string) (string, error) {
	if value == "" || utf8.RuneCountInString(value) > maxIDLength {
		return "", invalidRequest()
	}
	return value, nil
}

func invalidRequest() error {
	return &targetfs.Error{Status: http.StatusUnprocessableEntity, Code: "invalid_request", Detail: "Invalid request"}
}

func storageError(err error) error {
	if err == nil {
		return nil
	}
	return &targetfs.Error{Status: http.StatusInternalServerError, Code: "review_relay_storage_failed", Detail: "Internal server error", Err: err}
}

func writeTargetError(response http.ResponseWriter, err error) {
	var target *targetfs.Error
	if errors.As(err, &target) {
		writeError(response, target.Status, target.Detail)
		return
	}
	writeError(response, http.StatusInternalServerError, "Internal server error")
}

func writeError(response http.ResponseWriter, status int, detail string) {
	writeJSON(response, status, map[string]string{"detail": detail})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
