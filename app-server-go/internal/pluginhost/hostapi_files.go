package pluginhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/targetfs"
)

const (
	maxHostFileReadBytes  = 10 << 20 // 10 MiB read limit per plugin host file request
	maxHostFileWriteBytes = 10 << 20 // 10 MiB write limit per plugin host file request
	maxHostFileSearchHits = 200
)

func (m *Manager) resolvePluginWorkspaceFS(
	ctx context.Context,
	workspace string,
	connectionID string,
	sessionID string,
) (targetfs.Resolved, string, error) {
	ws := strings.TrimSpace(workspace)
	cid := strings.TrimSpace(connectionID)
	sid := strings.TrimSpace(sessionID)

	if ws == "" && sid != "" && m.store != nil {
		if sess, err := m.store.FindSessionByID(ctx, sid); err == nil && sess != nil {
			if s, ok := sess["workspace"].(string); ok && strings.TrimSpace(s) != "" {
				ws = strings.TrimSpace(s)
			}
			if cid == "" {
				if c, ok := sess["connection_id"].(string); ok {
					cid = strings.TrimSpace(c)
				}
			}
		}
	}
	if ws == "" {
		return targetfs.Resolved{}, "", fmt.Errorf("%w: workspace or session_id with workspace is required", ErrInvalid)
	}
	if m.resolver == nil {
		return targetfs.Resolved{}, "", errors.New("workspace file resolver is not configured")
	}

	cleanWS := targetfs.CleanPath(ws)
	resolved, err := m.resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    cleanWS,
		ConnectionID: cid,
		SessionID:    sid,
	})
	if err != nil {
		return targetfs.Resolved{}, "", err
	}
	root := resolved.Workspace
	if strings.TrimSpace(root) == "" {
		root = cleanWS
	}
	return resolved, root, nil
}

