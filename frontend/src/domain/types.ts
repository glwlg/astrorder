export type Capability =
  | "chat"
  | "stop"
  | "queue"
  | "attachments"
  | "approvals"
  | "launch"
  | "history"
  | "events";

export type ApprovalMode = "manual" | "auto" | "full_access";

export type AgentKind = string;
export type AgentStatus = "disconnected" | "connecting" | "ready" | "error";
export type SessionStatus = "idle" | "running" | "waiting_approval" | "error";
export type MessageRole = "user" | "assistant" | "system" | "tool";
export type MessageKind = "message" | "thinking" | "tool";
export type CommandAction = "send" | "enqueue" | "stop" | "approve" | "cancel";
export type CommandState =
  | "received"
  | "queued"
  | "accepted"
  | "running"
  | "completed"
  | "failed"
  | "unknown"
  | "cancelled";

export interface Agent {
  id: string;
  kind: AgentKind;
  name: string;
  status: AgentStatus;
  capabilities: string[];
  limitation: string | null;
  source_id?: string;
  connection_id?: string | null;
  profile_name?: string | null;
  runtime_id?: string;
}

export interface Session {
  native_kind?: string | null;
  ephemeral?: boolean;
  is_open?: boolean;
  id: string;
  agent_id: string;
  title: string;
  workspace: string | null;
  status: SessionStatus;
  updated_at: string;
  last_user_at?: string;
  live?: boolean;
  source_id?: string;
  connection_id?: string | null;
  source_session_id?: string;
  project_id?: string | null;
  project_name?: string | null;
  history_state?: string;
  control_state?: string;
  selected_approval_mode?: string | null;
  handoff_from_agent_id?: string | null;
  handoff_from_session_id?: string | null;
  handoff_context?: string | null;
  handoff_context_consumed?: boolean;
  parent_session_id?: string | null;
  parent_agent_id?: string | null;
  parent_session_key?: string | null;
  delegation_parent_agent_id?: string | null;
  delegation_parent_session_id?: string | null;
  worktree_root_workspace?: string | null;
  worktree_branch?: string | null;
  worktree_managed?: boolean;
}

export interface GitWorktreeEntry {
  path: string;
  head: string;
  branch: string;
  detached: boolean;
  bare: boolean;
  is_main: boolean;
  is_current: boolean;
  locked: boolean;
  lock_reason?: string;
  prunable: boolean;
  prune_reason?: string;
  status_loaded: boolean;
  dirty: boolean;
  changed_files: number;
  ahead: number;
  behind: number;
}

export interface GitWorktreeBranch {
  name: string;
  is_current: boolean;
  occupied_by_path?: string;
}

export interface GitWorktreeCopyCandidate {
  path: string;
  size: number;
  source: 'worktreeinclude' | 'default' | string;
}

export interface GitWorktreeRepoConfig {
  default_layout?: 'sibling' | 'inside' | string;
  copy_files?: string[];
  setup_command?: string;
}

export interface GitWorktreesResponse {
  is_git_repo: boolean;
  workspace: string;
  main_workspace?: string;
  is_worktree: boolean;
  current_branch?: string;
  default_branch?: string;
  default_sibling_base?: string;
  default_inside_base?: string;
  worktrees: GitWorktreeEntry[];
  branches: GitWorktreeBranch[];
  copyable_files: GitWorktreeCopyCandidate[];
  repo_config?: GitWorktreeRepoConfig | null;
}

export interface SessionWorktreePayload {
  mode: 'none' | 'create' | 'existing';
  branch?: string;
  base_ref?: string;
  create_branch?: boolean;
  path?: string;
  layout?: 'sibling' | 'inside';
  copy_files?: string[];
}

export interface GitWorktreeAddResult {
  path: string;
  branch: string;
  main_workspace: string;
  head?: string;
  created_branch: boolean;
  copied_files: string[];
  skipped_files?: string[];
}

export interface GitWorktreeRemoveResult {
  removed: boolean;
  path: string;
  branch?: string;
  branch_deleted: boolean;
  branch_delete_error?: string;
}

export interface GitWorktreePruneResult {
  pruned_count: number;
  output: string;
}

