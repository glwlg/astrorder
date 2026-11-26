package gitlabci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Service provides self-hosted & cloud GitLab CI integration over HTTP and Agent API.
type Service struct {
	options        Options
	registry       *Registry
	defaultClient  *http.Client
	insecureClient *http.Client
}

func NewService(options Options) *Service {
	stdClient, insecureClient := newDefaultHTTPClients()
	return &Service{
		options:        options,
		registry:       NewRegistry(options.Store),
		defaultClient:  stdClient,
		insecureClient: insecureClient,
	}
}

// Matches reports whether method + path belong to /api/v1/gitlab/*.
func Matches(method, reqPath string) bool {
	clean := strings.TrimRight(reqPath, "/")
	if !strings.HasPrefix(clean, BasePath+"/") {
		return false
	}
	rest := strings.TrimPrefix(clean, BasePath+"/")
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		switch parts[0] {
		case "instances":
			return method == http.MethodGet || method == http.MethodPost
		case "pipelines":
			return method == http.MethodGet
		}
	case 2:
		switch parts[0] {
		case "instances":
			if parts[1] == "test" {
				return method == http.MethodPost
			}
			return method == http.MethodPatch || method == http.MethodDelete
		case "pipelines":
			return method == http.MethodGet
		}
	case 3:
		switch parts[0] {
		case "pipelines":
			return method == http.MethodPost && (parts[2] == "retry" || parts[2] == "cancel")
		case "jobs":
			if parts[2] == "trace" {
				return method == http.MethodGet
			}
			return method == http.MethodPost && (parts[2] == "retry" || parts[2] == "play")
		}
	}
	return false
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
	rest := strings.TrimPrefix(clean, BasePath+"/")
	parts := strings.Split(rest, "/")

	switch len(parts) {
	case 1:
		switch parts[0] {
		case "instances":
			if r.Method == http.MethodGet {
				s.handleListInstances(w, r)
				return
			}
			if r.Method == http.MethodPost {
				s.handleCreateInstance(w, r)
				return
			}
		case "pipelines":
			s.handleListPipelines(w, r)
			return
		}
	case 2:
		switch parts[0] {
		case "instances":
			if parts[1] == "test" && r.Method == http.MethodPost {
				s.handleTestInstance(w, r)
				return
			}
			id, _ := url.PathUnescape(parts[1])
			if r.Method == http.MethodPatch {
				s.handleUpdateInstance(w, r, id)
				return
			}
			if r.Method == http.MethodDelete {
				s.handleDeleteInstance(w, r, id)
				return
			}
		case "pipelines":
			pid, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil || pid <= 0 {
				writeJSONError(w, http.StatusBadRequest, "无效的 pipeline_id")
				return
			}
			s.handleGetPipeline(w, r, pid)
			return
		}
	case 3:
		switch parts[0] {
		case "pipelines":
			pid, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil || pid <= 0 {
				writeJSONError(w, http.StatusBadRequest, "无效的 pipeline_id")
				return
			}
			s.handlePipelineAction(w, r, pid, parts[2])
			return
		case "jobs":
			jid, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil || jid <= 0 {
				writeJSONError(w, http.StatusBadRequest, "无效的 job_id")
				return
			}
			if parts[2] == "trace" {
				s.handleJobTrace(w, r, jid)
				return
			}
			s.handleJobAction(w, r, jid, parts[2])
			return
		}
	}
	writeJSONError(w, http.StatusNotFound, "Not Found")
}

// ResolveTarget resolves the matching GitLab instance and repository info for a workspace or explicit project.
func (s *Service) ResolveTarget(ctx context.Context, target QueryTarget) (*Instance, *RepoInfo, bool, string, error) {
	var repo *RepoInfo
	if strings.TrimSpace(target.Workspace) != "" {
		detected, err := s.DetectWorkspaceRepo(ctx, target.Workspace, target.ConnectionID, target.SessionID)
		if err == nil && detected != nil {
			repo = detected
		}
	}

	explicitProject := cleanProjectPath(target.ProjectPath)
	if explicitProject != "" {
		if repo == nil {
			repo = &RepoInfo{ProjectPath: explicitProject}
		} else {
			repo.ProjectPath = explicitProject
		}
	}

	// Explicit instance ID or name overrides host auto-matching.
	if strings.TrimSpace(target.InstanceID) != "" {
		inst, err := s.registry.Find(ctx, target.InstanceID)
		if err != nil {
			return nil, repo, true, err.Error(), err
		}
		if repo == nil || repo.ProjectPath == "" {
			return inst, repo, true, "未指定或检测到 GitLab 项目路径（project_path）", nil
		}
		return inst, repo, true, "", nil
	}

	if repo != nil && repo.Host != "" {
		inst, hasAny, err := s.registry.MatchForHost(ctx, repo.Host, repo.Port)
		if err != nil {
			return nil, repo, hasAny, err.Error(), err
		}
		if !hasAny {
			return nil, repo, false, "尚未配置 GitLab 实例", nil
		}
		if inst == nil {
			return nil, repo, true, fmt.Sprintf("未找到与仓库主机 %s 匹配的 GitLab 实例配置", repo.Host), nil
		}
		return inst, repo, true, "", nil
	}

	// Explicit project without workspace remote -> use default configured instance.
	if repo != nil && repo.ProjectPath != "" {
		inst, hasAny, err := s.registry.DefaultInstance(ctx)
		if err != nil {
			return nil, repo, hasAny, err.Error(), err
		}
		if inst == nil {
			return nil, repo, false, "尚未配置 GitLab 实例", nil
		}
		return inst, repo, true, "", nil
	}

	_, hasAny, err := s.registry.DefaultInstance(ctx)
	if err != nil {
		return nil, nil, false, err.Error(), err
	}
	if !hasAny {
		return nil, nil, false, "尚未配置 GitLab 实例", nil
	}
	return nil, nil, true, "当前工作区未检测到 Git 远程仓库地址", nil
}

