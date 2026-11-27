package pluginhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxAdminJSONBytes   = 1 << 20
	maxArchiveUploadSz  = 64 << 20 // 64 MiB upload limit
)

// Matches reports whether method + reqPath belong to the Extension Platform routes:
// /api/v1/extensions, /api/v1/extensions/*, /api/v1/ext/*, or /api/v1/plugin-host/*.
func (m *Manager) Matches(_, reqPath string) bool {
	return Matches(reqPath)
}

// Matches reports whether reqPath belongs to any Extension Platform route prefix.
func Matches(reqPath string) bool {
	clean := strings.TrimRight(reqPath, "/")
	if clean == BaseAdminPath || strings.HasPrefix(clean, BaseAdminPath+"/") {
		return true
	}
	if strings.HasPrefix(clean, BaseExtPath+"/") {
		return true
	}
	if strings.HasPrefix(clean, BaseHostAPIPath+"/") {
		return true
	}
	return false
}

// ServeHTTP dispatches plugin host APIs, static bridge SDK, sandboxed plugin UI assets,
// proxied extension endpoints, and browser-authenticated extension management routes.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimRight(r.URL.Path, "/")

	// 1. Plugin Host API (authenticated via per-plugin hostToken).
	if strings.HasPrefix(clean, BaseHostAPIPath+"/") {
		m.serveHostAPI(w, r)
		return
	}

	// 2. Public static SDK modules:
	//    - /api/v1/ext/_sdk/bridge.js (PostMessage bridge for sandboxed opaque-origin iframes)
	//    - /api/v1/ext/_sdk/*.js (shared host runtime ESM shims for same-page ESM plugins)
	if clean == SDKBridgePath {
		serveBridgeSDK(w, r)
		return
	}
	if strings.HasPrefix(clean, SDKESMDirPath) {
		serveSDKESMModule(w, r, strings.TrimPrefix(clean, SDKESMDirPath))
		return
	}

	// 3. Extension namespace: /api/v1/ext/{id}/...
	if strings.HasPrefix(clean, BaseExtPath+"/") {
		m.serveExtensionNamespace(w, r)
		return
	}

	// 4. Admin / Management namespace: /api/v1/extensions*
	if clean == BaseAdminPath || strings.HasPrefix(clean, BaseAdminPath+"/") {
		m.serveAdminAPI(w, r)
		return
	}

	writeJSONError(w, http.StatusNotFound, "Not found")
}

func (m *Manager) authorizeBrowser(w http.ResponseWriter, r *http.Request) bool {
	if m.authorizer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return false
	}
	if failure := m.authorizer.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return false
	}
	return true
}

func (m *Manager) serveExtensionNamespace(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, BaseExtPath+"/")
	rawID, subpath, hasSub := strings.Cut(rest, "/")
	pluginID, err := url.PathUnescape(rawID)
	if err != nil || ValidatePluginID(pluginID) != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid plugin id")
		return
	}

	// Sandboxed iframe static UI assets: /api/v1/ext/{id}/_ui/{file...}
	if hasSub && (subpath == "_ui" || strings.HasPrefix(subpath, "_ui/")) {
		fileRel := strings.TrimPrefix(subpath, "_ui")
		fileRel = strings.TrimLeft(fileRel, "/")
		m.servePluginStaticUI(w, r, pluginID, fileRel)
		return
	}

	// Same-page ES module assets: /api/v1/ext/{id}/_esm/{file...}
	if hasSub && (subpath == "_esm" || strings.HasPrefix(subpath, "_esm/")) {
		fileRel := strings.TrimPrefix(subpath, "_esm")
		fileRel = strings.TrimLeft(fileRel, "/")
		m.servePluginESM(w, r, pluginID, fileRel)
		return
	}

	// Dynamic extension endpoints require browser auth.
	if !m.authorizeBrowser(w, r) {
		return
	}

	m.mu.RLock()
	builtin, isB := m.builtins[pluginID]
	m.mu.RUnlock()

	extCopy, isE := m.lookupDiskPlugin(pluginID)

	if !isB && !isE {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Plugin %q not found", pluginID))
		return
	}

	normalizedSubpath := "/" + strings.TrimLeft(subpath, "/")

	if isB {
		bm := builtin.Manifest()
		bm.Normalize()
		enabled, err := m.storage.IsEnabled(r.Context(), pluginID, EffectiveDefaultEnabled(SourceBuiltin, bm))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to read plugin state")
			return
		}
		isTestEndpoint := r.Method == http.MethodPost &&
			bm.Settings.TestEndpoint != "" &&
			normalizedSubpath == "/"+strings.TrimLeft(bm.Settings.TestEndpoint, "/")
		if !enabled && !isTestEndpoint {
			writeJSONError(w, http.StatusForbidden, fmt.Sprintf("Plugin %q is disabled", pluginID))
			return
		}
		builtin.ServeExtension(w, r, normalizedSubpath)
		return
	}

	enabled, err := m.storage.IsEnabled(r.Context(), pluginID, EffectiveDefaultEnabled(extCopy.source, extCopy.manifest))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to read plugin state")
		return
	}
	if !enabled {
		writeJSONError(w, http.StatusForbidden, fmt.Sprintf("Plugin %q is disabled", pluginID))
		return
	}
	granted, err := m.storage.GetGrantedPermissions(r.Context(), pluginID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to read plugin permissions")
		return
	}
	if _, pending := computePermissionState(extCopy.manifest.Permissions, granted); len(pending) > 0 {
		writeJSONError(w, http.StatusForbidden, fmt.Sprintf("Plugin %q has ungranted permissions", pluginID))
		return
	}

	switch extCopy.manifest.Backend.Type {
	case BackendTypeProcess, BackendTypeHTTP:
		baseURL, err := m.resolveExternalBackendURL(r.Context(), extCopy)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		m.supervisor.ProxyHTTP(w, r, pluginID, baseURL, normalizedSubpath)
	default:
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Plugin %q does not expose backend endpoints", pluginID))
	}
}