export interface GitWorktreeMergeResult {
  merged: boolean;
  conflicted: boolean;
  aborted: boolean;
  source_branch: string;
  target_branch: string;
  target_path: string;
  strategy: string;
  conflict_files?: string[];
  detail?: string;
}

export interface GitWorktreeBootstrapResult {
  target_path: string;
  copied_files: string[];
  skipped_files?: string[];
}

export interface SessionHandoffContext {
  available: boolean;
  mode?: 'native_summary' | 'transcript_excerpt' | 'inline' | string;
  summary?: string;
  consumed?: boolean;
  source_agent_id?: string;
  source_agent_kind?: string;
  source_agent_name?: string;
  source_session_id?: string;
  source_session_title?: string;
  workspace?: string;
}

export interface Project {
  id: string;
  source_id: string;
  connection_id?: string | null;
  agent_id?: string | null;
  profile_name?: string | null;
  project_id: string;
  project_name?: string | null;
  workspace?: string | null;
  session_count: number;
  updated_at: string;
}

export interface Attachment {
  id: string;
  name: string;
  media_type: string;
  url: string;
}

export interface Message {
  id: string;
  session_id: string;
  agent_id: string;
  role: MessageRole;
  kind: MessageKind;
  text: string;
  attachments: Attachment[];
  created_at: string;
  completed_at?: string | null;
  timestamp_source?: 'native' | 'received';
  command_id: string | null;
  turn_id?: string | null;
  tool: Record<string, unknown> | null;
  delivery?: string | null;
  answered?: boolean;
  questions?: { title: string; options?: string[] | null; multi_select?: boolean }[] | null;
}

export interface Command {
  id: string;
  session_id: string;
  agent_id: string;
  action: CommandAction;
  state: CommandState;
  text: string;
  attachments: Attachment[];
  created_at: string;
  error: string | null;
  target_id?: string | null;
}

export interface AgentCommand {
  name: string;
  description: string;
  input_hint: string | null;
}

export interface AgentMention {
  name: string;
  description: string;
  kind: "skill" | "file" | "bot";
  path: string;
  botSlug?: string;
}

export interface CommandPayload {
  id: string;
  agent_id: string;
  session_id: string;
  action: CommandAction;
  text: string;
  attachment_ids: string[];
  target_id: string | null;
}

export type TaskKind = "background" | "todo" | "subagent" | "tool";
export type TaskStatus =
  | "pending"
  | "running"
  | "waiting_approval"
  | "completed"
  | "failed"
  | "cancelled"
  | "unknown";

export interface TaskLog {
  id: string;
  text: string;
  level: string;
  created_at: string;
}

