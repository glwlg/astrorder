package bots

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Service owns Specialist Bot registry, workspace docs (AGENTS.md & .agents/skills),
// LLM optimization/distillation, and unified job dispatch.
type Service struct {
	options    Options
	registry   *Registry
	httpClient *http.Client
}

// NewService creates a Bots Service (also implements http.Handler).
func NewService(options Options) *Service {
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &Service{
		options:    options,
		registry:   NewRegistry(options.Store),
		httpClient: client,
	}
}

// Matches reports whether method + path belong to the Bots REST API.
func Matches(method, reqPath string) bool {
	clean := strings.TrimRight(reqPath, "/")
	if clean == BasePath {
		return method == http.MethodGet || method == http.MethodPost
	}
	if !strings.HasPrefix(clean, BasePath+"/") {
		return false
	}
	rest := strings.TrimPrefix(clean, BasePath+"/")
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		switch parts[0] {
		case "optimize", "distill":
			return method == http.MethodPost
		default:
			return method == http.MethodGet || method == http.MethodPatch || method == http.MethodDelete
		}
	case 2:
		switch parts[1] {
		case "docs":
			return method == http.MethodGet
		case "agents-md":
			return method == http.MethodPut
		case "invoke", "upload":
			return method == http.MethodPost
		}
	case 3:
		if parts[1] == "skills" && parts[2] != "" {
			return method == http.MethodPut
		}
	}
	return false
}

// ListSummaries returns all Bots enriched with AGENTS.md, skills, and live session metrics.
func (s *Service) ListSummaries(ctx context.Context) ([]BotSummary, error) {
	profiles, err := s.registry.List(ctx)
	if err != nil {
		return nil, err
	}
	var allSessions []map[string]any
	if s.options.Store != nil {
		if loaded, listErr := s.options.Store.ListSessions(ctx, ""); listErr == nil {
			allSessions = loaded
		}
	}

	summaries := make([]BotSummary, 0, len(profiles))
	for _, bot := range profiles {
		summaries = append(summaries, s.enrichProfile(ctx, bot, allSessions))
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].RunningCount != summaries[j].RunningCount {
			return summaries[i].RunningCount > summaries[j].RunningCount
		}
		return summaries[i].UpdatedAt > summaries[j].UpdatedAt
	})
	return summaries, nil
}

// GetSummary returns one enriched Bot by ID, slug, or name.
func (s *Service) GetSummary(ctx context.Context, ref string) (*BotSummary, error) {
	bot, err := s.registry.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	var allSessions []map[string]any
	if s.options.Store != nil {
		if loaded, listErr := s.options.Store.ListSessions(ctx, ""); listErr == nil {
			allSessions = loaded
		}
	}
	summary := s.enrichProfile(ctx, *bot, allSessions)
	return &summary, nil
}

func (s *Service) enrichProfile(ctx context.Context, bot BotProfile, allSessions []map[string]any) BotSummary {
	summary := BotSummary{
		BotProfile: bot,
		Skills:     []BotSkillSummary{},
	}
	if s.options.Resolver != nil && strings.TrimSpace(bot.Workspace) != "" {
		if _, content, exists, err := s.ReadAgentsMD(ctx, bot); err == nil && exists {
			summary.HasAgentsMD = true
			summary.AgentsMDExcerpt = excerptAgentsMD(content)
		}
		if skills, err := s.ListSkills(ctx, bot, false); err == nil {
			for _, sk := range skills {
				summary.Skills = append(summary.Skills, sk.BotSkillSummary)
			}
		}
	}
	botWSKey := NormalizeWorkspaceKey(bot.Workspace)
	botConn := normalizeConnectionID(bot.ConnectionID)
	for _, sess := range allSessions {
		ws, _ := sess["workspace"].(string)
		if NormalizeWorkspaceKey(ws) != botWSKey {
			continue
		}
		sessConn, _ := sess["connection_id"].(string)
		if normalizeConnectionID(sessConn) != botConn {
			continue
		}
		summary.SessionCount++
		status, _ := sess["status"].(string)
		if status == "running" || status == "waiting_approval" {
			summary.RunningCount++
		}
		updatedAt, _ := sess["updated_at"].(string)
		if updatedAt > summary.LastActiveAt {
			summary.LastActiveAt = updatedAt
		}
	}
	return summary
}

