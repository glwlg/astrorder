import type { Agent, Project, Session } from '../domain/types'

export type RailFilter = 'all' | 'running' | 'unread' | 'pinned' | 'recent'

export function formatRelativeTime(isoString?: string): string {
  if (!isoString) return ''
  try {
    const d = new Date(isoString)
    const now = new Date()
    const diffMs = now.getTime() - d.getTime()
    if (diffMs < 0 || isNaN(diffMs)) {
      return isoString.slice(5, 10)
    }
    const diffSec = Math.floor(diffMs / 1000)
    if (diffSec < 60) return '刚刚'
    const diffMin = Math.floor(diffSec / 60)
    if (diffMin < 60) return `${diffMin}分钟前`
    const diffHours = Math.floor(diffMin / 60)
    if (diffHours < 24) return `${diffHours}小时前`
    const diffDays = Math.floor(diffHours / 24)
    if (diffDays === 1) return '昨天'
    if (diffDays < 7) return `${diffDays}天前`
    if (d.getFullYear() === now.getFullYear()) {
      const mm = String(d.getMonth() + 1).padStart(2, '0')
      const dd = String(d.getDate()).padStart(2, '0')
      return `${mm}-${dd}`
    }
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  } catch {
    return isoString.slice(0, 10)
  }
}

export interface ProjectGroup {
  key: string
  label: string
  remoteLabel?: string | null
  sessions: Session[]
  sessionCount: number
  latestUpdatedAt: string
  agentId?: string | null
  workspace?: string | null
}

type SessionDecorations = Session & {
  unread?: boolean
  pinned?: boolean
}

