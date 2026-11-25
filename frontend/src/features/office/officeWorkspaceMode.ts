import type { Session } from '../../domain/types'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'

export const OFFICE_WORKSPACE_MODE_CONFIG_KEY = 'office-workspace-mode'
const STORAGE_KEY = 'astrorder:office-workspace-mode'

export interface OfficeModeRegistry {
  keys: string[]
  workspaces: string[]
}

function normalizeWorkspacePath(raw?: string | null): string {
  if (!raw) return ''
  let norm = raw.trim().replaceAll('\\', '/').replace(/\/+$/, '')
  if (/^\/\/\?\/[a-zA-Z]:\//.test(norm)) {
    norm = norm.slice(4)
  }
  return norm.toLowerCase()
}

function normalizeRegistry(raw: unknown): OfficeModeRegistry {
  if (!raw || typeof raw !== 'object') return { keys: [], workspaces: [] }
  const obj = raw as Record<string, unknown>
  const keys = Array.isArray(obj.keys)
    ? obj.keys.filter((v): v is string => typeof v === 'string' && v.trim().length > 0)
    : []
  const workspaces = Array.isArray(obj.workspaces)
    ? obj.workspaces
        .filter((v): v is string => typeof v === 'string' && v.trim().length > 0)
        .map((v) => normalizeWorkspacePath(v))
        .filter(Boolean)
    : []
  return {
    keys: Array.from(new Set(keys)),
    workspaces: Array.from(new Set(workspaces)),
  }
}

export function readStoredOfficeModeRegistry(): OfficeModeRegistry {
  if (typeof localStorage === 'undefined') return { keys: [], workspaces: [] }
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { keys: [], workspaces: [] }
    return normalizeRegistry(JSON.parse(raw))
  } catch {
    return { keys: [], workspaces: [] }
  }
}

function writeStoredOfficeModeRegistry(reg: OfficeModeRegistry) {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(reg))
  } catch {}
}

function resolveActiveRegistry(configured: unknown): OfficeModeRegistry {
  const fromPlugin = normalizeRegistry(configured)
  if (fromPlugin.keys.length > 0 || fromPlugin.workspaces.length > 0) {
    return fromPlugin
  }
  return readStoredOfficeModeRegistry()
}

export function isProjectInOfficeMode(
  registry: OfficeModeRegistry,
  projectKey?: string | null,
  workspace?: string | null,
): boolean {
  if (projectKey && registry.keys.includes(projectKey)) {
    return true
  }
  const normWs = normalizeWorkspacePath(workspace)
  if (normWs && registry.workspaces.includes(normWs)) {
    return true
  }
  return false
}

export function isSessionInOfficeMode(
  registry: OfficeModeRegistry,
  session?: Pick<Session, 'workspace' | 'worktree_root_workspace' | 'project_id' | 'source_id' | 'connection_id' | 'agent_id'> | null,
): boolean {
  if (!session) return false
  const ws = normalizeWorkspacePath(session.worktree_root_workspace || session.workspace)
  if (ws && registry.workspaces.includes(ws)) {
    return true
  }
  if (session.project_id) {
    const suffix = `\u0000${session.project_id}`
    if (registry.keys.some((k) => k === session.project_id || k.endsWith(suffix))) {
      return true
    }
  }
  return false
}

export function setProjectOfficeMode(
  params: { projectKey: string; workspace?: string | null },
  enabled: boolean,
): OfficeModeRegistry {
  const store = usePluginSettingsStore.getState()
  const current = resolveActiveRegistry(store.pluginConfigs[OFFICE_WORKSPACE_MODE_CONFIG_KEY]?.projects)
  const keys = new Set(current.keys)
  const workspaces = new Set(current.workspaces)
  const normWs = normalizeWorkspacePath(params.workspace)

  if (enabled) {
    if (params.projectKey) keys.add(params.projectKey)
    if (normWs) workspaces.add(normWs)
  } else {
    if (params.projectKey) keys.delete(params.projectKey)
    if (normWs) workspaces.delete(normWs)
  }

  const next: OfficeModeRegistry = {
    keys: Array.from(keys),
    workspaces: Array.from(workspaces),
  }
  writeStoredOfficeModeRegistry(next)
  store.setPluginConfig(OFFICE_WORKSPACE_MODE_CONFIG_KEY, 'projects', next)
  return next
}

export function useOfficeModeRegistry(): OfficeModeRegistry {
  const configured = usePluginSettingsStore((s) => s.pluginConfigs[OFFICE_WORKSPACE_MODE_CONFIG_KEY]?.projects)
  return resolveActiveRegistry(configured)
}

export function useIsSessionOfficeMode(session?: Session | null): boolean {
  const registry = useOfficeModeRegistry()
  return isSessionInOfficeMode(registry, session)
}
