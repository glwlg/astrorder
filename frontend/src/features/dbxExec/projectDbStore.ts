import { create } from 'zustand'
import {
  api,
  type DbxConnectionSummary,
  type ProjectDatabaseBinding,
  type ProjectDatabaseTarget,
} from '../../api/client'
import type { Agent, Project, Session } from '../../domain/types'

export interface ProjectDatabaseIdentity {
  projectKey: string
  workspaceKey?: string
  projectLabel: string
}

function normalizeWorkspacePath(value: string | null | undefined): string | null {
  if (!value) return null
  const windows = /^[a-zA-Z]:[\\/]/.test(value) || value.startsWith('\\\\') || value.startsWith('//')
  if (!windows) return value.replace(/\/+$/, '') || '/'
  let normalized = value.replaceAll('\\', '/')
  if (/^\/\/\?\/UNC\//i.test(normalized)) normalized = '//' + normalized.slice(8)
  else if (/^\/\/\?\/[a-zA-Z]:\//.test(normalized)) normalized = normalized.slice(4)
  return normalized.replace(/\/+$/, '').toLowerCase()
}

function resolveConnectionScope(
  sourceId: string | null | undefined,
  connectionId: string | null | undefined,
  agent?: Agent,
): string {
  const connection = connectionId || agent?.connection_id
  if (connection) return `connection:${connection}`
  if (
    agent ||
    !sourceId ||
    sourceId === 'local' ||
    sourceId === 'local-codex' ||
    sourceId === 'local-antigravity' ||
    sourceId === 'local-grok' ||
    sourceId.startsWith('local-hermes-') ||
    sourceId.startsWith('hermes-local-')
  ) {
    return 'connection:local'
  }
  return `source:${sourceId}`
}

export function buildWorkspaceScopeKey(
  workspace: string | null | undefined,
  connectionId: string | null | undefined,
  sourceId?: string | null,
  agent?: Agent,
): string | undefined {
  const ws = normalizeWorkspacePath(workspace)
  if (!ws) return undefined
  return `${resolveConnectionScope(sourceId, connectionId, agent)}\u0000${ws}`
}

export function resolveProjectDatabaseIdentity(
  session: Session | null | undefined,
  projects: Record<string, Project>,
  agents: Record<string, Agent>,
): ProjectDatabaseIdentity | null {
  if (!session) return null
  const agent = agents[session.agent_id]
  const source =
    session.source_id ||
    agent?.source_id ||
    session.connection_id ||
    agent?.connection_id ||
    session.agent_id ||
    'unknown-source'
  const effectiveWorkspace = session.worktree_root_workspace || session.workspace
  const workspaceKey = buildWorkspaceScopeKey(effectiveWorkspace, session.connection_id, source, agent)

  const projectList = Object.values(projects)
  const nativeKey =
    session.project_id && session.project_id !== '__no_project__'
      ? `project:${source}\u0000${session.project_id}`
      : ''

  let matchedProject: Project | undefined
  if (nativeKey) {
    matchedProject = projectList.find(
      (p) =>
        p.id === nativeKey ||
        (`project:${p.source_id || p.connection_id || p.agent_id || 'unknown-source'}\u0000${p.project_id}` ===
          nativeKey),
    )
  }
  if (!matchedProject && workspaceKey) {
    matchedProject = projectList.find((p) => {
      if (p.project_id === '__no_project__') return false
      const pAgent = p.agent_id ? agents[p.agent_id] : undefined
      const pSource = p.source_id || pAgent?.source_id || p.connection_id || pAgent?.connection_id || p.agent_id || ''
      const pWsKey = buildWorkspaceScopeKey(p.workspace, p.connection_id, pSource, pAgent)
      return pWsKey === workspaceKey
    })
  }

  const projectKey = matchedProject
    ? matchedProject.id ||
      `project:${matchedProject.source_id || source}\u0000${matchedProject.project_id}`
    : workspaceKey
      ? `workspace:${workspaceKey}`
      : nativeKey || `unmarked:${source}`

  const basename = effectiveWorkspace
    ? effectiveWorkspace.replaceAll('\\', '/').replace(/\/+$/, '').split('/').filter(Boolean).pop() || effectiveWorkspace
    : ''
  const projectLabel = matchedProject?.project_name || session.project_name || basename || '当前项目'

  return {
    projectKey,
    workspaceKey,
    projectLabel,
  }
}

interface ProjectDbState {
  bindings: ProjectDatabaseBinding[]
  connections: DbxConnectionSummary[]
  hydrated: boolean
  loadingBindings: boolean
  loadingConnections: boolean
  connectionsError: string
  sessionTargetOverride: Record<string, string> // sessionScopeKey -> target.id
  hydrateBindings: (force?: boolean) => Promise<void>
  refreshConnections: () => Promise<DbxConnectionSummary[]>
  saveBinding: (binding: ProjectDatabaseBinding) => Promise<ProjectDatabaseBinding>
  deleteBinding: (projectKey: string) => Promise<void>
  setSessionTargetOverride: (sessionKey: string, targetId: string) => void
  findBinding: (projectKey?: string, workspaceKey?: string) => ProjectDatabaseBinding | null
}

let hydratePromise: Promise<void> | null = null

export const useProjectDbStore = create<ProjectDbState>((set, get) => ({
  bindings: [],
  connections: [],
  hydrated: false,
  loadingBindings: false,
  loadingConnections: false,
  connectionsError: '',
  sessionTargetOverride: {},

  hydrateBindings: async (force = false) => {
    if (!force && get().hydrated) return
    if (hydratePromise && !force) return hydratePromise
    set({ loadingBindings: true })
    hydratePromise = api
      .getProjectDatabaseBindings()
      .then((res) => {
        set({
          bindings: Array.isArray(res.bindings) ? res.bindings : [],
          hydrated: true,
          loadingBindings: false,
        })
      })
      .catch(() => {
        set({ loadingBindings: false })
      })
      .finally(() => {
        hydratePromise = null
      })
    return hydratePromise
  },

  refreshConnections: async () => {
    set({ loadingConnections: true, connectionsError: '' })
    try {
      const res = await api.listDbxConnections()
      const list = Array.isArray(res.connections) ? res.connections : []
      set({ connections: list, loadingConnections: false, connectionsError: '' })
      return list
    } catch (err) {
      const msg = err instanceof Error ? err.message : '无法读取 DBX 数据库连接'
      set({ loadingConnections: false, connectionsError: msg })
      return []
    }
  },

  saveBinding: async (binding) => {
    const res = await api.saveProjectDatabaseBinding(binding)
    set({
      bindings: Array.isArray(res.bindings) ? res.bindings : get().bindings,
      hydrated: true,
    })
    return res.binding
  },

  deleteBinding: async (projectKey) => {
    const res = await api.deleteProjectDatabaseBinding(projectKey)
    set({
      bindings: Array.isArray(res.bindings) ? res.bindings : get().bindings.filter((b) => b.project_key !== projectKey),
    })
  },

  setSessionTargetOverride: (sessionKey, targetId) => {
    if (!sessionKey) return
    set((state) => ({
      sessionTargetOverride: {
        ...state.sessionTargetOverride,
        [sessionKey]: targetId,
      },
    }))
  },

  findBinding: (projectKey, workspaceKey) => {
    const bindings = get().bindings
    if (projectKey) {
      const exact = bindings.find((b) => b.project_key === projectKey)
      if (exact) return exact
    }
    if (workspaceKey) {
      const lower = workspaceKey.toLowerCase()
      const byWs = bindings.find((b) => b.workspace_key && b.workspace_key.toLowerCase() === lower)
      if (byWs) return byWs
    }
    return null
  },
}))

export function selectActiveProjectTarget(
  binding: ProjectDatabaseBinding | null,
  overrideTargetId?: string,
): ProjectDatabaseTarget | null {
  if (!binding || !binding.targets.length) return null
  if (overrideTargetId) {
    const matched = binding.targets.find((t) => t.id === overrideTargetId)
    if (matched) return matched
  }
  return binding.targets.find((t) => t.is_default) || binding.targets[0] || null
}

export function formatTargetLabel(target: Pick<ProjectDatabaseTarget, 'label' | 'connection_name' | 'database' | 'schema'>): string {
  if (target.label) return target.label
  const parts = [target.connection_name]
  if (target.database) parts.push(target.database)
  if (target.schema) parts.push(target.schema)
  return parts.join(' · ')
}
