export type Capability =
  | 'chat'
  | 'stop'
  | 'queue'
  | 'attachments'
  | 'approvals'
  | 'launch'
  | 'history'
  | 'events'

export type ApprovalMode = 'manual' | 'auto' | 'full_access'

export type AgentKind = string
export type AgentStatus = 'disconnected' | 'connecting' | 'ready' | 'error'
export type SessionStatus = 'idle' | 'running' | 'waiting_approval' | 'error'
export type MessageRole = 'user' | 'assistant' | 'system' | 'tool'
export type MessageKind = 'message' | 'thinking' | 'tool'
export type CommandAction = 'send' | 'enqueue' | 'stop' | 'approve' | 'cancel'
export type CommandState =
  | 'received'
  | 'queued'
  | 'accepted'
  | 'running'
  | 'completed'
  | 'failed'
  | 'unknown'
  | 'cancelled'

export interface Agent {
  id: string
  kind: AgentKind
  name: string
  status: AgentStatus
  capabilities: string[]
  limitation: string | null
  source_id?: string
  connection_id?: string | null
  profile_name?: string | null
  runtime_id?: string
}

export interface Session {
  native_kind?: string | null
  ephemeral?: boolean
  is_open?: boolean
  id: string
  agent_id: string
  title: string
  workspace: string | null
  status: SessionStatus
  updated_at: string
  last_user_at?: string
  live?: boolean
  source_id?: string
  connection_id?: string | null
  source_session_id?: string
  project_id?: string | null
  project_name?: string | null
  history_state?: string
  control_state?: string
  handoff_from_agent_id?: string | null
  handoff_from_session_id?: string | null
  parent_session_id?: string | null
  parent_agent_id?: string | null
  parent_session_key?: string | null
}

export interface Project {
  id: string
  source_id: string
  connection_id?: string | null
  agent_id?: string | null
  profile_name?: string | null
  project_id: string
  project_name?: string | null
  workspace?: string | null
  session_count: number
  updated_at: string
}

export interface Attachment {
  id: string
  name: string
  media_type: string
  url: string
}

export interface Message {
  id: string
  session_id: string
  agent_id: string
  role: MessageRole
  kind: MessageKind
  text: string
  attachments: Attachment[]
  created_at: string
  command_id: string | null
  tool: Record<string, unknown> | null
}

export interface Command {
  id: string
  session_id: string
  agent_id: string
  action: CommandAction
  state: CommandState
  text: string
  attachments: Attachment[]
  created_at: string
  error: string | null
  target_id?: string | null
}

export interface AgentCommand {
  name: string
  description: string
  input_hint: string | null
}

export interface AgentMention {
  name: string
  description: string
  kind: 'skill' | 'file'
  path: string
}

export interface CommandPayload {
  id: string
  agent_id: string
  session_id: string
  action: CommandAction
  text: string
  attachment_ids: string[]
  target_id: string | null
}

export type TaskKind = 'background' | 'todo' | 'subagent' | 'tool'
export type TaskStatus = 'pending' | 'running' | 'waiting_approval' | 'completed' | 'failed' | 'cancelled' | 'unknown'

export interface TaskLog {
  id: string
  text: string
  level: string
  created_at: string
}

export interface Task {
  id: string
  session_id: string
  agent_id: string
  kind: TaskKind
  title: string
  status: TaskStatus
  progress: Record<string, unknown> | null
  command: string | null
  logs: TaskLog[]
  target_id: string | null
  created_at: string
  updated_at: string
}

export interface EventEnvelope {
  id: string
  cursor: number
  type: string
  agent_id: string | null
  session_id: string | null
  data: Record<string, unknown>
}

export interface BootstrapPayload {
  approvals?: Approval[]
  protocol_version: number
  agents: Agent[]
  projects?: Project[]
  sessions: Session[]
  tasks?: Task[]
  cursor: number
}

export interface Approval {
  id: string
  agent_id: string | null
  session_id: string | null
  title: string
  detail: string
  state: string
  target_id: string | null
  data: Record<string, unknown>
}

export type EventNotificationKind = 'task_completed' | 'task_failed' | 'approval_pending'

export interface EventNotification {
  key: string
  kind: EventNotificationKind
  title: string
  message: string
  agent_id: string
  session_id: string
  created_at: string
}

export interface RuntimeItem {
  kind: string
  available: boolean
  capabilities: string[]
  reason: string | null
}

export interface RuntimePayload {
  items: RuntimeItem[]
}

export type LocalHermesConnectionState =
  | 'discovered'
  | 'installed'
  | 'connecting'
  | 'connected'
  | 'offline'
  | 'error'

export interface LocalHermesConnection {
  kind: 'hermes'
  state: LocalHermesConnectionState
  available: boolean
  version: string | null
  agent_id: string | null
  source_id?: string
  profile_name?: string
  runtime_id?: string | null
  session_id: string | null
  detail: string
}

export interface SshConnectionSettings {
  connection_id?: string | null
  display_name?: string | null
  profile_name?: string | null
  host: string | null
  port: number
  user: string | null
  ssh_config_alias: string | null
  identity_file: string | null
  hermes_path: string | null
  workspace: string | null
}

export interface ConnectionHistoryEntry {
  id: string
  connection_id: string
  stage: string
  state: string
  detail: string
  details: Record<string, unknown>
  created_at: string
}

export interface SshConnection {
  id: string
  display_name: string
  profile_name: string
  state: 'unconfigured' | 'configured' | 'validated' | 'connecting' | 'connected' | 'disconnected' | 'error'
  settings: SshConnectionSettings | null
  detail: string
  remote_os: string | null
  agent_id: string | null
  runtime_id: string | null
}

export interface SshConnectionsCollection {
  items: SshConnection[]
  state: SshConnection['state']
  settings: SshConnectionSettings | null
  detail: string
}

export interface ConnectionsPayload {
  local: LocalHermesConnection
  ssh: SshConnectionsCollection
}

export interface AuthSession {
  authenticated: boolean
}

export interface DraftAttachment {
  key: string
  file: File
  attachment?: Attachment
  error?: string
}

export interface DraftState {
  text: string
  attachments: DraftAttachment[]
  sessionRefs?: SessionRef[]
}

export interface SessionRef {
  key: string
  agent_id: string
  id: string
  title: string
}

export type OutboxStatus = 'submitting' | CommandState

export interface OutboxEntry {
  command: Command
  status: OutboxStatus
  error: string | null
}

export type ConnectionStatus = 'disconnected' | 'connecting' | 'connected' | 'error'


export interface BotGroupMember {
  machine_id: string
  agent_id: string
  name?: string | null
  alias?: string | null
  system_role_prompt?: string | null
}

export interface BotGroup {
  id: string
  name: string
  description?: string | null
  members: BotGroupMember[]
  max_hops: number
  active_hop?: number
  active_speaker_agent_id?: string | null
  active_speakers?: string[]
  created_at: string
  updated_at: string
}

export interface GroupMessage {
  id: string
  group_id: string
  sender_type: 'user' | 'agent'
  sender_id: string
  sender_name?: string | null
  text: string
  mentions: string[]
  hop_count: number
  attachments?: string[]
  created_at: string
}
