package pluginhost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const maxHostAPIBodyBytes = 1 << 20

type gitInfoHostResponse struct {
	Detected    bool   `json:"detected"`
	Host        string `json:"host,omitempty"`
	Port        string `json:"port,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Branch      string `json:"branch,omitempty"`
	HeadSHA     string `json:"head_sha,omitempty"`
	RemoteName  string `json:"remote_name,omitempty"`
	RemoteURL   string `json:"remote_url,omitempty"`
}

func (m *Manager) serveHostAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token := extractHostToken(r)
	pluginID, ok := m.supervisor.ResolveHostToken(token)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "Invalid or missing plugin host token")
		return
	}

	enabled, err := m.storage.IsEnabled(r.Context(), pluginID, false)
	if err != nil || !enabled {
		writeJSONError(w, http.StatusForbidden, "Plugin is disabled")
		return
	}

	granted, err := m.storage.GetGrantedPermissions(r.Context(), pluginID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to load plugin permissions")
		return
	}

	clean := strings.TrimRight(r.URL.Path, "/")
	rest := strings.TrimPrefix(clean, BaseHostAPIPath)
	rest = strings.TrimLeft(rest, "/")

	switch rest {
	case "workspace/git-info":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if !hasPermission(granted, PermWorkspaceGitRead) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: workspace:git.read")
			return
		}
		m.handleHostGitInfo(w, r)

	case "storage/kv":
		if !hasPermission(granted, PermStorageKV) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: storage:kv")
			return
		}
		switch r.Method {
		case http.MethodGet:
			m.handleHostGetKV(w, r, pluginID)
		case http.MethodPut:
			m.handleHostPutKV(w, r, pluginID)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}

	case "storage/secrets":
		if !hasPermission(granted, PermStorageSecrets) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: storage:secrets")
			return
		}
		switch r.Method {
		case http.MethodGet:
			m.handleHostGetSecrets(w, r, pluginID)
		case http.MethodPut:
			m.handleHostPutSecrets(w, r, pluginID)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}

	case "sessions/context":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if !hasPermission(granted, PermWorkspaceGitRead) && !hasPermission(granted, PermAgentCapabilities) {
			writeJSONError(w, http.StatusForbidden, "Missing required permission: workspace:git.read or agent:capabilities")
			return
		}
		m.handleHostSessionContext(w, r)

	default:
		writeJSONError(w, http.StatusNotFound, "Unknown plugin host API endpoint")
	}
}

func (m *Manager) handleHostGitInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	workspace := strings.TrimSpace(q.Get("workspace"))
	connectionID := strings.TrimSpace(q.Get("connection_id"))
	sessionID := strings.TrimSpace(q.Get("session_id"))

	if workspace == "" && sessionID != "" && m.store != nil {
		if sess, err := m.store.FindSessionByID(r.Context(), sessionID); err == nil && sess != nil {
			if ws, ok := sess["workspace"].(string); ok && strings.TrimSpace(ws) != "" {
				workspace = strings.TrimSpace(ws)
			}
			if connectionID == "" {
				if cid, ok := sess["connection_id"].(string); ok {
					connectionID = strings.TrimSpace(cid)
				}
			}
		}
	}

	info, err := DetectWorkspaceGitInfo(r.Context(), m.resolver, workspace, connectionID, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if info == nil {
		writeJSON(w, http.StatusOK, gitInfoHostResponse{Detected: false})
		return
	}
	writeJSON(w, http.StatusOK, gitInfoHostResponse{
		Detected:    true,
		Host:        info.Host,
		Port:        info.Port,
		ProjectPath: info.ProjectPath,
		Branch:      info.Branch,
		HeadSHA:     info.HeadSHA,
		RemoteName:  info.RemoteName,
		RemoteURL:   info.RemoteURL,
	})
}

func (m *Manager) handleHostGetKV(w http.ResponseWriter, r *http.Request, pluginID string) {
	values, err := m.storage.GetKV(r.Context(), pluginID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to read KV storage")
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key != "" {
		val, exists := values[key]
		writeJSON(w, http.StatusOK, map[string]any{
			"key":    key,
			"value":  val,
			"exists": exists,
			"values": values,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"values": values,
	})
}

func (m *Manager) handleHostPutKV(w http.ResponseWriter, r *http.Request, pluginID string) {
	var raw map[string]any
	dec := json.NewDecoder(io.LimitReader(r.Body, maxHostAPIBodyBytes))
	if err := dec.Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	replace := strings.EqualFold(r.URL.Query().Get("replace"), "true")
	var updates map[string]any
	if v, ok := raw["values"].(map[string]any); ok {
		updates = v
		if rep, ok := raw["replace"].(bool); ok {
			replace = rep
		}
	} else if key, ok := raw["key"].(string); ok && strings.TrimSpace(key) != "" {
		updates = map[string]any{strings.TrimSpace(key): raw["value"]}
	} else {
		updates = raw
	}

	updated, err := m.storage.SetKV(r.Context(), pluginID, updates, replace)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to save KV storage")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"values": updated,
	})
}

func (m *Manager) handleHostGetSecrets(w http.ResponseWriter, r *http.Request, pluginID string) {
	secrets, err := m.storage.GetRawSecrets(r.Context(), pluginID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to read secrets storage")
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key != "" {
		val, exists := secrets[key]
		writeJSON(w, http.StatusOK, map[string]any{
			"key":     key,
			"value":   val,
			"exists":  exists,
			"secrets": secrets,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"secrets": secrets,
	})
}

func (m *Manager) handleHostPutSecrets(w http.ResponseWriter, r *http.Request, pluginID string) {
	var raw map[string]any
	dec := json.NewDecoder(io.LimitReader(r.Body, maxHostAPIBodyBytes))
	if err := dec.Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	updates := map[string]string{}
	if nested, ok := raw["secrets"].(map[string]any); ok {
		for k, v := range nested {
			if v == nil {
				updates[k] = ""
			} else if s, ok := v.(string); ok {
				updates[k] = s
			}
		}
	} else if key, ok := raw["key"].(string); ok && strings.TrimSpace(key) != "" {
		if raw["value"] == nil {
			updates[strings.TrimSpace(key)] = ""
		} else if s, ok := raw["value"].(string); ok {
			updates[strings.TrimSpace(key)] = s
		}
	} else {
		for k, v := range raw {
			if v == nil {
				updates[k] = ""
			} else if s, ok := v.(string); ok {
				updates[k] = s
			}
		}
	}

	if err := m.storage.SetSecrets(r.Context(), pluginID, updates); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to save secrets storage")
		return
	}
	secretSet, _, err := m.storage.GetMaskedSecrets(r.Context(), pluginID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to read secrets metadata")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"secrets_set": secretSet,
	})
}

func (m *Manager) handleHostSessionContext(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []any{}})
		return
	}
	q := r.URL.Query()
	sessionID := strings.TrimSpace(q.Get("session_id"))
	agentID := strings.TrimSpace(q.Get("agent_id"))

	if sessionID != "" {
		sess, err := m.lookupSession(r.Context(), agentID, sessionID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to query session")
			return
		}
		if sess == nil {
			writeJSONError(w, http.StatusNotFound, "Session not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"session": sanitizeSessionContext(sess),
		})
		return
	}

	rows, err := m.store.ListSessions(r.Context(), agentID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to list sessions")
		return
	}
	limit := 20
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, sanitizeSessionContext(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": out,
	})
}

func (m *Manager) lookupSession(ctx context.Context, agentID, sessionID string) (map[string]any, error) {
	if agentID != "" {
		return m.store.GetSession(ctx, agentID, sessionID)
	}
	return m.store.FindSessionByID(ctx, sessionID)
}

func sanitizeSessionContext(row map[string]any) map[string]any {
	return map[string]any{
		"session_id":    row["id"],
		"agent_id":      row["agent_id"],
		"title":         row["title"],
		"workspace":     row["workspace"],
		"connection_id": row["connection_id"],
		"status":        row["status"],
		"updated_at":    row["updated_at"],
	}
}

func extractHostToken(r *http.Request) string {
	if explicit := strings.TrimSpace(r.Header.Get("X-Astrorder-Host-Token")); explicit != "" {
		return explicit
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authz) > 7 && strings.EqualFold(authz[:7], "Bearer ") {
		return strings.TrimSpace(authz[7:])
	}
	return ""
}

func hasPermission(granted []string, perm string) bool {
	for _, p := range granted {
		if p == perm {
			return true
		}
	}
	return false
}