// QueryPipelines returns recent GitLab CI pipelines and picks the best matching latest pipeline.
func (s *Service) QueryPipelines(ctx context.Context, target QueryTarget) (*PipelinesResponse, error) {
	inst, repo, configured, reason, err := s.ResolveTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	if inst == nil || repo == nil || repo.ProjectPath == "" {
		return &PipelinesResponse{
			Configured: configured,
			Matched:    false,
			Reason:     reason,
			Repo:       repo,
			Pipelines:  []PipelineSummary{},
		}, nil
	}

	limit := target.Limit
	if limit <= 0 {
		limit = 10
	}
	refFilter := strings.TrimSpace(target.Ref)
	shaFilter := strings.TrimSpace(target.SHA)

	pipelines, err := s.fetchPipelines(ctx, *inst, repo.ProjectPath, refFilter, shaFilter, limit, repo.HeadSHA, repo.Branch)
	if err != nil {
		return nil, err
	}

	// Pick latest relevant pipeline: prefer HEAD SHA match, then current branch match, then newest overall.
	var latest *PipelineSummary
	for i := range pipelines {
		if pipelines[i].MatchesHead {
			cp := pipelines[i]
			latest = &cp
			break
		}
	}
	if latest == nil {
		for i := range pipelines {
			if pipelines[i].MatchesBranch {
				cp := pipelines[i]
				latest = &cp
				break
			}
		}
	}
	if latest == nil && len(pipelines) > 0 {
		cp := pipelines[0]
		latest = &cp
	}

	pub := inst.ToPublic()
	return &PipelinesResponse{
		Configured:     true,
		Matched:        true,
		Repo:           repo,
		Instance:       &pub,
		LatestPipeline: latest,
		Pipelines:      pipelines,
	}, nil
}

// QueryPipelineDetail returns a pipeline's stage and job breakdown. When pipelineID <= 0, inspects the latest pipeline.
func (s *Service) QueryPipelineDetail(ctx context.Context, target QueryTarget, pipelineID int64) (*PipelineDetailResponse, error) {
	inst, repo, _, reason, err := s.ResolveTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	if inst == nil || repo == nil || repo.ProjectPath == "" {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, nonEmpty(reason, "未匹配到 GitLab 项目"))
	}

	if pipelineID <= 0 {
		list, listErr := s.QueryPipelines(ctx, target)
		if listErr != nil {
			return nil, listErr
		}
		if list.LatestPipeline == nil {
			return nil, fmt.Errorf("%w: 项目 %s 暂无 CI 流水线记录", ErrNotFound, repo.ProjectPath)
		}
		pipelineID = list.LatestPipeline.ID
	}

	summary, stages, jobs, err := s.fetchPipelineDetail(ctx, *inst, repo.ProjectPath, pipelineID, repo.HeadSHA, repo.Branch)
	if err != nil {
		return nil, err
	}
	pub := inst.ToPublic()
	return &PipelineDetailResponse{
		Repo:     repo,
		Instance: &pub,
		Pipeline: *summary,
		Stages:   stages,
		Jobs:     jobs,
	}, nil
}

// QueryJobTrace reads the tail of a GitLab CI job's log.
func (s *Service) QueryJobTrace(ctx context.Context, target QueryTarget, jobID int64, tailLines int, stripANSI bool) (*JobTraceResponse, error) {
	if jobID <= 0 {
		return nil, fmt.Errorf("%w: job_id 必须为正整数", ErrInvalid)
	}
	inst, repo, _, reason, err := s.ResolveTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	if inst == nil || repo == nil || repo.ProjectPath == "" {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, nonEmpty(reason, "未匹配到 GitLab 项目"))
	}
	return s.fetchJobTrace(ctx, *inst, repo.ProjectPath, jobID, tailLines, stripANSI)
}

func (s *Service) handleListInstances(w http.ResponseWriter, r *http.Request) {
	items, err := s.registry.ListPublic(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	var req CreateInstanceRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	created, err := s.registry.Create(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"instance": created.ToPublic()})
}

