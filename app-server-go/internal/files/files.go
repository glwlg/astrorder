package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/targetfs"
)

const maxJSONBody = 1024 * 1024

type Registration struct {
	Kind   string `json:"kind"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Auth   string `json:"auth"`
	Owner  string `json:"owner"`
}

func Registrations() []Registration {
	return []Registration{
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/files/tree", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/files/search", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/files/stage", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/files/mkdir", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/files/exists", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/files/raw", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/system/open-file", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/system/write-file", Auth: "browser", Owner: "go-app"},
	}
}

type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type Publisher interface {
	Publish(context.Context, string, map[string]any) error
}

type Clipboard interface {
	SetFiles(context.Context, []string) error
}

type FileOpener interface {
	Open(context.Context, string, string) error
}

type Options struct {
	Authorizer  BrowserAuthorizer
	Resolver    *targetfs.Resolver
	Publisher   Publisher
	Clipboard   Clipboard
	Opener      FileOpener
	StagingRoot string
}

type Handler struct {
	options Options
}

func NewHandler(options Options) http.Handler {
	return &Handler{options: options}
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	registration, ok := match(request.Method, request.URL.Path)
	if !ok {
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
	if h.options.Resolver == nil {
		writeError(response, http.StatusServiceUnavailable, "Target filesystem is not configured")
		return
	}
	var err error
	switch registration.Path {
	case "/api/v1/files/tree":
		err = h.tree(response, request)
	case "/api/v1/files/search":
		err = h.search(response, request)
	case "/api/v1/files/stage":
		err = h.stage(response, request)
	case "/api/v1/files/mkdir":
		err = h.mkdir(response, request)
	case "/api/v1/files/exists":
		err = h.exists(response, request)
	case "/api/v1/files/raw":
		err = h.raw(response, request)
	case "/api/v1/system/open-file":
		err = h.open(response, request)
	case "/api/v1/system/write-file":
		err = h.write(response, request)
	}
	if err != nil {
		writeTargetError(response, err)
	}
}

func match(method, path string) (Registration, bool) {
	for _, registration := range Registrations() {
		if registration.Method == method && registration.Path == path {
			return registration, true
		}
	}
	return Registration{}, false
}

func (h *Handler) resolve(ctx context.Context, workspace, sessionID, connectionID string) (targetfs.Resolved, error) {
	return h.options.Resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace: targetfs.CleanPath(workspace), SessionID: strings.TrimSpace(sessionID), ConnectionID: strings.TrimSpace(connectionID),
	})
}

func (h *Handler) tree(response http.ResponseWriter, request *http.Request) error {
	query := request.URL.Query()
	depth := 6
	if raw := query.Get("depth"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return &targetfs.Error{Status: 422, Code: "invalid_depth", Detail: "depth must be an integer", Err: err}
		}
		depth = value
	}
	workspace := query.Get("path")
	connectionID := strings.TrimSpace(query.Get("connection_id"))
	// A project picker navigates within the connection's scope, rather than
	// making each visited directory a new scope that hides its parent.
	if query.Get("session_id") == "" && connectionID != "" && connectionID != targetfs.LocalConnectionID {
		workspace = ""
	}
	resolved, err := h.resolve(request.Context(), workspace, query.Get("session_id"), connectionID)
	if err != nil {
		return err
	}
	result, err := resolved.FileSystem.List(request.Context(), targetfs.ListRequest{
		Path: targetfs.CleanPath(query.Get("path")), Depth: depth, RevealPath: targetfs.CleanPath(query.Get("reveal_path")),
	})
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, result)
	return nil
}

func (h *Handler) search(response http.ResponseWriter, request *http.Request) error {
	query := request.URL.Query()
	limit := 30
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			return &targetfs.Error{Status: 422, Code: "invalid_limit", Detail: "limit must be between 1 and 100", Err: err}
		}
		limit = value
	}
	if len(query.Get("q")) > 256 {
		return &targetfs.Error{Status: 422, Code: "invalid_query", Detail: "q must be at most 256 characters"}
	}
	resolved, err := h.resolve(request.Context(), "", query.Get("session_id"), query.Get("connection_id"))
	if err != nil {
		return err
	}
	result, err := resolved.FileSystem.Search(request.Context(), targetfs.SearchRequest{Root: resolved.Workspace, Query: query.Get("q"), Limit: limit})
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, result)
	return nil
}

type stagePayload struct {
	Paths           []string `json:"paths"`
	SessionID       string   `json:"session_id"`
	ConnectionID    string   `json:"connection_id"`
	CopyToClipboard *bool    `json:"copy_to_clipboard"`
}

func (h *Handler) stage(response http.ResponseWriter, request *http.Request) error {
	var payload stagePayload
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	if len(payload.Paths) == 0 || len(payload.Paths) > 100 {
		return &targetfs.Error{Status: 400, Code: "invalid_paths", Detail: "At least one and at most 100 paths are required"}
	}
	resolved, err := h.resolve(request.Context(), fileParent(targetfs.CleanPath(payload.Paths[0])), payload.SessionID, payload.ConnectionID)
	if err != nil {
		return err
	}
	paths := make([]string, len(payload.Paths))
	for index, path := range payload.Paths {
		paths[index] = targetfs.CleanPath(path)
	}
	stagedResult, err := resolved.FileSystem.Stage(request.Context(), targetfs.StageRequest{Paths: paths, Destination: func() string {
		if resolved.Kind == "local" {
			return ""
		}
		return h.options.StagingRoot
	}()})
	if err != nil {
		return err
	}
	staged := stagedResult.Paths
	copyToClipboard := payload.CopyToClipboard == nil || *payload.CopyToClipboard
	clipboardSet := false
	if copyToClipboard && len(staged) > 0 {
		if h.options.Clipboard == nil {
			return &targetfs.Error{Status: 503, Code: "clipboard_unavailable", Detail: "File clipboard is unavailable"}
		}
		if err := h.options.Clipboard.SetFiles(request.Context(), staged); err != nil {
			return &targetfs.Error{Status: 500, Code: "clipboard_failed", Detail: "Failed to copy files to clipboard", Err: err}
		}
		clipboardSet = true
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "local_paths": staged, "clipboard_set": clipboardSet})
	return nil
}

func (h *Handler) mkdir(response http.ResponseWriter, request *http.Request) error {
	var payload struct {
		ParentPath   string `json:"parent_path"`
		Name         string `json:"name"`
		ConnectionID string `json:"connection_id"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	parent := targetfs.CleanPath(payload.ParentPath)
	resolved, err := h.resolve(request.Context(), parent, "", payload.ConnectionID)
	if err != nil {
		return err
	}
	result, err := resolved.FileSystem.Mkdir(request.Context(), targetfs.MkdirRequest{Parent: parent, Name: payload.Name})
	if err != nil {
		return err
	}
	if err := h.publish(request.Context(), "files.changed", map[string]any{"action": "mkdir", "path": result.Path, "connection_id": resolved.ConnectionID}); err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "path": result.Path})
	return nil
}

