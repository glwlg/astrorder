package gitlabci

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\r`)

func (s *Service) httpClientFor(inst Instance) *http.Client {
	if s.options.HTTPClient != nil {
		return s.options.HTTPClient
	}
	if !inst.InsecureSkipVerify {
		return s.defaultClient
	}
	return s.insecureClient
}

func newDefaultHTTPClients() (standard, insecure *http.Client) {
	standard = &http.Client{Timeout: 20 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user opt-in for self-hosted internal GitLab certs
	insecure = &http.Client{Timeout: 20 * time.Second, Transport: tr}
	return standard, insecure
}

func (s *Service) doGitLabJSON(ctx context.Context, inst Instance, method, apiPath string, query url.Values, out any) (int, error) {
	fullURL := strings.TrimRight(inst.BaseURL, "/") + "/api/v4" + apiPath
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(inst.Token) != "" {
		req.Header.Set("PRIVATE-TOKEN", strings.TrimSpace(inst.Token))
	}

	resp, err := s.httpClientFor(inst).Do(req)
	if err != nil {
		return 0, fmt.Errorf("连接 GitLab (%s) 失败: %w", inst.BaseURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("读取 GitLab 响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, formatGitLabAPIError(resp.StatusCode, body)
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return resp.StatusCode, fmt.Errorf("解析 GitLab JSON 响应失败: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func (s *Service) doGitLabText(ctx context.Context, inst Instance, apiPath string) (string, int, error) {
	fullURL := strings.TrimRight(inst.BaseURL, "/") + "/api/v4" + apiPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return "", 0, err
	}
	if strings.TrimSpace(inst.Token) != "" {
		req.Header.Set("PRIVATE-TOKEN", strings.TrimSpace(inst.Token))
	}
	resp, err := s.httpClientFor(inst).Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("连接 GitLab (%s) 失败: %w", inst.BaseURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTraceReadBytes+1))
	if err != nil {
		return "", resp.StatusCode, fmt.Errorf("读取 Job 日志失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", resp.StatusCode, formatGitLabAPIError(resp.StatusCode, data)
	}
	if len(data) > maxTraceReadBytes {
		data = data[len(data)-maxTraceReadBytes:]
	}
	return string(data), resp.StatusCode, nil
}

func formatGitLabAPIError(status int, body []byte) error {
	var parsed struct {
		Message any    `json:"message"`
		Error   string `json:"error"`
	}
	detail := ""
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Error != "" {
			detail = parsed.Error
		} else if parsed.Message != nil {
			switch m := parsed.Message.(type) {
			case string:
				detail = m
			default:
				raw, _ := json.Marshal(m)
				detail = string(raw)
			}
		}
	}
	if detail == "" {
		detail = strings.TrimSpace(string(body))
		if len(detail) > 200 {
			detail = detail[:200] + "…"
		}
	}
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("GitLab 认证失败（401）：请检查 Personal Access Token 是否有效或已过期（%s）", detail)
	case http.StatusForbidden:
		return fmt.Errorf("GitLab 权限不足（403）：请确认 Token 具备 read_api / api 权限（%s）", detail)
	case http.StatusNotFound:
		return fmt.Errorf("%w: GitLab 未找到对应项目或流水线（%s）", ErrNotFound, detail)
	default:
		return fmt.Errorf("GitLab API 返回 %d: %s", status, detail)
	}
}

// TestConnection verifies connectivity and token validity against a GitLab instance.
func (s *Service) TestConnection(ctx context.Context, inst Instance) (*TestInstanceResponse, error) {
	var user struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if _, err := s.doGitLabJSON(ctx, inst, http.MethodGet, "/user", nil, &user); err != nil {
		return &TestInstanceResponse{OK: false, Message: err.Error()}, nil
	}
	var ver struct {
		Version string `json:"version"`
	}
	_, _ = s.doGitLabJSON(ctx, inst, http.MethodGet, "/version", nil, &ver)
	msg := fmt.Sprintf("连接成功（用户：%s）", nonEmpty(user.Username, user.Name))
	if ver.Version != "" {
		msg = fmt.Sprintf("连接成功（GitLab %s · 用户：%s）", ver.Version, nonEmpty(user.Username, user.Name))
	}
	return &TestInstanceResponse{
		OK:       true,
		Username: user.Username,
		Name:     user.Name,
		Version:  ver.Version,
		Message:  msg,
	}, nil
}

type glPipelineWire struct {
	ID         int64   `json:"id"`
	IID        int64   `json:"iid"`
	Status     string  `json:"status"`
	Source     string  `json:"source"`
	Ref        string  `json:"ref"`
	SHA        string  `json:"sha"`
	WebURL     string  `json:"web_url"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	StartedAt  string  `json:"started_at"`
	FinishedAt string  `json:"finished_at"`
	Duration   float64 `json:"duration"`
	User       *struct {
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"user"`
}

func (p glPipelineWire) toSummary(headSHA, branch string) PipelineSummary {
	shortSHA := p.SHA
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	author := ""
	if p.User != nil {
		author = nonEmpty(p.User.Name, p.User.Username)
	}
	return PipelineSummary{
		ID:            p.ID,
		IID:           p.IID,
		Status:        p.Status,
		Source:        p.Source,
		Ref:           p.Ref,
		SHA:           p.SHA,
		ShortSHA:      shortSHA,
		WebURL:        p.WebURL,
		CreatedAt:     p.CreatedAt,
		UpdatedAt:     p.UpdatedAt,
		StartedAt:     p.StartedAt,
		FinishedAt:    p.FinishedAt,
		Duration:      p.Duration,
		AuthorName:    author,
		MatchesHead:   headSHA != "" && strings.EqualFold(p.SHA, headSHA),
		MatchesBranch: branch != "" && p.Ref == branch,
	}
}

type glJobWire struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Stage         string  `json:"stage"`
	Status        string  `json:"status"`
	WebURL        string  `json:"web_url"`
	Duration      float64 `json:"duration"`
	AllowFailure  bool    `json:"allow_failure"`
	FailureReason string  `json:"failure_reason"`
	CreatedAt     string  `json:"created_at"`
	StartedAt     string  `json:"started_at"`
	FinishedAt    string  `json:"finished_at"`
	Runner        *struct {
		Description string `json:"description"`
		Name        string `json:"name"`
	} `json:"runner"`
	Commit *struct {
		Title      string `json:"title"`
		AuthorName string `json:"author_name"`
	} `json:"commit"`
}

func (j glJobWire) toSummary(isBridge bool) JobSummary {
	runner := ""
	if j.Runner != nil {
		runner = nonEmpty(j.Runner.Description, j.Runner.Name)
	}
	return JobSummary{
		ID:            j.ID,
		Name:          j.Name,
		Stage:         j.Stage,
		Status:        j.Status,
		WebURL:        j.WebURL,
		Duration:      j.Duration,
		AllowFailure:  j.AllowFailure,
		FailureReason: j.FailureReason,
		RunnerName:    runner,
		CreatedAt:     j.CreatedAt,
		StartedAt:     j.StartedAt,
		FinishedAt:    j.FinishedAt,
		IsBridge:      isBridge,
	}
}

func (s *Service) fetchPipelines(ctx context.Context, inst Instance, projectPath, ref, sha string, limit int, headSHA, branch string) ([]PipelineSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 30 {
		limit = 30
	}
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(limit))
	if strings.TrimSpace(ref) != "" {
		q.Set("ref", strings.TrimSpace(ref))
	}
	if strings.TrimSpace(sha) != "" {
		q.Set("sha", strings.TrimSpace(sha))
	}
	apiPath := "/projects/" + url.PathEscape(projectPath) + "/pipelines"
	var raw []glPipelineWire
	if _, err := s.doGitLabJSON(ctx, inst, http.MethodGet, apiPath, q, &raw); err != nil {
		return nil, err
	}
	out := make([]PipelineSummary, 0, len(raw))
	for _, p := range raw {
		out = append(out, p.toSummary(headSHA, branch))
	}
	return out, nil
}

func (s *Service) fetchPipelineDetail(ctx context.Context, inst Instance, projectPath string, pipelineID int64, headSHA, branch string) (*PipelineSummary, []StageGroup, []JobSummary, error) {
	escapedProject := url.PathEscape(projectPath)
	var wire glPipelineWire
	if _, err := s.doGitLabJSON(ctx, inst, http.MethodGet, fmt.Sprintf("/projects/%s/pipelines/%d", escapedProject, pipelineID), nil, &wire); err != nil {
		return nil, nil, nil, err
	}
	summary := wire.toSummary(headSHA, branch)

	q := url.Values{}
	q.Set("per_page", "100")
	var rawJobs []glJobWire
	if _, err := s.doGitLabJSON(ctx, inst, http.MethodGet, fmt.Sprintf("/projects/%s/pipelines/%d/jobs", escapedProject, pipelineID), q, &rawJobs); err != nil {
		return nil, nil, nil, err
	}

	// Optional downstream trigger bridges (ignore 404 on older GitLab versions).
	var rawBridges []glJobWire
	if status, bErr := s.doGitLabJSON(ctx, inst, http.MethodGet, fmt.Sprintf("/projects/%s/pipelines/%d/bridges", escapedProject, pipelineID), q, &rawBridges); bErr != nil && status != http.StatusNotFound {
		rawBridges = nil
	}

	jobs := make([]JobSummary, 0, len(rawJobs)+len(rawBridges))
	for _, j := range rawJobs {
		if summary.CommitTitle == "" && j.Commit != nil && j.Commit.Title != "" {
			summary.CommitTitle = j.Commit.Title
		}
		if summary.AuthorName == "" && j.Commit != nil && j.Commit.AuthorName != "" {
			summary.AuthorName = j.Commit.AuthorName
		}
		jobs = append(jobs, j.toSummary(false))
	}
	for _, b := range rawBridges {
		jobs = append(jobs, b.toSummary(true))
	}

	stages := groupJobsByStage(jobs)
	return &summary, stages, jobs, nil
}

func (s *Service) fetchJobTrace(ctx context.Context, inst Instance, projectPath string, jobID int64, tailLines int, stripANSI bool) (*JobTraceResponse, error) {
	if tailLines <= 0 {
		tailLines = 200
	}
	if tailLines > 1000 {
		tailLines = 1000
	}
	apiPath := fmt.Sprintf("/projects/%s/jobs/%d/trace", url.PathEscape(projectPath), jobID)
	raw, _, err := s.doGitLabText(ctx, inst, apiPath)
	if err != nil {
		return nil, err
	}
	if stripANSI {
		raw = ansiEscapePattern.ReplaceAllString(raw, "")
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	// Drop single trailing empty split element when log ends with newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	truncated := false
	if total > tailLines {
		lines = lines[total-tailLines:]
		truncated = true
	}
	return &JobTraceResponse{
		JobID:      jobID,
		TailLines:  tailLines,
		TotalLines: total,
		Truncated:  truncated,
		Trace:      strings.Join(lines, "\n"),
	}, nil
}

func (s *Service) triggerPipelineAction(ctx context.Context, inst Instance, projectPath string, pipelineID int64, action string) (*PipelineSummary, error) {
	apiPath := fmt.Sprintf("/projects/%s/pipelines/%d/%s", url.PathEscape(projectPath), pipelineID, action)
	var wire glPipelineWire
	if _, err := s.doGitLabJSON(ctx, inst, http.MethodPost, apiPath, nil, &wire); err != nil {
		return nil, err
	}
	summary := wire.toSummary("", "")
	return &summary, nil
}

func (s *Service) triggerJobAction(ctx context.Context, inst Instance, projectPath string, jobID int64, action string) (*JobSummary, error) {
	apiPath := fmt.Sprintf("/projects/%s/jobs/%d/%s", url.PathEscape(projectPath), jobID, action)
	var wire glJobWire
	if _, err := s.doGitLabJSON(ctx, inst, http.MethodPost, apiPath, nil, &wire); err != nil {
		return nil, err
	}
	summary := wire.toSummary(false)
	return &summary, nil
}

func groupJobsByStage(jobs []JobSummary) []StageGroup {
	order := make([]string, 0, 6)
	byStage := map[string][]JobSummary{}
	for _, j := range jobs {
		stage := nonEmpty(j.Stage, "default")
		if _, exists := byStage[stage]; !exists {
			order = append(order, stage)
		}
		byStage[stage] = append(byStage[stage], j)
	}
	stages := make([]StageGroup, 0, len(order))
	for _, name := range order {
		stageJobs := byStage[name]
		stages = append(stages, StageGroup{
			Name:   name,
			Status: aggregateStageStatus(stageJobs),
			Jobs:   stageJobs,
		})
	}
	return stages
}

func aggregateStageStatus(jobs []JobSummary) string {
	if len(jobs) == 0 {
		return "unknown"
	}
	hasRunning := false
	hasPending := false
	hasFailed := false
	hasWarn := false
	hasManual := false
	hasCanceled := false
	allSkipped := true
	for _, j := range jobs {
		if j.Status != "skipped" {
			allSkipped = false
		}
		switch j.Status {
		case "running":
			hasRunning = true
		case "pending", "created", "preparing", "waiting_for_resource":
			hasPending = true
		case "failed":
			if j.AllowFailure {
				hasWarn = true
			} else {
				hasFailed = true
			}
		case "canceled", "canceling":
			hasCanceled = true
		case "manual":
			hasManual = true
		}
	}
	switch {
	case hasFailed:
		return "failed"
	case hasRunning:
		return "running"
	case hasPending:
		return "pending"
	case hasCanceled:
		return "canceled"
	case hasManual:
		return "manual"
	case allSkipped:
		return "skipped"
	case hasWarn:
		return "warning"
	default:
		return "success"
	}
}

func nonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