// ListForAgentAPI returns a compact Bot catalog tailored for astrorder-agent bots list.
func (s *Service) ListForAgentAPI(ctx context.Context) (map[string]any, error) {
	summaries, err := s.ListSummaries(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(summaries))
	for _, b := range summaries {
		skillNames := make([]string, 0, len(b.Skills))
		for _, sk := range b.Skills {
			skillNames = append(skillNames, sk.Slug)
		}
		items = append(items, map[string]any{
			"id":                    b.ID,
			"slug":                  b.Slug,
			"name":                  b.Name,
			"description":           b.Description,
			"workspace":             b.Workspace,
			"connection_id":         nonEmpty(b.ConnectionID, "local"),
			"default_agent_id":      b.DefaultAgentID,
			"default_provider":      b.DefaultProvider,
			"default_model":         b.DefaultModel,
			"default_effort":        b.DefaultEffort,
			"default_approval_mode": nonEmpty(b.DefaultApprovalMode, ApprovalModeFullAccess),
			"tags":                  b.Tags,
			"has_agents_md":         b.HasAgentsMD,
			"skills":                skillNames,
			"running_count":         b.RunningCount,
		})
	}
	return map[string]any{"items": items}, nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.options.Authorizer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := s.options.Authorizer.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return
	}

	clean := strings.TrimRight(r.URL.Path, "/")
	if clean == BasePath {
		switch r.Method {
		case http.MethodGet:
			s.handleList(w, r)
		case http.MethodPost:
			s.handleCreate(w, r)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
		return
	}

	rest := strings.TrimPrefix(clean, BasePath+"/")
	parts := strings.Split(rest, "/")
	if len(parts) == 1 {
		if parts[0] == "optimize" && r.Method == http.MethodPost {
			s.handleOptimize(w, r)
			return
		}
		if parts[0] == "distill" && r.Method == http.MethodPost {
			s.handleDistill(w, r)
			return
		}
		id, _ := url.PathUnescape(parts[0])
		switch r.Method {
		case http.MethodGet:
			s.handleGet(w, r, id)
		case http.MethodPatch:
			s.handleUpdate(w, r, id)
		case http.MethodDelete:
			s.handleDelete(w, r, id)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
		return
	}

	id, _ := url.PathUnescape(parts[0])
	if len(parts) == 2 {
		switch parts[1] {
		case "docs":
			s.handleGetDocs(w, r, id)
		case "agents-md":
			s.handlePutAgentsMD(w, r, id)
		case "invoke":
			s.handleInvoke(w, r, id)
		case "upload":
			s.handleUpload(w, r, id)
		default:
			writeJSONError(w, http.StatusNotFound, "Not Found")
		}
		return
	}

	if len(parts) == 3 && parts[1] == "skills" {
		skillSlug, _ := url.PathUnescape(parts[2])
		s.handlePutSkill(w, r, id, skillSlug)
		return
	}

	writeJSONError(w, http.StatusNotFound, "Not Found")
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	items, err := s.ListSummaries(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CreateBot registers a Bot and optionally initializes its workspace AGENTS.md and SKILL.md.
func (s *Service) CreateBot(ctx context.Context, req CreateBotRequest) (*BotSummary, []string, error) {
	bot, err := s.registry.Create(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	warnings := []string{}
	if req.WriteAgentsMD && strings.TrimSpace(req.AgentsMD) != "" {
		if _, writeErr := s.WriteAgentsMD(ctx, *bot, req.AgentsMD); writeErr != nil {
			warnings = append(warnings, "写入 AGENTS.md 失败: "+writeErr.Error())
		}
	}
	if req.WriteSkill && strings.TrimSpace(req.SkillMD) != "" {
		slug := nonEmpty(NormalizeSlug(req.SkillSlug), bot.Slug+"-workflow")
		if _, writeErr := s.WriteSkill(ctx, *bot, slug, req.SkillMD); writeErr != nil {
			warnings = append(warnings, "写入 Skill 失败: "+writeErr.Error())
		}
	}

	summary, sumErr := s.GetSummary(ctx, bot.ID)
	if sumErr != nil {
		fallback := BotSummary{BotProfile: *bot, Skills: []BotSkillSummary{}}
		return &fallback, warnings, nil
	}
	return summary, warnings, nil
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateBotRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	summary, warnings, err := s.CreateBot(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resp := map[string]any{"bot": summary}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request, id string) {
	summary, err := s.GetSummary(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot": summary})
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request, id string) {
	var req UpdateBotRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	bot, err := s.registry.Update(r.Context(), id, req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	summary, err := s.GetSummary(r.Context(), bot.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"bot": bot})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot": summary})
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request, id string) {
	removed, err := s.registry.Delete(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"deleted": removed.ID,
		"message": "已取消 Bot 注册，工作区内的 AGENTS.md 与 Skills 文件均已保留。",
	})
}

func (s *Service) handleGetDocs(w http.ResponseWriter, r *http.Request, id string) {
	bot, err := s.registry.Find(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	agentsPath, agentsMD, exists, err := s.ReadAgentsMD(r.Context(), *bot)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	skills, err := s.ListSkills(r.Context(), *bot, true)
	if err != nil {
		skills = []BotSkillDoc{}
	}
	writeJSON(w, http.StatusOK, BotDocsResponse{
		Bot:              *bot,
		AgentsMDPath:     agentsPath,
		AgentsMDExists:   exists,
		AgentsMD:         agentsMD,
		Skills:           skills,
		ReferencedSkills: extractReferencedExternalSkills(agentsMD, skills),
	})
}

func (s *Service) handlePutAgentsMD(w http.ResponseWriter, r *http.Request, id string) {
	bot, err := s.registry.Find(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	var req SaveAgentsMDRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	filePath, err := s.WriteAgentsMD(r.Context(), *bot, req.Content)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"path":    filePath,
		"content": req.Content,
	})
}

func (s *Service) handlePutSkill(w http.ResponseWriter, r *http.Request, id, skillSlug string) {
	bot, err := s.registry.Find(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	var req SaveSkillRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	doc, err := s.WriteSkill(r.Context(), *bot, skillSlug, req.Content)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"skill": doc,
	})
}

func (s *Service) handleOptimize(w http.ResponseWriter, r *http.Request) {
	var req OptimizeRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	resp, err := s.Optimize(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleDistill(w http.ResponseWriter, r *http.Request) {
	var req DistillRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	resp, err := s.Distill(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleInvoke(w http.ResponseWriter, r *http.Request, id string) {
	var req InvokeRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	req.Bot = id
	resp, err := s.Invoke(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request, id string) {
	bot, err := s.registry.Find(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	const maxUploadTotalBytes = 64 << 20 // 64 MiB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadTotalBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "解析上传文件失败（单次总大小上限 64MB）: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	var inputs []UploadInput
	if r.MultipartForm != nil {
		for _, fieldName := range []string{"files", "file"} {
			for _, fh := range r.MultipartForm.File[fieldName] {
				f, openErr := fh.Open()
				if openErr != nil {
					writeJSONError(w, http.StatusBadRequest, "读取上传文件失败: "+openErr.Error())
					return
				}
				data, readErr := readBoundedAttachment(f, maxUploadTotalBytes)
				_ = f.Close()
				if readErr != nil {
					writeJSONError(w, http.StatusBadRequest, "上传文件超过限制: "+readErr.Error())
					return
				}
				inputs = append(inputs, UploadInput{Name: fh.Filename, Data: data})
			}
		}
	}
	uploaded, err := s.StageUploadedFilesToInbox(r.Context(), *bot, inputs)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"files": uploaded,
	})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, out any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodySize))
	return dec.Decode(out)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrConflict):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}