func (h *Handler) exists(response http.ResponseWriter, request *http.Request) error {
	var payload struct {
		Workspace    string   `json:"workspace"`
		Paths        []string `json:"paths"`
		ConnectionID string   `json:"connection_id"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	if payload.Workspace == "" || len(payload.Paths) > 100 {
		return &targetfs.Error{Status: 400, Code: "invalid_file_path", Detail: "Invalid file path"}
	}
	resolved, err := h.resolve(request.Context(), payload.Workspace, "", payload.ConnectionID)
	if err != nil {
		return err
	}
	existing := make([]string, 0)
	for _, raw := range payload.Paths {
		if raw == "" || len(raw) > 1024 || strings.ContainsRune(raw, 0) {
			return &targetfs.Error{Status: 400, Code: "invalid_file_path", Detail: "Invalid file path"}
		}
		candidate := targetfs.CleanPath(raw)
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(resolved.Workspace, candidate)
		}
		info, statErr := resolved.FileSystem.Stat(request.Context(), candidate)
		if statErr == nil && !info.IsDir {
			existing = append(existing, raw)
			continue
		}
		if statErr != nil && targetfs.IsNotFound(statErr) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		return &targetfs.Error{Status: 400, Code: "invalid_file_path", Detail: "Invalid file path"}
	}
	writeJSON(response, http.StatusOK, map[string]any{"existing": existing})
	return nil
}

func (h *Handler) raw(response http.ResponseWriter, request *http.Request) error {
	query := request.URL.Query()
	path := targetfs.CleanPath(query.Get("path"))
	if path == "" {
		return &targetfs.Error{Status: 422, Code: "path_required", Detail: "path is required"}
	}
	resolved, err := h.resolve(request.Context(), fileParent(path), query.Get("session_id"), query.Get("connection_id"))
	if err != nil {
		return err
	}
	result, err := resolved.FileSystem.Read(request.Context(), path)
	if err != nil {
		return err
	}
	defer result.Reader.Close()
	response.Header().Set("Content-Type", result.ContentType)
	response.Header().Set("Content-Length", strconv.FormatInt(result.Info.Size, 10))
	if parseBool(query.Get("download")) {
		response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": result.Info.Name}))
	}
	response.WriteHeader(http.StatusOK)
	_, err = copyHTTP(request.Context(), response, result.Reader)
	return err
}

func (h *Handler) open(response http.ResponseWriter, request *http.Request) error {
	var payload struct {
		Path   string `json:"path"`
		Action string `json:"action"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	path := targetfs.CleanPath(payload.Path)
	if path == "" {
		return &targetfs.Error{Status: 400, Code: "path_required", Detail: "Path is required"}
	}
	resolved, err := h.resolve(request.Context(), fileParent(path), "", targetfs.LocalConnectionID)
	if err != nil {
		return err
	}
	info, err := resolved.FileSystem.Stat(request.Context(), path)
	if err != nil {
		return err
	}
	action := payload.Action
	if action == "" {
		action = "open"
	}
	if action != "open" && action != "reveal" {
		return &targetfs.Error{Status: 400, Code: "invalid_action", Detail: "Unsupported open action"}
	}
	if h.options.Opener == nil {
		return &targetfs.Error{Status: 503, Code: "opener_unavailable", Detail: "System file opener is unavailable"}
	}
	if err := h.options.Opener.Open(request.Context(), info.Path, action); err != nil {
		return &targetfs.Error{Status: 500, Code: "open_failed", Detail: "Failed to open file", Err: err}
	}
	writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "opened": info.Path})
	return nil
}

