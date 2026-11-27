package learning

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/bots"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	BasePath                  = "/api/v1/learning"
	PrefConfigKey             = "learning:config:v1"
	PrefTopicsKey             = "learning:topics:v1"
	PrefRunsKey               = "learning:runs:v1"
	DefaultLookbackDays       = 7
	DefaultMisfireCatchupHrs  = 48
	DefaultScanTimeout        = 25 * time.Minute
	maxJSONBodyBytes          = 4 << 20
	maxStoredTopics           = 500
	maxStoredRuns             = 50
	defaultMentorBotSlug      = "learning-mentor"
	defaultMentorBotName      = "成长导师"
	defaultMentorSkillSlug    = "learning-radar-mentor"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("conflict")
)

// BrowserAuthorizer validates browser/desktop HTTP requests.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// Publisher broadcasts real-time SSE events via EventHub.
type Publisher interface {
	Publish(ctx context.Context, eventType string, data map[string]any) error
}

// SessionHistoryReader retrieves recent projected transcript messages for a session.
type SessionHistoryReader func(ctx context.Context, agentID, sessionID string, limit int) ([]map[string]any, error)

// BotManager abstracts the bots.Service methods needed by Learning Radar.
type BotManager interface {
	ListSummaries(ctx context.Context) ([]bots.BotSummary, error)
	GetSummary(ctx context.Context, ref string) (*bots.BotSummary, error)
	CreateBot(ctx context.Context, req bots.CreateBotRequest) (*bots.BotSummary, []string, error)
	Invoke(ctx context.Context, req bots.InvokeRequest) (*bots.InvokeResponse, error)
}

// ExternalFailureSignal is a normalized failure signal contributed by an enabled extension/plugin.
type ExternalFailureSignal struct {
	Source    string
	Title     string
	Detail    string
	Timestamp string
}

// ExternalFailureSignalProvider optionally collects workspace failure signals from enabled plugins.
type ExternalFailureSignalProvider interface {
	CollectFailureSignals(ctx context.Context, workspace, connectionID, sessionID string, limit int) []ExternalFailureSignal
}

// Options configures the Learning Radar service.
type Options struct {
	Store            *store.Store
	Authorizer       BrowserAuthorizer
	Resolver         *targetfs.Resolver
	Publisher        Publisher
	HistoryReader    SessionHistoryReader
	Bots             BotManager
	FailureSignals   ExternalFailureSignalProvider
	WakeSchedules    func()
	DefaultWorkspace string
	Logger           *slog.Logger
	Now              func() time.Time
	Location         *time.Location
	SchedulerTick    time.Duration
}

// SignalSourcesConfig toggles each local evidence collection channel.
type SignalSourcesConfig struct {
	Sessions  bool `json:"sessions"`
	Git       bool `json:"git"`
	Failures  bool `json:"failures"`
	Meetings  bool `json:"meetings"`
}

// ScheduleConfig persists weekly scan schedule, bound mentor Bot, and filtering preferences.
type ScheduleConfig struct {
	MentorBotID         string              `json:"mentor_bot_id"`
	Enabled             bool                `json:"enabled"`
	Weekday             int                 `json:"weekday"` // 0=Sunday .. 6=Saturday
	Time                string              `json:"time"`    // HH:MM 24-hour
	Timezone            string              `json:"timezone"`
	LookbackDays        int                 `json:"lookback_days"` // 7, 14, or 30
	MisfireCatchupHours int                 `json:"misfire_catchup_hours"`
	NotifyDynamicIsland bool                `json:"notify_dynamic_island"`
	MinActivitySessions int                 `json:"min_activity_sessions"`
	MinActivityCommits  int                 `json:"min_activity_commits"`
	ExcludedWorkspaces  []string            `json:"excluded_workspaces"`
	SignalSources       SignalSourcesConfig `json:"signal_sources"`
	LastRunAt           string              `json:"last_run_at,omitempty"`
	NextRunAt           string              `json:"next_run_at,omitempty"`
	UpdatedAt           string              `json:"updated_at,omitempty"`
}

