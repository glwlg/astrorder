// Package backups manages versioned manual and scheduled backups from local,
// WSL, and SSH servers to user-specified local directories.
package backups

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	TriggerManual    = "manual"
	TriggerScheduled = "scheduled"

	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"

	ScheduleManual   = "manual"
	ScheduleInterval = "interval"
	ScheduleDaily    = "daily"
	ScheduleWeekly   = "weekly"

	DefaultRetentionCount = 20
	DefaultIntervalMins   = 60
	DefaultDailyTime      = "03:00"
	manifestFilename      = "backup-manifest.json"
)

// BrowserAuthorizer matches the shared browser credential guard used across Go routes.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// Opener reveals or opens a local directory using the desktop shell.
type Opener interface {
	Open(ctx context.Context, path, action string) error
}

type AgentRestarter interface {
	RestartEnvironmentAgent(ctx context.Context, environmentID, kind string) error
}

type RestoreAgentTarget struct {
	EnvironmentID string `json:"environment_id"`
	Kind          string `json:"kind"`
}

// Target describes a selectable source machine (local, SSH, or WSL).
type Target struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"` // local | ssh | wsl
	Name         string `json:"name"`
	State        string `json:"state"`
	Detail       string `json:"detail,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	ConnectionID string `json:"connection_id"`
}

// TargetsResponse is returned by GET /api/v1/backups/targets.
type TargetsResponse struct {
	Items                 []Target `json:"items"`
	DefaultDestinationDir string   `json:"default_destination_dir"`
}

// BackupJob represents a saved backup configuration and its schedule state.
type BackupJob struct {
	ID                      string              `json:"id"`
	Name                    string              `json:"name"`
	ConnectionID            string              `json:"connection_id"`
	SourcePath              string              `json:"source_path"`
	DestinationDir          string              `json:"destination_dir"`
	PostRestoreCommand      string              `json:"post_restore_command"`
	PostRestoreRestartAgent *RestoreAgentTarget `json:"post_restore_restart_agent,omitempty"`
	ScheduleEnabled         bool                `json:"schedule_enabled"`
	ScheduleMode            string              `json:"schedule_mode"`
	IntervalMinutes         int                 `json:"interval_minutes"`
	DailyTime               string              `json:"daily_time"`
	Weekday                 int                 `json:"weekday"`
	RetentionCount          int                 `json:"retention_count"`
	LastRunAt               *string             `json:"last_run_at"`
	LastStatus              *string             `json:"last_status"`
	LastError               *string             `json:"last_error"`
	NextRunAt               *string             `json:"next_run_at"`
	CreatedAt               string              `json:"created_at"`
	UpdatedAt               string              `json:"updated_at"`

	// Enriched fields populated for API responses.
	TargetName    string         `json:"target_name"`
	TargetKind    string         `json:"target_kind"`
	TargetState   string         `json:"target_state"`
	VersionCount  int            `json:"version_count"`
	LatestVersion *BackupVersion `json:"latest_version"`
	IsRunning     bool           `json:"is_running"`
}

// BackupVersion represents a single snapshot produced by a backup job.
type BackupVersion struct {
	ID             string  `json:"id"`
	JobID          string  `json:"job_id"`
	VersionNumber  int     `json:"version_number"`
	VersionLabel   string  `json:"version_label"`
	TriggerKind    string  `json:"trigger_kind"`
	Note           string  `json:"note"`
	Status         string  `json:"status"`
	ErrorDetail    *string `json:"error_detail"`
	LocalPath      string  `json:"local_path"`
	SourceKind     string  `json:"source_kind"` // file | directory | unknown
	FileCount      int     `json:"file_count"`
	TotalBytes     int64   `json:"total_bytes"`
	ManifestSHA256 *string `json:"manifest_sha256"`
	CreatedAt      string  `json:"created_at"`
	FinishedAt     *string `json:"finished_at"`
	ExistsOnDisk   bool    `json:"exists_on_disk"`
	ConnectionID   string  `json:"-"`
	SourcePath     string  `json:"-"`
}

// BackupManifest is written to backup-manifest.json inside each version directory.
type BackupManifest struct {
	VersionID        string   `json:"version_id"`
	VersionNumber    int      `json:"version_number"`
	VersionLabel     string   `json:"version_label"`
	JobID            string   `json:"job_id"`
	JobName          string   `json:"job_name"`
	ConnectionID     string   `json:"connection_id"`
	SourcePath       string   `json:"source_path"`
	SourceKind       string   `json:"source_kind"`
	TriggerKind      string   `json:"trigger_kind"`
	Note             string   `json:"note"`
	FileCount        int      `json:"file_count"`
	TotalBytes       int64    `json:"total_bytes"`
	SkippedLinks     []string `json:"skipped_links,omitempty"`
	SkippedSensitive []string `json:"skipped_sensitive,omitempty"` // Preserve when editing older manifests.
	ManifestSHA256   string   `json:"manifest_sha256"`
	CreatedAt        string   `json:"created_at"`
	FinishedAt       string   `json:"finished_at"`
}

// BrowseEntry represents one child directory or regular file returned when browsing a target.
type BrowseEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// BrowseResponse is returned by GET /api/v1/backups/browse.
type BrowseResponse struct {
	ConnectionID string        `json:"connection_id"`
	Root         string        `json:"root"`
	Parent       *string       `json:"parent"`
	Drives       []string      `json:"drives"`
	Items        []BrowseEntry `json:"items"`
}

// Options configures the backup Service.
type Options struct {
	Store                 *store.Store
	Authorizer            BrowserAuthorizer
	Resolver              *targetfs.Resolver
	Opener                Opener
	AgentRestarter        AgentRestarter
	DefaultWorkspace      string
	DefaultDestinationDir string
	WSLBinary             string
	Logger                *slog.Logger
	Now                   func() time.Time
	Location              *time.Location
	SchedulerTick         time.Duration
}

// Service implements backup job management, execution, version history, and scheduling.
type Service struct {
	store                 *store.Store
	authorizer            BrowserAuthorizer
	resolver              *targetfs.Resolver
	opener                Opener
	agentRestarter        AgentRestarter
	defaultWorkspace      string
	defaultDestinationDir string
	wslBinary             string
	logger                *slog.Logger
	now                   func() time.Time
	location              *time.Location
	schedulerTick         time.Duration

	mu          sync.Mutex
	runningJobs map[string]string // jobID -> versionID
	runWG       sync.WaitGroup
}