func (s *Service) handleUpdateInstance(w http.ResponseWriter, r *http.Request, id string) {
	var req UpdateInstanceRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	updated, err := s.registry.Update(r.Context(), id, req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": updated.ToPublic()})
}

func (s *Service) handleDeleteInstance(w http.ResponseWriter, r *http.Request, id string) {
	removed, err := s.registry.Delete(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": removed.ID})
}

func (s *Service) handleTestInstance(w http.ResponseWriter, r *http.Request) {
	var req TestInstanceRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的 JSON 请求体")
		return
	}
	var inst Instance
	if strings.TrimSpace(req.InstanceID) != "" {
		found, err := s.registry.Find(r.Context(), req.InstanceID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		inst = *found
	}
	if strings.TrimSpace(req.BaseURL) != "" {
		norm, err := normalizeBaseURL(req.BaseURL)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		inst.BaseURL = norm
	}
	if strings.TrimSpace(req.Token) != "" {
		inst.Token = strings.TrimSpace(req.Token)
	}
	if req.InsecureSkipVerify != nil {
		inst.InsecureSkipVerify = *req.InsecureSkipVerify
	}
	if inst.BaseURL == "" {
		writeJSONError(w, http.StatusBadRequest, "请填写 GitLab 服务地址")
		return
	}
	if inst.Token == "" {
		writeJSONError(w, http.StatusBadRequest, "请填写 GitLab Personal Access Token")
		return
	}
	resp, err := s.TestConnection(r.Context(), inst)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func queryTargetFromRequest(r *http.Request) QueryTarget {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	return QueryTarget{
		Workspace:    q.Get("workspace"),
		ConnectionID: q.Get("connection_id"),
		SessionID:    q.Get("session_id"),
		InstanceID:   q.Get("instance_id"),
		ProjectPath:  q.Get("project"),
		Ref:          nonEmpty(q.Get("ref"), q.Get("branch")),
		SHA:          q.Get("sha"),
		Limit:        limit,
	}
}

func (s *Service) handleListPipelines(w http.ResponseWriter, r *http.Request) {
	resp, err := s.QueryPipelines(r.Context(), queryTargetFromRequest(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleGetPipeline(w http.ResponseWriter, r *http.Request, pipelineID int64) {
	resp, err := s.QueryPipelineDetail(r.Context(), queryTargetFromRequest(r), pipelineID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleJobTrace(w http.ResponseWriter, r *http.Request, jobID int64) {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	resp, err := s.QueryJobTrace(r.Context(), queryTargetFromRequest(r), jobID, tail, true)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handlePipelineAction(w http.ResponseWriter, r *http.Request, pipelineID int64, action string) {
	target := queryTargetFromRequest(r)
	var body struct {
		Workspace    string `json:"workspace"`
		ConnectionID string `json:"connection_id"`
		SessionID    string `json:"session_id"`
		InstanceID   string `json:"instance_id"`
		Project      string `json:"project"`
	}
	if r.Body != nil {
		_ = decodeJSONBody(w, r, &body)
		if body.Workspace != "" {
			target.Workspace = body.Workspace
		}
		if body.ConnectionID != "" {
			target.ConnectionID = body.ConnectionID
		}
		if body.SessionID != "" {
			target.SessionID = body.SessionID
		}
		if body.InstanceID != "" {
			target.InstanceID = body.InstanceID
		}
		if body.Project != "" {
			target.ProjectPath = body.Project
		}
	}
	inst, repo, _, reason, err := s.ResolveTarget(r.Context(), target)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if inst == nil || repo == nil || repo.ProjectPath == "" {
		writeJSONError(w, http.StatusNotFound, nonEmpty(reason, "未匹配到 GitLab 项目"))
		return
	}
	summary, err := s.triggerPipelineAction(r.Context(), *inst, repo.ProjectPath, pipelineID, action)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pipeline": summary})
}

func (s *Service) handleJobAction(w http.ResponseWriter, r *http.Request, jobID int64, action string) {
	target := queryTargetFromRequest(r)
	var body struct {
		Workspace    string `json:"workspace"`
		ConnectionID string `json:"connection_id"`
		SessionID    string `json:"session_id"`
		InstanceID   string `json:"instance_id"`
		Project      string `json:"project"`
	}
	if r.Body != nil {
		_ = decodeJSONBody(w, r, &body)
		if body.Workspace != "" {
			target.Workspace = body.Workspace
		}
		if body.ConnectionID != "" {
			target.ConnectionID = body.ConnectionID
		}
		if body.SessionID != "" {
			target.SessionID = body.SessionID
		}
		if body.InstanceID != "" {
			target.InstanceID = body.InstanceID
		}
		if body.Project != "" {
			target.ProjectPath = body.Project
		}
	}
	inst, repo, _, reason, err := s.ResolveTarget(r.Context(), target)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if inst == nil || repo == nil || repo.ProjectPath == "" {
		writeJSONError(w, http.StatusNotFound, nonEmpty(reason, "未匹配到 GitLab 项目"))
		return
	}
	summary, err := s.triggerJobAction(r.Context(), *inst, repo.ProjectPath, jobID, action)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": summary})
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
		writeJSONError(w, http.StatusBadGateway, err.Error())
	}
}