// EvidenceItem links a discovered learning topic back to concrete local work artifacts.
type EvidenceItem struct {
	Kind        string `json:"kind"` // session | file | commit | ci_job | meeting | doc
	Label       string `json:"label"`
	ProjectName string `json:"project_name,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
}

// QuizQuestion is one code/work-grounded diagnostic self-check question.
type QuizQuestion struct {
	Question string `json:"question"`
	Hint     string `json:"hint,omitempty"`
	Answer   string `json:"answer,omitempty"`
}

// ReviewState tracks Leitner / spaced-repetition progression (1 -> 3 -> 7 -> 21 days).
type ReviewState struct {
	Stage               int    `json:"stage"` // 0=not enrolled, 1..4=active rounds
	IntervalDays        int    `json:"interval_days"`
	NextReviewAt        string `json:"next_review_at,omitempty"`
	LastReviewedAt      string `json:"last_reviewed_at,omitempty"`
	PassCount           int    `json:"pass_count"`
	FuzzyCount          int    `json:"fuzzy_count"`
	SyncSessionSchedule bool   `json:"sync_session_schedule,omitempty"`
	ScheduleID          string `json:"schedule_id,omitempty"`
}

// LearningTopic is one discovered knowledge gap / reviewable micro-card.
type LearningTopic struct {
	ID               string         `json:"id"`
	Fingerprint      string         `json:"fingerprint"`
	Title            string         `json:"title"`
	Category         string         `json:"category"` // blackbox_code | recurring_friction | failure_pattern | new_domain
	Priority         string         `json:"priority"` // high | medium | low
	Status           string         `json:"status"`   // open | learning | review_due | mastered | snoozed | dismissed
	EstimatedMinutes int            `json:"estimated_minutes"`
	ProjectKey       string         `json:"project_key,omitempty"`
	ProjectName      string         `json:"project_name,omitempty"`
	Workspace        string         `json:"workspace,omitempty"`
	ConnectionID     string         `json:"connection_id,omitempty"`
	WhyNow           string         `json:"why_now"`
	KeyPoints        []string       `json:"key_points"`
	QuizQuestions    []QuizQuestion `json:"quiz_questions"`
	Evidence         []EvidenceItem `json:"evidence"`
	StudyNotes       string         `json:"study_notes,omitempty"`
	NotesFilePath    string         `json:"notes_file_path,omitempty"`
	StudySessionKey  string         `json:"study_session_key,omitempty"`
	StudyBotID       string         `json:"study_bot_id,omitempty"`
	OccurrenceCount  int            `json:"occurrence_count"`
	LastScanRunID    string         `json:"last_scan_run_id,omitempty"`
	SnoozeUntil      string         `json:"snooze_until,omitempty"`
	MasteredAt       string         `json:"mastered_at,omitempty"`
	Review           ReviewState    `json:"review"`
	CreatedAt        string         `json:"created_at"`
	UpdatedAt        string         `json:"updated_at"`
}

// ScanRun records one manual or weekly scheduled Learning Radar Bot scan.
type ScanRun struct {
	ID                string `json:"id"`
	Trigger           string `json:"trigger"` // manual | scheduled | agent
	Status            string `json:"status"`  // running | completed | skipped | timeout | failed
	BotID             string `json:"bot_id,omitempty"`
	BotName           string `json:"bot_name,omitempty"`
	SessionKey        string `json:"session_key,omitempty"`
	InboxDir          string `json:"inbox_dir,omitempty"`
	LookbackDays      int    `json:"lookback_days"`
	StartedAt         string `json:"started_at"`
	FinishedAt        string `json:"finished_at,omitempty"`
	DurationMs        int64  `json:"duration_ms,omitempty"`
	EvidenceCount     int    `json:"evidence_count"`
	ActiveSessions    int    `json:"active_sessions"`
	CommitCount       int    `json:"commit_count"`
	NewTopicsCount    int    `json:"new_topics_count"`
	MergedTopicsCount int    `json:"merged_topics_count"`
	SkippedCount      int    `json:"skipped_count,omitempty"`
	Summary           string `json:"summary,omitempty"`
	Error             string `json:"error,omitempty"`
}

// EvidencePack is the deterministic JSON bundle staged into the Mentor Bot's inbox.
type EvidencePack struct {
	RunID          string                 `json:"run_id"`
	Trigger        string                 `json:"trigger"`
	From           string                 `json:"from"`
	To             string                 `json:"to"`
	LookbackDays   int                    `json:"lookback_days"`
	GeneratedAt    string                 `json:"generated_at"`
	ActiveSessions int                    `json:"active_sessions"`
	CommitCount    int                    `json:"commit_count"`
	EvidenceCount  int                    `json:"evidence_count"`
	Projects       []ProjectEvidencePack  `json:"projects"`
	ExcludedTopics []ExcludedTopicRef     `json:"excluded_topics"`
	ActiveTopics   []ExcludedTopicRef     `json:"active_topics"`
}

type ProjectEvidencePack struct {
	ProjectKey   string           `json:"project_key"`
	ProjectName  string           `json:"project_name"`
	Workspace    string           `json:"workspace,omitempty"`
	ConnectionID string           `json:"connection_id,omitempty"`
	Sessions     []SessionClue    `json:"sessions,omitempty"`
	GitChanges   []GitChangeClue  `json:"git_changes,omitempty"`
	Failures     []FailureClue    `json:"failures,omitempty"`
	MeetingsDocs []MeetingDocClue `json:"meetings_docs,omitempty"`
}

type SessionClue struct {
	AgentID         string   `json:"agent_id"`
	SessionID       string   `json:"session_id"`
	SessionKey      string   `json:"session_key"`
	Title           string   `json:"title"`
	UpdatedAt       string   `json:"updated_at"`
	MessageCount    int      `json:"message_count"`
	UserQuestions   []string `json:"user_questions,omitempty"`
	RecentSnippets  []string `json:"recent_snippets,omitempty"`
}

type GitChangeClue struct {
	Kind         string   `json:"kind"` // checkpoint | commit
	Timestamp    string   `json:"timestamp"`
	CommitSHA    string   `json:"commit_sha,omitempty"`
	Branch       string   `json:"branch,omitempty"`
	Message      string   `json:"message"`
	FilesChanged int      `json:"files_changed,omitempty"`
	Insertions   int      `json:"insertions,omitempty"`
	Deletions    int      `json:"deletions,omitempty"`
	Files        []string `json:"files,omitempty"`
}

type FailureClue struct {
	Kind      string `json:"kind"` // task_failed | gitlab_ci
	Title     string `json:"title"`
	Detail    string `json:"detail,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Ref       string `json:"ref,omitempty"`
}

