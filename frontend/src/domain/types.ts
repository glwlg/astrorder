export type Capability =
  | 'chat'
  | 'stop'
  | 'queue'
  | 'attachments'
  | 'approvals'
  | 'launch'
  | 'history'
  | 'events'

export type AgentKind = 'hermes' | 'codex'
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
}

export interface Session {
  id: string
  agent_id: string
  title: string
  workspace: string | null
  status: SessionStatus
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

export interface CommandPayload {
  id: string
  agent_id: string
  session_id: string
  action: CommandAction
  text: string
  attachment_ids: string[]
  target_id: string | null
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
  protocol_version: number
  agents: Agent[]
  sessions: Session[]
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

export interface RuntimeItem {
  kind: string
  available: boolean
  capabilities: string[]
  reason: string | null
}

export interface RuntimePayload {
  items: RuntimeItem[]
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
}

export type OutboxStatus = 'submitting' | CommandState

export interface OutboxEntry {
  command: Command
  status: OutboxStatus
  error: string | null
}

export type ConnectionStatus = 'disconnected' | 'connecting' | 'connected' | 'error'
