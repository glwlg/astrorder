import { request, type BotSummary } from './client'

export type LearningTopicCategory =
  | 'blackbox_code'
  | 'recurring_friction'
  | 'failure_pattern'
  | 'new_domain'

export type LearningTopicPriority = 'high' | 'medium' | 'low'

export type LearningTopicStatus =
  | 'open'
  | 'learning'
  | 'review_due'
  | 'mastered'
  | 'snoozed'
  | 'dismissed'

export interface LearningSignalSourcesConfig {
  sessions: boolean
  git: boolean
  failures: boolean
  meetings: boolean
}

export interface LearningScheduleConfig {
  mentor_bot_id: string
  enabled: boolean
  weekday: number // 0=Sunday .. 6=Saturday
  time: string // HH:MM
  timezone: string
  lookback_days: number
  misfire_catchup_hours: number
  notify_dynamic_island: boolean
  min_activity_sessions: number
  min_activity_commits: number
  excluded_workspaces: string[]
  signal_sources: LearningSignalSourcesConfig
  last_run_at?: string
  next_run_at?: string
  updated_at?: string
}

export interface LearningEvidenceItem {
  kind: 'session' | 'file' | 'commit' | 'ci_job' | 'meeting' | 'doc' | string
  label: string
  project_name?: string
  workspace?: string
  agent_id?: string
  session_id?: string
  file_path?: string
  commit_sha?: string
  timestamp?: string
  snippet?: string
}

export interface LearningQuizQuestion {
  question: string
  hint?: string
  answer?: string
}

export interface LearningReviewState {
  stage: number
  interval_days: number
  next_review_at?: string
  last_reviewed_at?: string
  pass_count: number
  fuzzy_count: number
  sync_session_schedule?: boolean
  schedule_id?: string
}

export interface LearningTopic {
  id: string
  fingerprint: string
  title: string
  category: LearningTopicCategory
  priority: LearningTopicPriority
  status: LearningTopicStatus
  estimated_minutes: number
  project_key?: string
  project_name?: string
  workspace?: string
  connection_id?: string
  why_now: string
  key_points: string[]
  quiz_questions: LearningQuizQuestion[]
  evidence: LearningEvidenceItem[]
  study_notes?: string
  notes_file_path?: string
  study_session_key?: string
  study_bot_id?: string
  occurrence_count: number
  last_scan_run_id?: string
  snooze_until?: string
  mastered_at?: string
  review: LearningReviewState
  created_at: string
  updated_at: string
}

export interface LearningScanRun {
  id: string
  trigger: 'manual' | 'scheduled' | 'agent' | string
  status: 'running' | 'completed' | 'skipped' | 'timeout' | 'failed' | string
  bot_id?: string
  bot_name?: string
  session_key?: string
  inbox_dir?: string
  lookback_days: number
  started_at: string
  finished_at?: string
  duration_ms?: number
  evidence_count: number
  active_sessions: number
  commit_count: number
  new_topics_count: number
  merged_topics_count: number
  skipped_count?: number
  summary?: string
  error?: string
}

export interface LearningOverviewStats {
  pending_count: number
  blackbox_count: number
  friction_count: number
  failure_count: number
  new_domain_count: number
  due_review_count: number
  learning_count: number
  mastered_count: number
  dismissed_count: number
  project_count: number
  weekly_new_count: number
}

export interface LearningOverviewResponse {
  config: LearningScheduleConfig
  mentor_bot?: BotSummary
  stats: LearningOverviewStats
  active_run?: LearningScanRun
  latest_run?: LearningScanRun
  topics: LearningTopic[]
  runs: LearningScanRun[]
}

export interface BootstrapMentorBotRequest {
  name?: string
  slug?: string
  workspace?: string
  connection_id?: string
  agent_id?: string
  model?: string
  provider?: string
  effort?: string
}

export interface BootstrapMentorBotResponse {
  ok: boolean
  created: boolean
  bot: BotSummary
  config: LearningScheduleConfig
  warnings?: string[]
}

export interface TriggerLearningScanRequest {
  lookback_days?: number
  project_keys?: string[]
  bot_id?: string
}

export interface TriggerLearningScanResponse {
  ok: boolean
  run: LearningScanRun
  message?: string
}

export interface UpdateLearningTopicRequest {
  status?: LearningTopicStatus
  study_notes?: string
  notes_file_path?: string
  snooze_days?: number
}

export interface StartLearningStudyRequest {
  bot_id?: string
  agent_id?: string
  extra_instruction?: string
  sync_session_schedule?: boolean
  mode?: 'study' | 'review_drill'
}

export interface StartLearningStudyResponse {
  ok: boolean
  topic: LearningTopic
  session_key?: string
  agent_id?: string
  session_id?: string
  inbox_dir?: string
  message?: string
}

export interface SubmitLearningReviewRequest {
  outcome: 'enroll' | 'fuzzy' | 'remembered' | 'mastered'
  notes?: string
  sync_session_schedule?: boolean
}

export interface SubmitLearningReviewResponse {
  ok: boolean
  topic: LearningTopic
  message?: string
}

function sendJson<T>(path: string, body: unknown, method: 'POST' | 'PUT' | 'PATCH'): Promise<T> {
  return request<T>(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export const learningApi = {
  getOverview: (signal?: AbortSignal) =>
    request<LearningOverviewResponse>('/learning/overview', { signal }),

  getConfig: () =>
    request<{ config: LearningScheduleConfig }>('/learning/config'),

  saveConfig: (config: LearningScheduleConfig) =>
    sendJson<{ config: LearningScheduleConfig }>('/learning/config', config, 'PUT'),

  bootstrapMentorBot: (payload: BootstrapMentorBotRequest = {}) =>
    sendJson<BootstrapMentorBotResponse>('/learning/mentor-bot/bootstrap', payload, 'POST'),

  listScans: () =>
    request<{ items: LearningScanRun[] }>('/learning/scans'),

  triggerScan: (payload: TriggerLearningScanRequest = {}) =>
    sendJson<TriggerLearningScanResponse>('/learning/scans', payload, 'POST'),

  listTopics: (params?: { status?: string; category?: string }, signal?: AbortSignal) => {
    const q = new URLSearchParams()
    if (params?.status) q.set('status', params.status)
    if (params?.category) q.set('category', params.category)
    const suffix = q.toString() ? `?${q.toString()}` : ''
    return request<{ items: LearningTopic[] }>(`/learning/topics${suffix}`, { signal })
  },

  updateTopic: (id: string, payload: UpdateLearningTopicRequest) =>
    sendJson<{ topic: LearningTopic }>(`/learning/topics/${encodeURIComponent(id)}`, payload, 'PATCH'),

  deleteTopic: (id: string) =>
    request<{ ok: boolean; deleted: string }>(`/learning/topics/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),

  startStudy: (id: string, payload: StartLearningStudyRequest = {}) =>
    sendJson<StartLearningStudyResponse>(`/learning/topics/${encodeURIComponent(id)}/study`, payload, 'POST'),

  submitReview: (id: string, payload: SubmitLearningReviewRequest) =>
    sendJson<SubmitLearningReviewResponse>(`/learning/topics/${encodeURIComponent(id)}/review`, payload, 'POST'),

  exportProfile: () =>
    request<{ exported_at: string; config: LearningScheduleConfig; topics: LearningTopic[] }>('/learning/profile/export'),
}