type MeetingDocClue struct {
	Kind      string `json:"kind"` // meeting | office_doc
	Title     string `json:"title"`
	Summary   string `json:"summary,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

type ExcludedTopicRef struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	ProjectName string `json:"project_name,omitempty"`
}

// IngestTopicInput is one structured blind-spot card submitted by the Mentor Bot via CLI.
type IngestTopicInput struct {
	Title            string         `json:"title"`
	Category         string         `json:"category"`
	Priority         string         `json:"priority,omitempty"`
	EstimatedMinutes int            `json:"estimated_minutes,omitempty"`
	ProjectKey       string         `json:"project_key,omitempty"`
	ProjectName      string         `json:"project_name,omitempty"`
	Workspace        string         `json:"workspace,omitempty"`
	ConnectionID     string         `json:"connection_id,omitempty"`
	WhyNow           string         `json:"why_now"`
	KeyPoints        []string       `json:"key_points"`
	QuizQuestions    []QuizQuestion `json:"quiz_questions,omitempty"`
	Evidence         []EvidenceItem `json:"evidence,omitempty"`
	NotesFilePath    string         `json:"notes_file_path,omitempty"`
}

// IngestRequest is submitted by astrorder-agent learning ingest.
type IngestRequest struct {
	RunID   string             `json:"run_id,omitempty"`
	Summary string             `json:"summary,omitempty"`
	Topics  []IngestTopicInput `json:"topics"`
}

// IngestResponse reports how many topics were inserted, merged, or skipped.
type IngestResponse struct {
	OK                bool            `json:"ok"`
	RunID             string          `json:"run_id,omitempty"`
	NewTopicsCount    int             `json:"new_topics_count"`
	MergedTopicsCount int             `json:"merged_topics_count"`
	SkippedCount      int             `json:"skipped_count"`
	Topics            []LearningTopic `json:"topics"`
}

// BootstrapMentorBotRequest creates (or reuses) the official Learning Mentor Bot.
type BootstrapMentorBotRequest struct {
	Name             string `json:"name,omitempty"`
	Slug             string `json:"slug,omitempty"`
	Workspace        string `json:"workspace,omitempty"`
	ConnectionID     string `json:"connection_id,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	Model            string `json:"model,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Effort           string `json:"effort,omitempty"`
	UpgradeTemplates bool   `json:"upgrade_templates,omitempty"`
}