// explicitESMContentType returns a deterministic MIME type for ESM plugin assets without
// consulting the host OS or Windows registry (which may mislabel .js as text/plain).
func explicitESMContentType(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".wasm":
		return "application/wasm"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	case ".txt", ".glsl", ".vert", ".frag", ".wgsl":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func (m *Manager) servePluginESM(w http.ResponseWriter, r *http.Request, pluginID, fileRel string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !m.authorizeBrowser(w, r) {
		return
	}

	var plug *externalPlugin
	if builtin, bf, isB := m.lookupBuiltinWithFrontend(pluginID); isB {
		if bf == nil || bf.source != SourceOfficial || !IsFrontendESM(bf.manifest) {
			writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Plugin %q does not provide an ESM frontend", pluginID))
			return
		}
		bm := builtin.Manifest()
		bm.Normalize()
		enabled, err := m.storage.IsEnabled(r.Context(), pluginID, EffectiveDefaultEnabled(SourceBuiltin, bm))
		if err != nil || !enabled {
			writeJSONError(w, http.StatusForbidden, fmt.Sprintf("Plugin %q is disabled", pluginID))
			return
		}
		plug = bf
	} else {
		diskPlug, ok := m.lookupDiskPlugin(pluginID)
		if !ok {
			writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Plugin %q not found", pluginID))
			return
		}
		if !IsFrontendESM(diskPlug.manifest) {
			writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Plugin %q does not provide an ESM frontend", pluginID))
			return
		}

		enabled, err := m.storage.IsEnabled(r.Context(), pluginID, EffectiveDefaultEnabled(diskPlug.source, diskPlug.manifest))
		if err != nil || !enabled {
			writeJSONError(w, http.StatusForbidden, fmt.Sprintf("Plugin %q is disabled", pluginID))
			return
		}

		if policyErr := m.checkFrontendESMPolicy(r.Context(), diskPlug.source, diskPlug.manifest, diskPlug.dir); policyErr != nil {
			writePluginError(w, policyErr)
			return
		}

		sigVerified := verifyPluginSignature(diskPlug.manifest, diskPlug.dir, m.trustedPublicKeys)
		reqPerms := effectiveRequiredPermissions(diskPlug.source, diskPlug.manifest, sigVerified)
		granted, err := m.storage.GetGrantedPermissions(r.Context(), pluginID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to read plugin permissions")
			return
		}
		if _, pending := computePermissionState(reqPerms, granted); len(pending) > 0 {
			writeJSONError(w, http.StatusForbidden, fmt.Sprintf("Plugin %q has ungranted permissions", pluginID))
			return
		}
		plug = diskPlug
	}

	cleanRel, err := ValidateRelativePluginPath(fileRel)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid ESM asset path: %v", err))
		return
	}
	if hasHiddenPathSegment(cleanRel) {
		writeJSONError(w, http.StatusForbidden, "Hidden files are not accessible")
		return
	}

	fullPath := filepath.Join(plug.dir, filepath.FromSlash(cleanRel))
	if !isPathStrictlyInside(plug.dir, fullPath) {
		writeJSONError(w, http.StatusForbidden, "Path escapes plugin directory")
		return
	}
	realBase, err := filepath.EvalSymlinks(plug.dir)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Plugin directory unavailable")
		return
	}
	realTarget, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "ESM asset not found")
		return
	}
	if !isPathStrictlyInside(realBase, realTarget) {
		writeJSONError(w, http.StatusForbidden, "Symlink escapes plugin directory")
		return
	}
	resolvedRel, relErr := filepath.Rel(realBase, realTarget)
	if relErr != nil || hasHiddenPathSegment(filepath.ToSlash(resolvedRel)) {
		writeJSONError(w, http.StatusForbidden, "Hidden files are not accessible")
		return
	}

	info, err := os.Stat(realTarget)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		writeJSONError(w, http.StatusNotFound, "ESM asset not found")
		return
	}

	f, err := os.Open(realTarget)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "ESM asset not found")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", explicitESMContentType(filepath.Ext(realTarget)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if v := strings.TrimSpace(r.URL.Query().Get("v")); v != "" && plug.contentDigest != "" && v == plug.contentDigest {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	http.ServeContent(w, r, filepath.Base(realTarget), info.ModTime(), f)
}