// resolveConfinedWorkspacePath resolves rawPath (relative to workspaceRoot or an absolute
// path strictly inside workspaceRoot) and rejects any traversal or escape outside workspaceRoot.
func resolveConfinedWorkspacePath(workspaceRoot, rawPath string) (string, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return "", fmt.Errorf("%w: workspace root is empty", ErrInvalid)
	}
	if strings.ContainsRune(rawPath, 0) {
		return "", fmt.Errorf("%w: path contains null byte", ErrInvalid)
	}

	isWin := isWindowsStylePath(root)
	cleanRoot := normalizeWorkspaceRoot(root, isWin)

	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" || trimmed == "." {
		return root, nil
	}

	var candidate string
	if isWin {
		normIn := strings.ReplaceAll(trimmed, "/", `\`)
		if isWindowsAbsolute(normIn) {
			candidate = filepath.Clean(normIn)
		} else {
			if strings.HasPrefix(normIn, `\`) {
				return "", fmt.Errorf("%w: path must be relative to workspace or inside workspace root", ErrInvalid)
			}
			candidate = filepath.Clean(cleanRoot + `\` + normIn)
		}
		if !isConfinedUnderRoot(cleanRoot, candidate, true) {
			return "", fmt.Errorf("%w: path %q escapes workspace root", ErrPermissionDenied, rawPath)
		}
		return candidate, nil
	}

	// POSIX workspace (Linux / WSL / SSH remote).
	if strings.Contains(trimmed, `\`) {
		return "", fmt.Errorf("%w: backslashes are not allowed in POSIX workspace path", ErrInvalid)
	}
	if strings.HasPrefix(trimmed, "/") {
		candidate = path.Clean(trimmed)
	} else {
		candidate = path.Clean(cleanRoot + "/" + trimmed)
	}
	if !isConfinedUnderRoot(cleanRoot, candidate, false) {
		return "", fmt.Errorf("%w: path %q escapes workspace root", ErrPermissionDenied, rawPath)
	}
	return candidate, nil
}

func isWindowsStylePath(p string) bool {
	return isWindowsAbsolute(p) || (strings.Contains(p, `\`) && !strings.HasPrefix(p, "/"))
}

func isWindowsAbsolute(p string) bool {
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return true
	}
	return strings.HasPrefix(p, `\\`)
}

func normalizeWorkspaceRoot(root string, isWin bool) string {
	if isWin {
		s := strings.ReplaceAll(strings.TrimSpace(root), "/", `\`)
		if len(s) == 2 && s[1] == ':' {
			return s + `\`
		}
		s = strings.TrimRight(s, `\`)
		if len(s) == 2 && s[1] == ':' {
			return s + `\`
		}
		return s
	}
	cleaned := path.Clean(strings.TrimSpace(root))
	return cleaned
}

func isConfinedUnderRoot(root, candidate string, isWin bool) bool {
	if isWin {
		r := strings.ToLower(strings.TrimRight(root, `\`))
		c := strings.ToLower(strings.TrimRight(candidate, `\`))
		if c == r {
			return true
		}
		return strings.HasPrefix(c, r+`\`)
	}
	r := strings.TrimRight(root, "/")
	c := strings.TrimRight(candidate, "/")
	if r == "" {
		return strings.HasPrefix(c, "/")
	}
	if c == r {
		return true
	}
	return strings.HasPrefix(c, r+"/")
}

func writeTargetFSError(w http.ResponseWriter, err error) {
	var tErr *targetfs.Error
	if errors.As(err, &tErr) && tErr.Status > 0 {
		writeJSONError(w, tErr.Status, tErr.Detail)
		return
	}
	writePluginError(w, err)
}

func (m *Manager) serveHostWorkspaceFiles(w http.ResponseWriter, r *http.Request, pluginID string, granted []string, sub string) {
	switch sub {
	case "list":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if !hasPermission(granted, PermWorkspaceFilesRead) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: workspace:files.read")
			return
		}
		m.handleHostFilesList(w, r)

	case "stat":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if !hasPermission(granted, PermWorkspaceFilesRead) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: workspace:files.read")
			return
		}
		m.handleHostFilesStat(w, r)

	case "read":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if !hasPermission(granted, PermWorkspaceFilesRead) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: workspace:files.read")
			return
		}
		m.handleHostFilesRead(w, r)

	case "search":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if !hasPermission(granted, PermWorkspaceFilesRead) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: workspace:files.read")
			return
		}
		m.handleHostFilesSearch(w, r)

	case "write":
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if !hasPermission(granted, PermWorkspaceFilesWrite) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: workspace:files.write")
			return
		}
		m.handleHostFilesWrite(w, r, pluginID)

	case "mkdir":
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if !hasPermission(granted, PermWorkspaceFilesWrite) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: workspace:files.write")
			return
		}
		m.handleHostFilesMkdir(w, r, pluginID)

	default:
		writeJSONError(w, http.StatusNotFound, "Unknown plugin host workspace files endpoint")
	}
}

func (m *Manager) handleHostFilesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resolved, root, err := m.resolvePluginWorkspaceFS(r.Context(), q.Get("workspace"), q.Get("connection_id"), q.Get("session_id"))
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	targetPath, err := resolveConfinedWorkspacePath(root, q.Get("path"))
	if err != nil {
		writePluginError(w, err)
		return
	}
	depth := 1
	if rawDepth := strings.TrimSpace(q.Get("depth")); rawDepth != "" {
		if parsed, parseErr := strconv.Atoi(rawDepth); parseErr == nil && parsed >= 1 && parsed <= 4 {
			depth = parsed
		}
	}
	res, err := resolved.FileSystem.List(r.Context(), targetfs.ListRequest{
		Path:  targetPath,
		Depth: depth,
	})
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace": root,
		"path":      res.Root,
		"name":      res.Name,
		"items":     res.Items,
		"truncated": res.Truncated,
	})
}

func (m *Manager) handleHostFilesStat(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resolved, root, err := m.resolvePluginWorkspaceFS(r.Context(), q.Get("workspace"), q.Get("connection_id"), q.Get("session_id"))
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	targetPath, err := resolveConfinedWorkspacePath(root, q.Get("path"))
	if err != nil {
		writePluginError(w, err)
		return
	}
	info, err := resolved.FileSystem.Stat(r.Context(), targetPath)
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace": root,
		"info":      info,
	})
}

func (m *Manager) handleHostFilesRead(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rawPath := strings.TrimSpace(q.Get("path"))
	if rawPath == "" {
		writeJSONError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}
	resolved, root, err := m.resolvePluginWorkspaceFS(r.Context(), q.Get("workspace"), q.Get("connection_id"), q.Get("session_id"))
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	targetPath, err := resolveConfinedWorkspacePath(root, rawPath)
	if err != nil {
		writePluginError(w, err)
		return
	}
	readRes, err := resolved.FileSystem.Read(r.Context(), targetPath)
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	defer readRes.Reader.Close()

	data, err := io.ReadAll(io.LimitReader(readRes.Reader, maxHostFileReadBytes+1))
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	if len(data) > maxHostFileReadBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("File exceeds %d byte plugin host read limit", maxHostFileReadBytes))
		return
	}

	encoding := strings.ToLower(strings.TrimSpace(q.Get("encoding")))
	if encoding == "raw" {
		ct := strings.TrimSpace(readRes.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}

	if encoding == "" {
		if utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
			encoding = "utf8"
		} else {
			encoding = "base64"
		}
	}

	var content string
	switch encoding {
	case "utf8", "utf-8", "text":
		encoding = "utf8"
		content = string(data)
	case "base64":
		content = base64.StdEncoding.EncodeToString(data)
	default:
		writeJSONError(w, http.StatusBadRequest, "encoding must be utf8, base64, or raw")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"workspace":    root,
		"path":         readRes.Info.Path,
		"name":         readRes.Info.Name,
		"size":         len(data),
		"content_type": readRes.ContentType,
		"encoding":     encoding,
		"content":      content,
		"mod_time":     readRes.Info.ModTime,
	})
}

func (m *Manager) handleHostFilesSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("query"))
	if query == "" {
		writeJSONError(w, http.StatusBadRequest, "query parameter is required")
		return
	}
	resolved, root, err := m.resolvePluginWorkspaceFS(r.Context(), q.Get("workspace"), q.Get("connection_id"), q.Get("session_id"))
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	limit := 50
	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		if parsed, parseErr := strconv.Atoi(rawLimit); parseErr == nil && parsed >= 1 {
			limit = parsed
		}
	}
	if limit > maxHostFileSearchHits {
		limit = maxHostFileSearchHits
	}
	res, err := resolved.FileSystem.Search(r.Context(), targetfs.SearchRequest{
		Root:  root,
		Query: query,
		Limit: limit,
	})
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace": root,
		"items":     res.Items,
	})
}

func (m *Manager) handleHostFilesWrite(w http.ResponseWriter, r *http.Request, pluginID string) {
	var req struct {
		Workspace    string `json:"workspace"`
		ConnectionID string `json:"connection_id"`
		SessionID    string `json:"session_id"`
		Path         string `json:"path"`
		Content      string `json:"content"`
		Encoding     string `json:"encoding"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxHostFileWriteBytes+(256<<10)))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeJSONError(w, http.StatusBadRequest, "path is required")
		return
	}
	resolved, root, err := m.resolvePluginWorkspaceFS(r.Context(), req.Workspace, req.ConnectionID, req.SessionID)
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	targetPath, err := resolveConfinedWorkspacePath(root, req.Path)
	if err != nil {
		writePluginError(w, err)
		return
	}
	if targetPath == root {
		writeJSONError(w, http.StatusBadRequest, "cannot overwrite workspace root directory")
		return
	}

	var payload []byte
	switch strings.ToLower(strings.TrimSpace(req.Encoding)) {
	case "", "utf8", "utf-8", "text":
		payload = []byte(req.Content)
	case "base64":
		decoded, decErr := base64.StdEncoding.DecodeString(req.Content)
		if decErr != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid base64 content: %v", decErr))
			return
		}
		payload = decoded
	default:
		writeJSONError(w, http.StatusBadRequest, "encoding must be utf8 or base64")
		return
	}
	if len(payload) > maxHostFileWriteBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("File content exceeds %d byte write limit", maxHostFileWriteBytes))
		return
	}

	info, err := resolved.FileSystem.WriteAtomic(r.Context(), targetPath, bytes.NewReader(payload), 0o644)
	if err != nil {
		writeTargetFSError(w, err)
		return
	}

	m.logger.Info("plugin host workspace file written",
		"plugin_id", pluginID,
		"workspace", root,
		"path", info.Path,
		"bytes", len(payload),
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"workspace": root,
		"info":      info,
	})
}

func (m *Manager) handleHostFilesMkdir(w http.ResponseWriter, r *http.Request, pluginID string) {
	var req struct {
		Workspace    string `json:"workspace"`
		ConnectionID string `json:"connection_id"`
		SessionID    string `json:"session_id"`
		Parent       string `json:"parent"`
		Name         string `json:"name"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxHostAPIBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		writeJSONError(w, http.StatusBadRequest, "valid directory name is required")
		return
	}
	resolved, root, err := m.resolvePluginWorkspaceFS(r.Context(), req.Workspace, req.ConnectionID, req.SessionID)
	if err != nil {
		writeTargetFSError(w, err)
		return
	}
	parentPath, err := resolveConfinedWorkspacePath(root, req.Parent)
	if err != nil {
		writePluginError(w, err)
		return
	}
	info, err := resolved.FileSystem.Mkdir(r.Context(), targetfs.MkdirRequest{
		Parent: parentPath,
		Name:   name,
	})
	if err != nil {
		writeTargetFSError(w, err)
		return
	}

	m.logger.Info("plugin host workspace directory created",
		"plugin_id", pluginID,
		"workspace", root,
		"path", info.Path,
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"workspace": root,
		"info":      info,
	})
}