type BootstrapMentorBotResponse struct {
	OK       bool            `json:"ok"`
	Created  bool            `json:"created"`
	Bot      bots.BotSummary `json:"bot"`
	Config   ScheduleConfig  `json:"config"`
	Warnings []string        `json:"warnings,omitempty"`
}

type TriggerScanRequest struct {
	LookbackDays int      `json:"lookback_days,omitempty"`
	ProjectKeys  []string `json:"project_keys,omitempty"`
	BotID        string   `json:"bot_id,omitempty"`
	Trigger      string   `json:"trigger,omitempty"`
}

type TriggerScanResponse struct {
	OK      bool    `json:"ok"`
	Run     ScanRun `json:"run"`
	Message string  `json:"message,omitempty"`
}

type UpdateTopicRequest struct {
	Status        *string `json:"status,omitempty"`
	StudyNotes    *string `json:"study_notes,omitempty"`
	NotesFilePath *string `json:"notes_file_path,omitempty"`
	SnoozeDays    *int    `json:"snooze_days,omitempty"`
}

type StartStudyRequest struct {
	BotID               string `json:"bot_id,omitempty"`
	AgentID             string `json:"agent_id,omitempty"`
	ExtraInstruction    string `json:"extra_instruction,omitempty"`
	SyncSessionSchedule *bool  `json:"sync_session_schedule,omitempty"`
	Mode                string `json:"mode,omitempty"` // study (default) | review_drill
}

type StartStudyResponse struct {
	OK         bool          `json:"ok"`
	Topic      LearningTopic `json:"topic"`
	SessionKey string        `json:"session_key,omitempty"`
	AgentID    string        `json:"agent_id,omitempty"`
	SessionID  string        `json:"session_id,omitempty"`
	InboxDir   string        `json:"inbox_dir,omitempty"`
	Message    string        `json:"message,omitempty"`
}

type ReviewFeedbackRequest struct {
	Outcome             string `json:"outcome"` // enroll | fuzzy | remembered | mastered
	Notes               string `json:"notes,omitempty"`
	SyncSessionSchedule *bool  `json:"sync_session_schedule,omitempty"`
}

type ReviewFeedbackResponse struct {
	OK      bool          `json:"ok"`
	Topic   LearningTopic `json:"topic"`
	Message string        `json:"message,omitempty"`
}

type OverviewStats struct {
	PendingCount   int `json:"pending_count"`
	BlackboxCount  int `json:"blackbox_count"`
	FrictionCount  int `json:"friction_count"`
	FailureCount   int `json:"failure_count"`
	NewDomainCount int `json:"new_domain_count"`
	DueReviewCount int `json:"due_review_count"`
	LearningCount  int `json:"learning_count"`
	MasteredCount  int `json:"mastered_count"`
	DismissedCount int `json:"dismissed_count"`
	ProjectCount   int `json:"project_count"`
	WeeklyNewCount int `json:"weekly_new_count"`
}

type OverviewResponse struct {
	Config     ScheduleConfig   `json:"config"`
	MentorBot  *bots.BotSummary `json:"mentor_bot,omitempty"`
	Stats      OverviewStats    `json:"stats"`
	ActiveRun  *ScanRun         `json:"active_run,omitempty"`
	LatestRun  *ScanRun         `json:"latest_run,omitempty"`
	Topics     []LearningTopic  `json:"topics"`
	Runs       []ScanRun        `json:"runs"`
}
