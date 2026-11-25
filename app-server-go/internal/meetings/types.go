package meetings

// MeetingActionItem represents a single follow-up action item extracted from a meeting.
type MeetingActionItem struct {
	ID           string `json:"id"`
	MeetingID    string `json:"meeting_id"`
	MeetingTitle string `json:"meeting_title"`
	Text         string `json:"text"`
	Owner        string `json:"owner,omitempty"`
	DueDate      string `json:"due_date,omitempty"`
	Priority     string `json:"priority,omitempty"` // high | medium | low
	Status       string `json:"status"`             // open | done
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

// MeetingTranscriptSegment represents one timestamped speech chunk or inline user note.
type MeetingTranscriptSegment struct {
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	Seconds    int    `json:"seconds"`
	Speaker    string `json:"speaker,omitempty"`
	Text       string `json:"text"`
	Kind       string `json:"kind,omitempty"` // "speech" | "note"
	Bookmarked bool   `json:"bookmarked,omitempty"`
}

// MeetingTopic captures an evolving discussion chapter with optional anchor timestamp.
type MeetingTopic struct {
	Timestamp string `json:"timestamp,omitempty"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
}

// MeetingRecord represents a persisted meeting with live transcript timeline,
// structured minutes, topics, and action items.
type MeetingRecord struct {
	ID              string                     `json:"id"`
	Title           string                     `json:"title"`
	MeetingDate     string                     `json:"meeting_date"`
	DurationSeconds int                        `json:"duration_seconds,omitempty"`
	ProjectKey      string                     `json:"project_key,omitempty"`
	Workspace       string                     `json:"workspace,omitempty"`
	Participants    []string                   `json:"participants,omitempty"`
	Transcript      string                     `json:"transcript,omitempty"`
	Segments        []MeetingTranscriptSegment `json:"segments,omitempty"`
	MinutesMarkdown string                     `json:"minutes_markdown"`
	Summary         string                     `json:"summary,omitempty"`
	Topics          []MeetingTopic             `json:"topics,omitempty"`
	Decisions       []string                   `json:"decisions,omitempty"`
	Risks           []string                   `json:"risks,omitempty"`
	ActionItems     []MeetingActionItem        `json:"action_items"`
	Model           string                     `json:"model,omitempty"`
	CreatedAt       string                     `json:"created_at,omitempty"`
	UpdatedAt       string                     `json:"updated_at,omitempty"`
}

// MeetingSettings controls the meeting summarization model selection and live cadence.
type MeetingSettings struct {
	ModelSource              string `json:"model_source"`            // "global" | "provider" | "custom" (legacy)
	ProviderID               string `json:"provider_id,omitempty"`   // AI Provider ID when ModelSource == "provider"
	ProviderName             string `json:"provider_name,omitempty"` // Computed provider name returned to UI
	BaseURL                  string `json:"base_url"`
	APIKey                   string `json:"api_key,omitempty"`
	APIKeyClear              bool   `json:"api_key_clear,omitempty"`
	APIKeySet                bool   `json:"api_key_set"`
	APIKeyPreview            string `json:"api_key_preview,omitempty"`
	Model                    string `json:"model"`
	Reasoning                string `json:"reasoning,omitempty"`
	AutoSummarize            bool   `json:"auto_summarize"`
	AutoSummarizeIntervalSec int    `json:"auto_summarize_interval_sec"` // 20 | 30 | 60
	SummaryStyle             string `json:"summary_style"`               // "concise" | "detailed" | "technical"

	// Computed effective runtime state returned to UI
	EffectiveModel        string `json:"effective_model,omitempty"`
	EffectiveProviderName string `json:"effective_provider_name,omitempty"`
	EffectiveConfigured   bool   `json:"effective_configured"`
	GlobalModel           string `json:"global_model,omitempty"`
	GlobalProviderName    string `json:"global_provider_name,omitempty"`
	GlobalConfigured      bool   `json:"global_configured"`
}

// SettingsTestResponse reports live connectivity for the meeting summary model.
type SettingsTestResponse struct {
	OK        bool   `json:"ok"`
	Model     string `json:"model,omitempty"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// SummarizeRequest is accepted by POST /api/v1/meetings/summarize.
type SummarizeRequest struct {
	Title               string              `json:"title,omitempty"`
	MeetingDate         string              `json:"meeting_date,omitempty"`
	Participants        []string            `json:"participants,omitempty"`
	Transcript          string              `json:"transcript"`
	ExtraNotes          string              `json:"extra_notes,omitempty"`
	Mode                string              `json:"mode,omitempty"` // "llm" (default) or "prompt_only"
	SummaryStyle        string              `json:"summary_style,omitempty"`
	Incremental         bool                `json:"incremental,omitempty"`
	NewTranscript       string              `json:"new_transcript,omitempty"`
	PreviousSummary     string              `json:"previous_summary,omitempty"`
	PreviousTopics      []MeetingTopic      `json:"previous_topics,omitempty"`
	PreviousDecisions   []string            `json:"previous_decisions,omitempty"`
	PreviousRisks       []string            `json:"previous_risks,omitempty"`
	PreviousActionItems []MeetingActionItem `json:"previous_action_items,omitempty"`
}

// SummarizeResponse is returned by POST /api/v1/meetings/summarize.
type SummarizeResponse struct {
	OK              bool                `json:"ok"`
	Mode            string              `json:"mode"` // "llm" | "prompt_only"
	LLMConfigured   bool                `json:"llm_configured"`
	Model           string              `json:"model,omitempty"`
	Title           string              `json:"title"`
	Summary         string              `json:"summary"`
	Topics          []MeetingTopic      `json:"topics"`
	Decisions       []string            `json:"decisions"`
	Risks           []string            `json:"risks"`
	ActionItems     []MeetingActionItem `json:"action_items"`
	MinutesMarkdown string              `json:"minutes_markdown"`
	AssembledPrompt string              `json:"assembled_prompt"`
	Message         string              `json:"message,omitempty"`
}

// ActionItemPatchRequest updates a single action item inside a persisted meeting.
type ActionItemPatchRequest struct {
	MeetingID string  `json:"meeting_id"`
	ItemID    string  `json:"item_id"`
	Status    *string `json:"status,omitempty"`
	Text      *string `json:"text,omitempty"`
	Owner     *string `json:"owner,omitempty"`
	DueDate   *string `json:"due_date,omitempty"`
	Priority  *string `json:"priority,omitempty"`
}