var writeExtensions = map[string]bool{
	".drawio": true, ".xml": true, ".svg": true, ".json": true, ".md": true, ".txt": true,
	".py": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".rs": true, ".go": true,
	".c": true, ".cpp": true, ".h": true, ".css": true, ".scss": true, ".less": true, ".html": true,
	".sql": true, ".yaml": true, ".yml": true, ".sh": true, ".bash": true, ".bat": true, ".ps1": true,
	".toml": true, ".ini": true, ".env": true, ".dockerfile": true, ".mmd": true, ".excalidraw": true,
}

func (h *Handler) write(response http.ResponseWriter, request *http.Request) error {
	var payload struct {
		Path         string  `json:"path"`
		Content      *string `json:"content"`
		SessionID    string  `json:"session_id"`
		ConnectionID string  `json:"connection_id"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	path := targetfs.CleanPath(payload.Path)
	if path == "" || payload.Content == nil {
		return &targetfs.Error{Status: 400, Code: "path_content_required", Detail: "Path and content are required"}
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension == "" && strings.HasPrefix(strings.ToLower(filepath.Base(path)), "dockerfile") {
		extension = ".dockerfile"
	}
	if !writeExtensions[extension] {
		return &targetfs.Error{Status: 403, Code: "extension_not_allowed", Detail: fmt.Sprintf("Extension %s is not allowed for write", filepath.Ext(path))}
	}
	resolved, err := h.resolve(request.Context(), fileParent(path), payload.SessionID, payload.ConnectionID)
	if err != nil {
		return err
	}
	info, err := resolved.FileSystem.WriteAtomic(request.Context(), path, strings.NewReader(*payload.Content), 0o600)
	if err != nil {
		return err
	}
	if err := h.publish(request.Context(), "files.changed", map[string]any{"action": "write", "path": info.Path, "connection_id": resolved.ConnectionID}); err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "path": info.Path})
	return nil
}

func (h *Handler) publish(ctx context.Context, event string, data map[string]any) error {
	if h.options.Publisher == nil {
		return nil
	}
	if err := h.options.Publisher.Publish(ctx, event, data); err != nil {
		return &targetfs.Error{Status: 500, Code: "publish_failed", Detail: "File change could not be published", Err: err}
	}
	return nil
}

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, maxJSONBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return &targetfs.Error{Status: 400, Code: "invalid_json", Detail: "Invalid JSON", Err: err}
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return &targetfs.Error{Status: 400, Code: "invalid_json", Detail: "Invalid JSON"}
	}
	return nil
}

func writeTargetError(response http.ResponseWriter, err error) {
	var target *targetfs.Error
	if errors.As(err, &target) {
		writeError(response, target.Status, target.Detail)
		return
	}
	if errors.Is(err, context.Canceled) {
		writeError(response, 499, "Request cancelled")
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

func parseBool(value string) bool {
	parsed, _ := strconv.ParseBool(value)
	return parsed
}

func copyHTTP(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		length, readErr := source.Read(buffer)
		if length > 0 {
			written, writeErr := destination.Write(buffer[:length])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

type NativeOpener struct{}

func (NativeOpener) Open(ctx context.Context, path, action string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The system window outlives this HTTP request. CommandContext would kill
	// the launcher as soon as ServeHTTP returns and cancels the request.
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if action == "reveal" && !isDirectory(path) {
			command = exec.Command("explorer.exe", "/select,", path)
		} else {
			command = exec.Command("explorer.exe", path)
		}
	case "darwin":
		if action == "reveal" && !isDirectory(path) {
			command = exec.Command("open", "-R", path)
		} else {
			command = exec.Command("open", path)
		}
	default:
		target := path
		if action == "reveal" && !isDirectory(path) {
			target = filepath.Dir(path)
		}
		command = exec.Command("xdg-open", target)
	}
	configureProcess(command)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func URLPath(value string) string {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return targetfs.CleanPath(decoded)
}
