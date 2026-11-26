package bots

import (
	"context"
	"net/http"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
	"astrorder.dev/app-server/internal/targetfs"
)

const (
	BasePath        = "/api/v1/bots"
	PreferenceKey   = "astrorder:bots:v1"
	maxJSONBodySize = 4 << 20 // 4 MiB

	ApprovalModeFullAccess = "full_access"
	ApprovalModeAuto       = "auto"
	ApprovalModeManual     = "manual"
)

// BrowserAuthorizer validates browser/desktop HTTP requests.
type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

// SessionHistoryReader reads recent messages for a session (provided by sessions.Handler).
type SessionHistoryReader func(ctx context.Context, agentID, sessionID string, limit int) ([]map[string]any, error)

// SpawnRequest describes spawning a clean job session in a Bot's workspace and sending its initial brief.
type SpawnRequest struct {
	OperationID  string
	AgentID      string
	Title        string
	Workspace    string
	Model        string
	Provider     string
	Effort       string
	ApprovalMode string
	Prompt       string
	// Caller is optional; present when invoked from another Agent session via astrorder-agent.
	Caller map[string]any
}

// SpawnResult holds the spawned session and daemon receipt.
type SpawnResult struct {
	Session     map[string]any `json:"session"`
	Receipt     map[string]any `json:"receipt,omitempty"`
	OperationID string         `json:"operation_id,omitempty"`
	Stage       string         `json:"stage,omitempty"`
}

// SessionSpawner creates a clean session for a Bot job and sends the initial brief.
type SessionSpawner func(ctx context.Context, req SpawnRequest) (*SpawnResult, error)

// Options configures the Bots service and HTTP handler.
type Options struct {
	Store         *store.Store
	Authorizer    BrowserAuthorizer
	Resolver      *targetfs.Resolver
	HistoryReader SessionHistoryReader
	Spawner       SessionSpawner
	HTTPClient    *http.Client
}

// BotPreset is a reusable quick-dispatch prompt template configured on a Bot.
type BotPreset struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Title  string `json:"title,omitempty"`
	Prompt string `json:"prompt"`
}

// BotProfile is the persisted registry record for one Bot (1 Bot = 1 workspace/project).
type BotProfile struct {
	ID                  string      `json:"id"`
	Slug                string      `json:"slug"`
	Name                string      `json:"name"`
	Description         string      `json:"description"`
	Icon                string      `json:"icon,omitempty"`
	Workspace           string      `json:"workspace"`
	ConnectionID        string      `json:"connection_id,omitempty"`
	DefaultAgentID      string      `json:"default_agent_id"`
	DefaultProvider     string      `json:"default_provider,omitempty"`
	DefaultModel        string      `json:"default_model,omitempty"`
	DefaultEffort       string      `json:"default_effort,omitempty"`
	DefaultApprovalMode string      `json:"default_approval_mode,omitempty"`
	DefaultNotifyCaller *bool       `json:"default_notify_caller,omitempty"`
	Presets             []BotPreset `json:"presets,omitempty"`
	OfficeMode          bool        `json:"office_mode,omitempty"`
	Tags                []string    `json:"tags,omitempty"`
	SourceSessionKey    string      `json:"source_session_key,omitempty"`
	SourceSessionTitle  string      `json:"source_session_title,omitempty"`
	CreatedAt           string      `json:"created_at"`
	UpdatedAt           string      `json:"updated_at"`
}

// BotSkillSummary is lightweight metadata about one skill under <workspace>/.agents/skills/<slug>/SKILL.md.
type BotSkillSummary struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

// BotSkillDoc includes the full Markdown content and auxiliary asset paths of one skill folder.
type BotSkillDoc struct {
	BotSkillSummary
	Content string   `json:"content"`
	Files   []string `json:"files,omitempty"`
}

// BotSummary enriches BotProfile with live workspace doc and session stats.
type BotSummary struct {
	BotProfile
	HasAgentsMD     bool              `json:"has_agents_md"`
	AgentsMDExcerpt string            `json:"agents_md_excerpt,omitempty"`
	Skills          []BotSkillSummary `json:"skills"`
	SessionCount    int               `json:"session_count"`
	RunningCount    int               `json:"running_count"`
	LastActiveAt    string            `json:"last_active_at,omitempty"`
}