function workspaceKey(value: string | null | undefined): string | null {
  if (!value) return null
  const windows = /^[a-zA-Z]:[\\/]/.test(value) || value.startsWith('\\\\') || value.startsWith('//')
  if (!windows) return value.replace(/\/+$/, '') || '/'
  let normalized = value.replaceAll('\\', '/')
  if (/^\/\/\?\/UNC\//i.test(normalized)) normalized = '//' + normalized.slice(8)
  else if (/^\/\/\?\/[a-zA-Z]:\//.test(normalized)) normalized = normalized.slice(4)
  return normalized.replace(/\/+$/, '').toLowerCase()
}

function sourceKey(
  sourceId: string | null | undefined,
  connectionId: string | null | undefined,
  agentId: string | null | undefined,
  agents: Record<string, Agent>,
): string {
  return sourceId || (agentId ? agents[agentId]?.source_id : undefined) || connectionId || (agentId ? agents[agentId]?.connection_id : undefined) || agentId || 'unknown-source'
}

function remoteLabel(
  sourceId: string | null | undefined,
  connectionId: string | null | undefined,
  agent: Agent | undefined,
): string | null {
  const isRemote = Boolean(connectionId || agent?.connection_id || sourceId?.startsWith('hermes-ssh-'))
  if (!isRemote) return null
  return agent?.name?.trim() || connectionId || '远程'
}

function connectionScope(source: string, connectionId: string | null | undefined, agent: Agent | undefined): string {
  const connection = connectionId || agent?.connection_id
  if (connection) return `connection:${connection}`
  if (source === 'local-codex' || source.startsWith('local-hermes-') || source.startsWith('hermes-local-')) return 'connection:local'
  return `source:${source}`
}

function isScheduledSession(session: Session): boolean {
  const title = session.title || ''
  return title.includes('每晚整理') || title.includes('OV 记忆') || title.startsWith('补发柳如烟')
}

function sessionMatches(session: Session, query: string, filter: RailFilter): boolean {
  if (session.native_kind === 'subagent') return false
  if (isScheduledSession(session)) return false
  const decorated = session as SessionDecorations
  const searchable = `${session.title} ${session.workspace || ''} ${session.project_name || ''}`.toLocaleLowerCase()
  if (query && !searchable.includes(query)) return false
  if (filter === 'running') return session.status === 'running'
  if (filter === 'unread') return decorated.unread === true
  if (filter === 'pinned') return decorated.pinned === true
  if (filter === 'recent') { const age = Date.now() - Date.parse(session.updated_at); return Number.isFinite(age) && age >= 0 && age <= 86400000 }
  return true
}

export function buildProjectGroups(
  sessions: Session[],
  agents: Record<string, Agent>,
  projects: Project[] = [],
  query = '',
  filter: RailFilter = 'all',
  _pinnedProjects: string[] = [],
): ProjectGroup[] {
  const groups = new Map<string, ProjectGroup>()
  const workspaceToProject = new Map<string, string>()
  const nameToProject = new Map<string, string>()
  const projectAliases = new Map<string, string>()

  for (const catalogProject of projects) {
    const source = sourceKey(catalogProject.source_id, catalogProject.connection_id, catalogProject.agent_id, agents)
    const key = `project:${source}\u0000${catalogProject.project_id}`
    const catalogWs = workspaceKey(catalogProject.workspace)
    const workspaceScope = `${connectionScope(source, catalogProject.connection_id, agents[catalogProject.agent_id || ''])}\u0000${catalogWs}`
    const existingKey = catalogWs && catalogProject.project_id !== '__no_project__' ? workspaceToProject.get(workspaceScope) : undefined
    if (existingKey) {
      projectAliases.set(key, existingKey)
      const existing = groups.get(existingKey)!
      existing.sessionCount += Math.max(0, catalogProject.session_count)
      continue
    }
    groups.set(key, {
      key,
      label: catalogProject.project_name || catalogProject.workspace || '未标记项目',
      remoteLabel: remoteLabel(catalogProject.source_id, catalogProject.connection_id, agents[catalogProject.agent_id || '']),
      sessions: [],
      sessionCount: Math.max(0, catalogProject.session_count),
      latestUpdatedAt: catalogProject.updated_at || '',
      agentId: catalogProject.agent_id,
      workspace: catalogProject.workspace,
    })
    const ws = workspaceKey(catalogProject.workspace)
    if (ws && catalogProject.project_id !== '__no_project__') {
      workspaceToProject.set(`${connectionScope(source, catalogProject.connection_id, agents[catalogProject.agent_id || ''])}\u0000${ws}`, key)
    }
    const pname = (catalogProject.project_name || '').trim().toLowerCase()
    if (pname && catalogProject.project_id !== '__no_project__') {
      nameToProject.set(`${source}\u0000${pname}`, key)
    }
  }

  for (const session of sessions) {
    if (!sessionMatches(session, query, filter)) continue
    const source = sourceKey(session.source_id, session.connection_id, session.agent_id, agents)
    const pid = session.project_id
    const ws = workspaceKey(session.workspace)
    const location = connectionScope(source, session.connection_id, agents[session.agent_id])
    const pname = (session.project_name || '').trim().toLowerCase()

    let key = `unmarked:${source}`
    const nativeKey = `project:${source}\u0000${pid}`
    if (pid && pid !== '__no_project__' && (groups.has(nativeKey) || projectAliases.has(nativeKey))) {
      key = projectAliases.get(nativeKey) || nativeKey
    } else if (ws) {
      // Paths only identify projects within their native connection/source.
      if (workspaceToProject.has(`${location}\u0000${ws}`)) {
        key = workspaceToProject.get(`${location}\u0000${ws}`)!
      } else {
        key = `workspace:${location}\u0000${ws}`
      }
    } else if (pname && nameToProject.has(`${source}\u0000${pname}`)) {
      key = nameToProject.get(`${source}\u0000${pname}`)!
    } else if (pid === '__no_project__' || pname === 'home') {
      const homeKey = `project:${source}\u0000__no_project__`
      if (groups.has(homeKey)) key = homeKey
    }

    const project = groups.get(key) || {
      key,
      label: session.project_name || session.workspace || '未标记项目',
      remoteLabel: remoteLabel(session.source_id, session.connection_id, agents[session.agent_id]),
      sessions: [] as Session[],
      sessionCount: 0,
      latestUpdatedAt: session.updated_at,
      agentId: session.agent_id,
      workspace: session.workspace,
    }
    if (!project.agentId) project.agentId = session.agent_id
    if (!project.workspace) project.workspace = session.workspace
    if (project.label === session.workspace && session.project_name) project.label = session.project_name
    if (!project.remoteLabel) project.remoteLabel = remoteLabel(session.source_id, session.connection_id, agents[session.agent_id])
    project.sessions.push(session)
    project.sessionCount = Math.max(project.sessionCount, project.sessions.length)
    if (session.updated_at > project.latestUpdatedAt) project.latestUpdatedAt = session.updated_at
    groups.set(key, project)
  }

  return [...groups.values()]
    .filter((project) => {
      if (filter !== 'all') return project.sessions.length > 0
      if (query) return project.sessions.length > 0 || project.label.toLowerCase().includes(query)
      return true
    })
    .map((project) => ({
      ...project,
      sessions: project.sessions,
    }))
    .sort((a, b) => a.label.localeCompare(b.label, 'zh-CN', { sensitivity: 'base', numeric: true }) || a.key.localeCompare(b.key))
}
