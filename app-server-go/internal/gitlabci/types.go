package gitlabci

import (
	"errors"
	"net/http"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	BasePath          = "/api/v1/gitlab"
	PrefInstancesKey  = "integrations:gitlab_instances"
	maxJSONBodySize   = 1 << 20
	maxTraceReadBytes = 2 << 20 // 2 MiB cap for raw job logs
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("conflict")
)

// BrowserAuthorizer authorizes browser HTTP requests.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// Options configures the GitLab CI integration service.
type Options struct {
	Store      *store.Store
	Authorizer BrowserAuthorizer
	Resolver   *targetfs.Resolver
	HTTPClient *http.Client
}

// Instance is one configured GitLab server (self-hosted or gitlab.com).
type Instance struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	BaseURL            string `json:"base_url"`
	Token              string `json:"token,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
	Enabled            bool   `json:"enabled"`
	IsDefault          bool   `json:"is_default,omitempty"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// PublicInstance is the secret-masked wire representation returned to browsers & agents.
type PublicInstance struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	BaseURL            string `json:"base_url"`
	Host               string `json:"host"`
	TokenSet           bool   `json:"token_set"`
	MaskedToken        string `json:"masked_token,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
	Enabled            bool   `json:"enabled"`
	IsDefault          bool   `json:"is_default,omitempty"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// CreateInstanceRequest creates a GitLab server configuration.
type CreateInstanceRequest struct {
	Name               string `json:"name"`
	BaseURL            string `json:"base_url"`
	Token              string `json:"token"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
	Enabled            *bool  `json:"enabled,omitempty"`
	IsDefault          bool   `json:"is_default"`
}

// UpdateInstanceRequest updates a GitLab server configuration.
type UpdateInstanceRequest struct {
	Name               *string `json:"name,omitempty"`
	BaseURL            *string `json:"base_url,omitempty"`
	Token              *string `json:"token,omitempty"`
	InsecureSkipVerify *bool   `json:"insecure_skip_verify,omitempty"`
	Enabled            *bool   `json:"enabled,omitempty"`
	IsDefault          *bool   `json:"is_default,omitempty"`
}

// TestInstanceRequest tests connectivity against an existing or unsaved GitLab server.
type TestInstanceRequest struct {
	InstanceID         string `json:"instance_id,omitempty"`
	BaseURL            string `json:"base_url,omitempty"`
	Token              string `json:"token,omitempty"`
	InsecureSkipVerify *bool  `json:"insecure_skip_verify,omitempty"`
}

// TestInstanceResponse reports GitLab connection verification status.
type TestInstanceResponse struct {
	OK       bool   `json:"ok"`
	Username string `json:"username,omitempty"`
	Name     string `json:"name,omitempty"`
	Version  string `json:"version,omitempty"`
	Message  string `json:"message,omitempty"`
}

// RepoInfo describes the GitLab repository inferred from a workspace's .git metadata.
type RepoInfo struct {
	Host        string `json:"host"`
	Port        string `json:"port,omitempty"`
	ProjectPath string `json:"project_path"`
	Branch      string `json:"branch,omitempty"`
	HeadSHA     string `json:"head_sha,omitempty"`
	RemoteName  string `json:"remote_name,omitempty"`
	RemoteURL   string `json:"remote_url,omitempty"`
}

// PipelineSummary is a concise GitLab CI pipeline record.
type PipelineSummary struct {
	ID            int64   `json:"id"`
	IID           int64   `json:"iid,omitempty"`
	Status        string  `json:"status"`
	Source        string  `json:"source,omitempty"`
	Ref           string  `json:"ref"`
	SHA           string  `json:"sha"`
	ShortSHA      string  `json:"short_sha"`
	WebURL        string  `json:"web_url"`
	CreatedAt     string  `json:"created_at,omitempty"`
	UpdatedAt     string  `json:"updated_at,omitempty"`
	StartedAt     string  `json:"started_at,omitempty"`
	FinishedAt    string  `json:"finished_at,omitempty"`
	Duration      float64 `json:"duration,omitempty"`
	CommitTitle   string  `json:"commit_title,omitempty"`
	AuthorName    string  `json:"author_name,omitempty"`
	MatchesHead   bool    `json:"matches_head,omitempty"`
	MatchesBranch bool    `json:"matches_branch,omitempty"`
}

// JobSummary is one job or trigger bridge inside a GitLab pipeline.
type JobSummary struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Stage         string  `json:"stage"`
	Status        string  `json:"status"`
	WebURL        string  `json:"web_url,omitempty"`
	Duration      float64 `json:"duration,omitempty"`
	AllowFailure  bool    `json:"allow_failure,omitempty"`
	FailureReason string  `json:"failure_reason,omitempty"`
	RunnerName    string  `json:"runner_name,omitempty"`
	CreatedAt     string  `json:"created_at,omitempty"`
	StartedAt     string  `json:"started_at,omitempty"`
	FinishedAt    string  `json:"finished_at,omitempty"`
	IsBridge      bool    `json:"is_bridge,omitempty"`
}

// StageGroup aggregates jobs belonging to one pipeline stage in execution order.
type StageGroup struct {
	Name   string       `json:"name"`
	Status string       `json:"status"`
	Jobs   []JobSummary `json:"jobs"`
}

// PipelinesResponse returns detected GitLab repo + recent CI pipelines for a workspace/project.
type PipelinesResponse struct {
	Configured     bool              `json:"configured"`
	Matched        bool              `json:"matched"`
	Reason         string            `json:"reason,omitempty"`
	Repo           *RepoInfo         `json:"repo,omitempty"`
	Instance       *PublicInstance   `json:"instance,omitempty"`
	LatestPipeline *PipelineSummary  `json:"latest_pipeline,omitempty"`
	Pipelines      []PipelineSummary `json:"pipelines"`
}

// PipelineDetailResponse returns one pipeline with its stages and jobs.
type PipelineDetailResponse struct {
	Repo     *RepoInfo       `json:"repo,omitempty"`
	Instance *PublicInstance `json:"instance,omitempty"`
	Pipeline PipelineSummary `json:"pipeline"`
	Stages   []StageGroup    `json:"stages"`
	Jobs     []JobSummary    `json:"jobs"`
}

// JobTraceResponse returns bounded job log text.
type JobTraceResponse struct {
	JobID      int64  `json:"job_id"`
	TailLines  int    `json:"tail_lines"`
	TotalLines int    `json:"total_lines"`
	Truncated  bool   `json:"truncated"`
	Trace      string `json:"trace"`
}

// QueryTarget identifies which GitLab project/instance to query (from workspace or explicit override).
type QueryTarget struct {
	Workspace    string
	ConnectionID string
	SessionID    string
	InstanceID   string
	ProjectPath  string
	Ref          string
	SHA          string
	Limit        int
}