func (m *Manager) servePluginStaticUI(w http.ResponseWriter, r *http.Request, pluginID, fileRel string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	extCopy, ok := m.lookupDiskPlugin(pluginID)
	if !ok || extCopy == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Plugin %q UI not found", pluginID))
		return
	}

	enabled, err := m.storage.IsEnabled(r.Context(), pluginID, EffectiveDefaultEnabled(extCopy.source, extCopy.manifest))
	if err != nil || !enabled {
		writeJSONError(w, http.StatusForbidden, fmt.Sprintf("Plugin %q is disabled", pluginID))
		return
	}
	granted, err := m.storage.GetGrantedPermissions(r.Context(), pluginID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to read plugin permissions")
		return
	}
	if _, pending := computePermissionState(extCopy.manifest.Permissions, granted); len(pending) > 0 {
		writeJSONError(w, http.StatusForbidden, fmt.Sprintf("Plugin %q has ungranted permissions", pluginID))
		return
	}

	cleanRel, err := ValidateRelativePluginPath(fileRel)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid UI asset path: %v", err))
		return
	}
	// Block hidden files (e.g. .env) from being served over static UI route.
	if hasHiddenPathSegment(cleanRel) {
		writeJSONError(w, http.StatusForbidden, "Hidden files are not accessible")
		return
	}

	fullPath := filepath.Join(extCopy.dir, filepath.FromSlash(cleanRel))
	if !isPathStrictlyInside(extCopy.dir, fullPath) {
		writeJSONError(w, http.StatusForbidden, "Path escapes plugin directory")
		return
	}
	realBase, err := filepath.EvalSymlinks(extCopy.dir)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Plugin directory unavailable")
		return
	}
	realTarget, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "UI asset not found")
		return
	}
	if !isPathStrictlyInside(realBase, realTarget) {
		writeJSONError(w, http.StatusForbidden, "Symlink escapes plugin directory")
		return
	}
	resolvedRel, relErr := filepath.Rel(realBase, realTarget)
	if relErr != nil || hasHiddenPathSegment(filepath.ToSlash(resolvedRel)) {
		writeJSONError(w, http.StatusForbidden, "Hidden files are not accessible")
		return
	}

	info, err := os.Stat(realTarget)
	if err != nil || info.IsDir() {
		writeJSONError(w, http.StatusNotFound, "UI asset not found")
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	hostOrigin := requestOrigin(r)
	w.Header().Set(
		"Content-Security-Policy",
		fmt.Sprintf(
			"default-src %s 'unsafe-inline' data: blob:; connect-src 'none'; object-src 'none'; frame-ancestors 'self' http://localhost:* http://127.0.0.1:* http://wails.localhost https://wails.localhost",
			hostOrigin,
		),
	)
	ext := strings.ToLower(filepath.Ext(realTarget))
	if ext == ".html" || ext == ".htm" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	} else if ct := explicitESMContentType(ext); ct != "application/octet-stream" {
		w.Header().Set("Content-Type", ct)
	} else if extType := mime.TypeByExtension(ext); extType != "" {
		w.Header().Set("Content-Type", extType)
	}
	http.ServeFile(w, r, realTarget)
}