// BotDocsResponse returns live workspace constitution (AGENTS.md) and skills.
type BotDocsResponse struct {
	Bot              BotProfile    `json:"bot"`
	AgentsMDPath     string        `json:"agents_md_path"`
	AgentsMDExists   bool          `json:"agents_md_exists"`
	AgentsMD         string        `json:"agents_md"`
	Skills           []BotSkillDoc `json:"skills"`
	ReferencedSkills []string      `json:"referenced_skills,omitempty"`
}

// CreateBotRequest registers a new Bot and optionally writes AGENTS.md and/or a skill to its workspace.
type CreateBotRequest struct {
	Name                string      `json:"name"`
	Slug                string      `json:"slug,omitempty"`
	Description         string      `json:"description,omitempty"`
	Icon                string      `json:"icon,omitempty"`
	Workspace           string      `json:"workspace"`
	ConnectionID        string      `json:"connection_id,omitempty"`
	DefaultAgentID      string      `json:"default_agent_id"`
	DefaultProvider     string      `json:"default_provider,omitempty"`
	DefaultModel        string      `json:"default_model,omitempty"`
	DefaultEffort       string      `json:"default_effort,omitempty"`
	DefaultApprovalMode string      `json:"default_approval_mode,omitempty"`
	DefaultNotifyCaller *bool       `json:"default_notify_caller,omitempty"`
	Presets             []BotPreset `json:"presets,omitempty"`
	OfficeMode          bool        `json:"office_mode,omitempty"`
	Tags                []string    `json:"tags,omitempty"`
	SourceSessionKey    string      `json:"source_session_key,omitempty"`
	SourceSessionTitle  string      `json:"source_session_title,omitempty"`
	WriteAgentsMD       bool        `json:"write_agents_md,omitempty"`
	AgentsMD            string      `json:"agents_md,omitempty"`
	WriteSkill          bool        `json:"write_skill,omitempty"`
	SkillSlug           string      `json:"skill_slug,omitempty"`
	SkillMD             string      `json:"skill_md,omitempty"`
}

// UpdateBotRequest updates mutable fields on a registered Bot.
type UpdateBotRequest struct {
	Name                *string      `json:"name,omitempty"`
	Slug                *string      `json:"slug,omitempty"`
	Description         *string      `json:"description,omitempty"`
	Icon                *string      `json:"icon,omitempty"`
	Workspace           *string      `json:"workspace,omitempty"`
	ConnectionID        *string      `json:"connection_id,omitempty"`
	DefaultAgentID      *string      `json:"default_agent_id,omitempty"`
	DefaultProvider     *string      `json:"default_provider,omitempty"`
	DefaultModel        *string      `json:"default_model,omitempty"`
	DefaultEffort       *string      `json:"default_effort,omitempty"`
	DefaultApprovalMode *string      `json:"default_approval_mode,omitempty"`
	DefaultNotifyCaller *bool        `json:"default_notify_caller,omitempty"`
	Presets             *[]BotPreset `json:"presets,omitempty"`
	OfficeMode          *bool        `json:"office_mode,omitempty"`
	Tags                []string     `json:"tags,omitempty"`
}

// SaveAgentsMDRequest writes <workspace>/AGENTS.md.
type SaveAgentsMDRequest struct {
	Content string `json:"content"`
}

// SaveSkillRequest writes <workspace>/.agents/skills/<slug>/SKILL.md.
type SaveSkillRequest struct {
	Content string `json:"content"`
}

// OptimizeRequest asks AI (with clean template fallback) to turn basic Bot settings into AGENTS.md.
type OptimizeRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	Instructions     string `json:"instructions,omitempty"`
	Workspace        string `json:"workspace,omitempty"`
	DefaultAgentID   string `json:"default_agent_id,omitempty"`
	ExistingAgentsMD string `json:"existing_agents_md,omitempty"`
	Mode             string `json:"mode,omitempty"` // "llm" (default) or "template"
	ProviderID       string `json:"provider_id,omitempty"`
	Model            string `json:"model,omitempty"`
}

// OptimizeResponse returns optimized Bot metadata and AGENTS.md draft.
type OptimizeResponse struct {
	OK                   bool     `json:"ok"`
	Mode                 string   `json:"mode"` // "llm" or "template"
	LLMConfigured        bool     `json:"llm_configured"`
	Model                string   `json:"model,omitempty"`
	SuggestedName        string   `json:"suggested_name"`
	SuggestedSlug        string   `json:"suggested_slug"`
	SuggestedDescription string   `json:"suggested_description"`
	SuggestedTags        []string `json:"suggested_tags"`
	AgentsMD             string   `json:"agents_md"`
	Message              string   `json:"message,omitempty"`
}

