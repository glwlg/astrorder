package capabilities

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/envtarget"
)

const maxBody = 1 << 20

// BrowserAuthorizer preserves the shared browser token and Origin policy.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type handler struct {
	svc  *Service
	auth BrowserAuthorizer
}

// NewHandler serves /capabilities, /skills and /rtk.
func NewHandler(svc *Service, authorizer BrowserAuthorizer) http.Handler {
	return &handler{svc: svc, auth: authorizer}
}

const (
	capPrefix    = "/api/v1/capabilities/"
	skillsPrefix = "/api/v1/skills/"
	rtkPrefix    = "/api/v1/rtk/"
)

type route struct {
	method string
	path   string // exact path, or prefix when ending in "/*"
}

var routes = []route{
	{http.MethodGet, capPrefix + "targets"},
	{http.MethodGet, capPrefix + "summary"},
	{http.MethodPost, capPrefix + "mcp/update"},
	{http.MethodPost, capPrefix + "mcp/ping"},
	{http.MethodPost, capPrefix + "plugins/update"},
	{http.MethodPost, capPrefix + "marketplaces/update"},
	{http.MethodPost, capPrefix + "dispatch"},
	{http.MethodGet, skillsPrefix + "targets"},
	{http.MethodGet, skillsPrefix + "search"},
	{http.MethodGet, skillsPrefix + "target/*"},
	{http.MethodPost, skillsPrefix + "install"},
	{http.MethodPost, skillsPrefix + "remove"},
	{http.MethodGet, rtkPrefix + "*"},
	{http.MethodPost, rtkPrefix + "*"},
	{http.MethodPut, rtkPrefix + "*"},
}

// Matches reports whether this package owns the route.
func Matches(method, path string) bool {
	path = strings.TrimRight(path, "/")
	for _, r := range routes {
		if r.method != method {
			continue
		}
		if prefix, ok := strings.CutSuffix(r.path, "*"); ok {
			if strings.HasPrefix(path, prefix) && len(path) > len(prefix) {
				return true
			}
		} else if path == r.path {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	status, detail := statusOf(err)
	writeJSON(w, status, map[string]string{"detail": detail})
}

func decode(r *http.Request, into any) error {
	return json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(into)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.auth != nil {
		if failure := h.auth.AuthorizeBrowser(r); failure != nil {
			writeJSON(w, failure.Status, map[string]string{"detail": failure.Detail})
			return
		}
	}
	path := strings.TrimRight(r.URL.Path, "/")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	switch {
	case strings.HasPrefix(path, capPrefix):
		h.capabilities(ctx, w, r, strings.TrimPrefix(path, capPrefix))
	case strings.HasPrefix(path, skillsPrefix):
		h.skills(ctx, w, r, strings.TrimPrefix(path, skillsPrefix))
	case strings.HasPrefix(path, rtkPrefix):
		h.rtk(ctx, w, r, strings.TrimPrefix(path, rtkPrefix))
	default:
		http.NotFound(w, r)
	}
}

func invalid(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "请求内容无效"})
}