func (m *Manager) serveAdminAPI(w http.ResponseWriter, r *http.Request) {
	if !m.authorizeBrowser(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	clean := strings.TrimRight(r.URL.Path, "/")
	if clean == BaseAdminPath {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		items, err := m.ListPlugins(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}

	rest := strings.TrimPrefix(clean, BaseAdminPath+"/")
	parts := strings.Split(rest, "/")

	switch len(parts) {
	case 1:
		switch parts[0] {
		case "install":
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			m.handleAdminInstall(w, r)
			return
		case "reload":
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			if err := m.ReloadExternal(r.Context()); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			items, err := m.ListPlugins(r.Context())
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		default:
			pluginID, _ := url.PathUnescape(parts[0])
			switch r.Method {
			case http.MethodGet:
				view, err := m.GetPlugin(r.Context(), pluginID)
				if err != nil {
					writePluginError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"item": view})
				return
			case http.MethodDelete:
				if err := m.Uninstall(r.Context(), pluginID); err != nil {
					writePluginError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": pluginID})
				return
			default:
				writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
		}

	case 2:
		pluginID, _ := url.PathUnescape(parts[0])
		action := parts[1]
		switch action {
		case "enable":
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			m.handleAdminEnable(w, r, pluginID)
			return
		case "restart":
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			view, err := m.Restart(r.Context(), pluginID)
			if err != nil {
				writePluginError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"item": view})
			return
		case "config":
			switch r.Method {
			case http.MethodGet:
				cfg, err := m.GetConfig(r.Context(), pluginID)
				if err != nil {
					writePluginError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, cfg)
				return
			case http.MethodPut:
				m.handleAdminPutConfig(w, r, pluginID)
				return
			default:
				writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
		}
	}

	writeJSONError(w, http.StatusNotFound, "Unknown extension endpoint")
}

func (m *Manager) handleAdminInstall(w http.ResponseWriter, r *http.Request) {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, maxArchiveUploadSz)
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse upload: %v", err))
			return
		}
		if r.MultipartForm != nil {
			defer func() { _ = r.MultipartForm.RemoveAll() }()
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Multipart form field 'file' is required")
			return
		}
		defer file.Close()

		data, err := io.ReadAll(io.LimitReader(file, maxArchiveUploadSz+1))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to read archive: %v", err))
			return
		}
		if int64(len(data)) > maxArchiveUploadSz {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "Plugin archive exceeds size limit")
			return
		}

		view, err := m.InstallFromZip(r.Context(), bytes.NewReader(data), int64(len(data)))
		if err != nil {
			writePluginError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"item": view})
		return
	}

	var req struct {
		SourcePath string `json:"source_path"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxAdminJSONBytes))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON request body")
		return
	}
	if strings.TrimSpace(req.SourcePath) == "" {
		writeJSONError(w, http.StatusBadRequest, "source_path is required")
		return
	}

	view, err := m.InstallFromDir(r.Context(), req.SourcePath)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": view})
}

func (m *Manager) handleAdminEnable(w http.ResponseWriter, r *http.Request, pluginID string) {
	var req struct {
		Enabled          bool     `json:"enabled"`
		GrantPermissions []string `json:"grant_permissions,omitempty"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxAdminJSONBytes))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON request body")
		return
	}

	view, pending, err := m.SetEnabled(r.Context(), pluginID, req.Enabled, req.GrantPermissions)
	if errors.Is(err, ErrPermissionGrantRequired) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"requires_permission_grant": true,
			"pending_permissions":       pending,
			"item":                      view,
			"detail":                    "Plugin requires user permission grant before enabling",
		})
		return
	}
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": view})
}

func (m *Manager) handleAdminPutConfig(w http.ResponseWriter, r *http.Request, pluginID string) {
	var req struct {
		Values  map[string]any    `json:"values,omitempty"`
		Secrets map[string]string `json:"secrets,omitempty"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxAdminJSONBytes))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON request body")
		return
	}

	cfg, err := m.UpdateConfig(r.Context(), pluginID, req.Values, req.Secrets)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrConflict), errors.Is(err, ErrPermissionGrantRequired):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrPermissionDenied), errors.Is(err, ErrPluginDisabled):
		writeJSONError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return "'self'"
	}
	return scheme + "://" + host
}