// DistillRequest extracts reusable AGENTS.md (and optionally SKILL.md) experience from an existing session.
type DistillRequest struct {
	SessionKey    string `json:"session_key,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	BotID         string `json:"bot_id,omitempty"` // optional: feedback-merge into an existing Bot
	Name          string `json:"name,omitempty"`
	Hint          string `json:"hint,omitempty"`
	MaxMessages   int    `json:"max_messages,omitempty"`
	GenerateSkill *bool  `json:"generate_skill,omitempty"` // optional: defaults to false (focus on AGENTS.md guiding existing/session skills)
	Mode          string `json:"mode,omitempty"`           // "llm" (default) or "template"
	ProviderID    string `json:"provider_id,omitempty"`
	Model         string `json:"model,omitempty"`
}

// DistillResponse returns editable AGENTS.md (and optional SKILL.md) drafts distilled from a session.
type DistillResponse struct {
	OK                   bool              `json:"ok"`
	Mode                 string            `json:"mode"` // "llm" or "template"
	LLMConfigured        bool              `json:"llm_configured"`
	Model                string            `json:"model,omitempty"`
	SourceSessionKey     string            `json:"source_session_key"`
	SourceSessionTitle   string            `json:"source_session_title"`
	Workspace            string            `json:"workspace"`
	ConnectionID         string            `json:"connection_id,omitempty"`
	DefaultAgentID       string            `json:"default_agent_id"`
	SuggestedName        string            `json:"suggested_name"`
	SuggestedSlug        string            `json:"suggested_slug"`
	SuggestedDescription string            `json:"suggested_description"`
	SuggestedTags        []string          `json:"suggested_tags"`
	AgentsMD             string            `json:"agents_md"`
	SkillGenerated       bool              `json:"skill_generated"`
	SkillSlug            string            `json:"skill_slug,omitempty"`
	SkillName            string            `json:"skill_name,omitempty"`
	SkillDescription     string            `json:"skill_description,omitempty"`
	SkillMD              string            `json:"skill_md,omitempty"`
	ExistingAgentsMD     string            `json:"existing_agents_md,omitempty"`
	HasExistingAgentsMD  bool              `json:"has_existing_agents_md"`
	ExistingSkills       []BotSkillSummary `json:"existing_skills"`
	UsedSkills           []string          `json:"used_skills,omitempty"`
	MessageCount         int               `json:"message_count"`
	Message              string            `json:"message,omitempty"`
}

// StagedFile records one attachment copied from caller into the Bot's inbox/<job-id>/.
type StagedFile struct {
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
}

// UploadedInboxFile describes one file uploaded directly from the browser into a Bot's inbox staging area.
type UploadedInboxFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// InvokeRequest dispatches a clean job session to a Bot.
type InvokeRequest struct {
	Bot                string         `json:"bot,omitempty"`
	OperationID        string         `json:"operation_id,omitempty"`
	Title              string         `json:"title,omitempty"`
	Prompt             string         `json:"prompt"`
	AttachPaths        []string       `json:"attach_paths,omitempty"`
	CallerSessionKey   string         `json:"caller_session_key,omitempty"`
	CallerWorkspace    string         `json:"caller_workspace,omitempty"`
	CallerConnectionID string         `json:"caller_connection_id,omitempty"`
	Caller             map[string]any `json:"caller,omitempty"`
	NotifyCaller       *bool          `json:"notify_caller,omitempty"`
	AgentID            string         `json:"agent_id,omitempty"`
	Model              string         `json:"model,omitempty"`
	Provider           string         `json:"provider,omitempty"`
	Effort             string         `json:"effort,omitempty"`
	ApprovalMode       string         `json:"approval_mode,omitempty"`
}

// InvokeResponse reports the newly spawned Bot job session and staged attachments.
type InvokeResponse struct {
	OK          bool           `json:"ok"`
	Bot         BotProfile     `json:"bot"`
	JobID       string         `json:"job_id"`
	InboxDir    string         `json:"inbox_dir,omitempty"`
	StagedFiles []StagedFile   `json:"staged_files,omitempty"`
	Session     map[string]any `json:"session"`
	Receipt     map[string]any `json:"receipt,omitempty"`
	OperationID string         `json:"operation_id,omitempty"`
	Stage       string         `json:"stage,omitempty"`
	Message     string         `json:"message,omitempty"`
}