func (h *handler) capabilities(ctx context.Context, w http.ResponseWriter, r *http.Request, tail string) {
	switch tail {
	case "targets":
		targets, err := h.svc.TargetList(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, nonNil(targets))
	case "summary":
		summary, err := h.svc.Summary(ctx, orLocal(r.URL.Query().Get("target_id")), r.URL.Query().Get("agent"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, summary)
	case "mcp/update":
		var body struct {
			TargetID string         `json:"target_id"`
			Agent    string         `json:"agent"`
			Action   string         `json:"action"`
			Server   map[string]any `json:"server"`
		}
		if decode(r, &body) != nil || body.Server == nil {
			invalid(w)
			return
		}
		result, err := h.svc.UpdateMCP(ctx, orLocal(body.TargetID), body.Agent, body.Action, body.Server)
		reply(w, result, err)
	case "mcp/ping":
		var body struct {
			TargetID string         `json:"target_id"`
			Server   map[string]any `json:"server"`
		}
		if decode(r, &body) != nil || body.Server == nil {
			invalid(w)
			return
		}
		result, err := h.svc.Ping(ctx, orLocal(body.TargetID), body.Server)
		reply(w, result, err)
	case "plugins/update":
		var body struct {
			TargetID   string `json:"target_id"`
			Agent      string `json:"agent"`
			Action     string `json:"action"`
			PluginName string `json:"plugin_name"`
			Enabled    *bool  `json:"enabled"`
		}
		if decode(r, &body) != nil {
			invalid(w)
			return
		}
		enabled := body.Enabled == nil || *body.Enabled
		result, err := h.svc.UpdatePlugin(ctx, orLocal(body.TargetID), body.Agent, body.Action, body.PluginName, enabled)
		reply(w, result, err)
	case "marketplaces/update":
		var body struct {
			TargetID string         `json:"target_id"`
			Agent    string         `json:"agent"`
			Action   string         `json:"action"`
			Market   map[string]any `json:"market"`
		}
		if decode(r, &body) != nil || body.Market == nil {
			invalid(w)
			return
		}
		result, err := h.svc.UpdateMarketplace(ctx, orLocal(body.TargetID), body.Agent, body.Action, body.Market)
		reply(w, result, err)
	case "dispatch":
		var body struct {
			Type         string           `json:"capability_type"`
			SourceTarget string           `json:"source_target_id"`
			SourceAgent  string           `json:"source_agent"`
			ItemID       string           `json:"item_id"`
			Matrix       []DispatchTarget `json:"target_matrix"`
		}
		if decode(r, &body) != nil {
			invalid(w)
			return
		}
		result, err := h.svc.Dispatch(ctx, body.Type, orLocal(body.SourceTarget), body.SourceAgent, body.ItemID, body.Matrix)
		reply(w, result, err)
	default:
		http.NotFound(w, r)
	}
}

func orLocal(id string) string {
	if strings.TrimSpace(id) == "" {
		return "local"
	}
	return id
}

func reply[T any](w http.ResponseWriter, result *T, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) skills(ctx context.Context, w http.ResponseWriter, r *http.Request, tail string) {
	switch {
	case tail == "targets":
		rows, err := h.svc.SkillTargets(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"targets": rows})
	case tail == "search":
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if query == "" {
			query = "agent"
		}
		if len(query) > 100 {
			invalid(w)
			return
		}
		writeJSON(w, http.StatusOK, h.svc.Search(ctx, query))
	case strings.HasPrefix(tail, "target/"):
		id := strings.TrimPrefix(tail, "target/")
		skills, err := h.svc.TargetSkillList(ctx, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"target_id": id, "skills": skills, "count": len(skills)})
	case tail == "install":
		var body struct {
			TargetIDs []string `json:"target_ids"`
			Package   string   `json:"package_source"`
			Skill     string   `json:"skill_name"`
		}
		if decode(r, &body) != nil || len(body.TargetIDs) == 0 || len(body.TargetIDs) > 64 {
			invalid(w)
			return
		}
		results := make([]SkillInstallResult, 0, len(body.TargetIDs))
		for _, id := range body.TargetIDs {
			results = append(results, h.svc.InstallSkill(ctx, id, strings.TrimSpace(body.Package), strings.TrimSpace(body.Skill)))
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	case tail == "remove":
		var body struct {
			TargetID string `json:"target_id"`
			Skill    string `json:"skill_name"`
		}
		if decode(r, &body) != nil {
			invalid(w)
			return
		}
		result, err := h.svc.RemoveSkill(ctx, orLocal(body.TargetID), strings.TrimSpace(body.Skill))
		reply(w, result, err)
	default:
		http.NotFound(w, r)
	}
}

func (h *handler) rtk(ctx context.Context, w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	id := parts[0]
	days := r.URL.Query().Get("days")
	if days == "" {
		days = "30"
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		status, err := h.svc.RTK(ctx, id, days)
		reply(w, status, err)
	case len(parts) == 2 && parts[1] == "install" && r.Method == http.MethodPost:
		status, err := h.svc.InstallRTK(ctx, id, days)
		reply(w, status, err)
	case len(parts) == 3 && parts[1] == "agents" && r.Method == http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if decode(r, &body) != nil || body.Enabled == nil {
			invalid(w)
			return
		}
		status, err := h.svc.SetRTKAgent(ctx, id, parts[2], *body.Enabled, days)
		reply(w, status, err)
	default:
		http.NotFound(w, r)
	}
}

var _ = envtarget.ErrNotExist