export interface Task {
  id: string;
  session_id: string;
  agent_id: string;
  kind: TaskKind;
  title: string;
  status: TaskStatus;
  progress: Record<string, unknown> | null;
  command: string | null;
  logs: TaskLog[];
  target_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface EventEnvelope {
  id: string;
  cursor: number;
  type: string;
  agent_id: string | null;
  session_id: string | null;
  data: Record<string, unknown>;
}

export interface BootstrapPayload {
  notifications?: EventNotification[];
  commands?: Command[];
  approvals?: Approval[];
  protocol_version: number;
  agents: Agent[];
  disabled_agent_connections?: string[];
  projects?: Project[];
  sessions: Session[];
  tasks?: Task[];
  cursor: number;
}

export interface Approval {
  id: string;
  agent_id: string | null;
  session_id: string | null;
  title: string;
  detail: string;
  state: string;
  target_id: string | null;
  data: Record<string, unknown>;
}

export type EventNotificationKind =
  "task_completed" | "task_failed" | "approval_pending";

export interface EventNotification {
  cursor?: number;
  key: string;
  kind: EventNotificationKind;
  title: string;
  message: string;
  agent_id: string;
  session_id: string;
  group_id?: string;
  group_name?: string;
  created_at: string;
}

export interface RuntimeItem {
  kind: string;
  available: boolean;
  capabilities: string[];
  reason: string | null;
}

export interface RuntimePayload {
  items: RuntimeItem[];
}

export type LocalHermesConnectionState =
  "discovered" | "installed" | "connecting" | "connected" | "offline" | "error";

export interface LocalHermesConnection {
  kind: "hermes";
  state: LocalHermesConnectionState;
  available: boolean;
  version: string | null;
  agent_id: string | null;
  source_id?: string;
  profile_name?: string;
  runtime_id?: string | null;
  session_id: string | null;
  detail: string;
}

export interface SshConnectionSettings {
  connection_id?: string | null;
  display_name?: string | null;
  profile_name?: string | null;
  host: string | null;
  port: number;
  user: string | null;
  ssh_config_alias: string | null;
  identity_file: string | null;
  hermes_path: string | null;
  codex_executable?: string | null;
  grok_executable?: string | null;
  antigravity_executable?: string | null;
  workspace: string | null;
}

export interface ConnectionHistoryEntry {
  id: string;
  connection_id: string;
  stage: string;
  state: string;
  detail: string;
  details: Record<string, unknown>;
  created_at: string;
}

export interface SshConnection {
  id: string;
  display_name: string;
  profile_name: string;
  state:
    | "unconfigured"
    | "configured"
    | "validated"
    | "connecting"
    | "connected"
    | "disconnected"
    | "error";
  settings: SshConnectionSettings | null;
  detail: string;
  remote_os: string | null;
  agent_id: string | null;
  runtime_id: string | null;
}

export interface SshConnectionsCollection {
  items: SshConnection[];
  state: SshConnection["state"];
  settings: SshConnectionSettings | null;
  detail: string;
}

export interface ConnectionsPayload {
  local: LocalHermesConnection;
  ssh: SshConnectionsCollection;
}

export interface AuthSession {
  authenticated: boolean;
}

export interface DraftAttachment {
  key: string;
  file: File;
  attachment?: Attachment;
  error?: string;
}

export interface DraftState {
  text: string;
  attachments: DraftAttachment[];
  sessionRefs?: SessionRef[];
}

export interface SessionRef {
  key: string;
  agent_id: string;
  id: string;
  title: string;
}

export type OutboxStatus = "submitting" | CommandState;

export interface OutboxEntry {
  command: Command;
  status: OutboxStatus;
  error: string | null;
}

export type ConnectionStatus =
  "disconnected" | "connecting" | "connected" | "error";

export interface BotGroupMember {
  id?: string;
  machine_id: string;
  agent_id: string;
  name?: string | null;
  alias?: string | null;
  system_role_prompt?: string | null;
  model_provider?: string | null;
  model_name?: string | null;
  thinking_effort?: string | null;
  approval_policy?: ApprovalMode | null;
  workspace?: string | null;
  source_session_id?: string | null;
  fork_session_id?: string | null;
}

export interface BotGroupMemberState {
  status: "idle" | "waiting" | "running" | "success" | "failed" | "unknown" | "cancelled";
  muted?: boolean;
  session_id?: string | null;
  last_error?: string | null;
  updated_at?: string;
}

export interface BotGroup {
  id: string;
  name: string;
  description?: string | null;
  members: BotGroupMember[];
  max_hops: number;
  active_hop?: number;
  active_speaker_agent_id?: string | null;
  active_speakers?: string[];
  member_states?: Record<string, BotGroupMemberState>;
  coordination?: {
    status: "idle" | "deciding" | "running" | "waiting" | "complete" | "error" | "stopped";
    error?: string;
  };
  created_at: string;
  updated_at: string;
}

export interface GroupMessage {
  id: string;
  group_id: string;
  sender_type: "user" | "agent" | "system";
  sender_id: string;
  sender_member_id?: string | null;
  sender_name?: string | null;
  text: string;
  mentions: string[];
  hop_count: number;
  attachments?: Attachment[];
  created_at: string;
}

export interface GroupFileReference {
  id: string;
  group_id: string;
  member_id: string;
  member_name: string;
  agent_id: string;
  session_id: string;
  connection_id: string;
  workspace: string;
  path: string;
  aliases: string[];
  name: string;
  content_key: string;
  created_at: string;
  evidence_id?: string;
}

export interface GroupFiles {
  items: GroupFileReference[];
  references: GroupFileReference[];
  checking: boolean;
  error?: string;
}

export type SessionSwarmMemberState = "working" | "waiting" | "stopped" | "killed";

export interface SessionSwarmSummary {
  total: number;
  working: number;
  waiting: number;
  stopped: number;
  killed: number;
}

export interface SessionSwarmMember {
  name: string;
  role: string;
  is_coordinator: boolean;
  conversation_id: string;
  parent_conversation_id?: string;
  agent_id?: string;
  in_catalog?: boolean;
  model?: string;
  workspace_mode?: string;
  state: SessionSwarmMemberState;
  brief?: string;
  title?: string;
  last_message?: string;
  updated_at?: string;
  spawned_by?: string;
  nesting_depth?: number;
}

export interface SessionSwarmBoardItem {
  id: string;
  kind: "brief" | "message" | "model_change" | "kill" | string;
  author: string;
  author_conversation_id?: string;
  targets?: string[];
  text: string;
  created_at?: string;
}

export interface SessionSwarmMemberMessage {
  id: string;
  role: string;
  kind: string;
  text: string;
  created_at?: string;
  tool_name?: string;
  tool_status?: string;
}

export interface SessionSwarm {
  supported: boolean;
  available: boolean;
  active: boolean;
  needs_daemon_upgrade?: boolean;
  kind: "swarm" | "subagents" | "none" | string;
  title: string;
  coordinator_session_id: string;
  current_session_id: string;
  is_coordinator: boolean;
  summary: SessionSwarmSummary;
  members: SessionSwarmMember[];
  board: SessionSwarmBoardItem[];
  updated_at?: string;
  member_conversation_id?: string;
  member_messages?: SessionSwarmMemberMessage[];
}

export type SessionScheduleKind = "once" | "interval" | "daily" | "weekly" | "cron";
export type SessionScheduleWhenBusy = "skip" | "queue";
export type SessionScheduleMisfirePolicy = "grace_once" | "skip" | "run_once";

export interface SessionSchedule {
  id: string;
  agent_id: string;
  session_id: string;
  name: string;
  prompt: string;
  enabled: boolean;
  schedule_kind: SessionScheduleKind;
  cron_expr: string;
  interval_minutes: number;
  daily_time: string;
  weekday_mask: number;
  run_at?: string;
  timezone: string;
  when_busy: SessionScheduleWhenBusy;
  misfire_policy: SessionScheduleMisfirePolicy;
  misfire_grace_sec: number;
  next_run_at?: string;
  last_run_at?: string;
  last_status: string;
  last_error: string;
  last_command_id: string;
  consecutive_failures: number;
  pause_reason: string;
  created_at: string;
  updated_at: string;
  session_title?: string;
  session_status?: string;
  session_approval_mode?: string;
  agent_name?: string;
  agent_kind?: string;
}

export interface SessionScheduleInput {
  agent_id: string;
  name?: string;
  prompt: string;
  enabled?: boolean;
  schedule_kind: SessionScheduleKind;
  cron_expr?: string;
  interval_minutes?: number;
  daily_time?: string;
  weekday_mask?: number;
  run_at?: string;
  timezone?: string;
  when_busy?: SessionScheduleWhenBusy;
  misfire_policy?: SessionScheduleMisfirePolicy;
  misfire_grace_sec?: number;
}

export interface SessionSchedulePatch {
  name?: string;
  prompt?: string;
  enabled?: boolean;
  schedule_kind?: SessionScheduleKind;
  cron_expr?: string;
  interval_minutes?: number;
  daily_time?: string;
  weekday_mask?: number;
  run_at?: string;
  timezone?: string;
  when_busy?: SessionScheduleWhenBusy;
  misfire_policy?: SessionScheduleMisfirePolicy;
  misfire_grace_sec?: number;
}

export interface SessionSchedulePreview {
  valid: boolean;
  timezone: string;
  next_runs: string[];
}

export interface SessionScheduleRunResult {
  schedule: SessionSchedule;
  outcome: string;
  command_id?: string;
  detail?: string;
}
