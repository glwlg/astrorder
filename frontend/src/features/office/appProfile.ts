import { useSyncExternalStore } from 'react'
import type { Session } from '../../domain/types'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { writeStoredSettingsMode } from '../settings/settingsMode'
import {
  isProjectInOfficeMode,
  isSessionInOfficeMode,
  useOfficeModeRegistry,
} from './officeWorkspaceMode'

export type AppProfile = 'office' | 'developer'

export const APP_PROFILE_PLUGIN_KEY = 'app-profile'
export const APP_PROFILE_STORAGE_KEY = 'astrorder:app-profile'
export const APP_PROFILE_SYNC_KEY = 'astrorder:app-profile:sync'

const listeners = new Set<() => void>()

function emitChange() {
  for (const listener of listeners) {
    listener()
  }
}

function normalizeProfile(raw: unknown): AppProfile | null {
  if (raw === 'office' || raw === 'developer') return raw
  return null
}

export function readStoredAppProfile(): AppProfile | null {
  if (typeof localStorage === 'undefined') return null
  try {
    return normalizeProfile(localStorage.getItem(APP_PROFILE_STORAGE_KEY))
  } catch {
    return null
  }
}

export function hasExplicitAppProfile(): boolean {
  const fromPlugin = normalizeProfile(
    usePluginSettingsStore.getState()?.pluginConfigs?.[APP_PROFILE_PLUGIN_KEY]?.mode,
  )
  if (fromPlugin) return true
  return readStoredAppProfile() !== null
}

export function getAppProfile(): AppProfile {
  const fromPlugin = normalizeProfile(
    usePluginSettingsStore.getState()?.pluginConfigs?.[APP_PROFILE_PLUGIN_KEY]?.mode,
  )
  if (fromPlugin) return fromPlugin
  return readStoredAppProfile() ?? 'developer'
}

export function setAppProfile(next: AppProfile, options?: { syncPluginStore?: boolean }) {
  if (typeof localStorage !== 'undefined') {
    try {
      localStorage.setItem(APP_PROFILE_STORAGE_KEY, next)
      localStorage.setItem(APP_PROFILE_SYNC_KEY, String(Date.now()))
    } catch {}
  }
  if (next === 'office') {
    // Keep Settings mode converged with global Office Profile so mode switches never conflict.
    writeStoredSettingsMode('basic')
  }
  if (options?.syncPluginStore !== false) {
    usePluginSettingsStore.getState().setPluginConfig(APP_PROFILE_PLUGIN_KEY, 'mode', next)
  }
  emitChange()
}

export function clearStoredAppProfileForTest() {
  if (typeof localStorage !== 'undefined') {
    try {
      localStorage.removeItem(APP_PROFILE_STORAGE_KEY)
      localStorage.removeItem(APP_PROFILE_SYNC_KEY)
    } catch {}
  }
  emitChange()
}

function subscribe(callback: () => void): () => void {
  listeners.add(callback)
  const unsubStore =
    typeof usePluginSettingsStore.subscribe === 'function'
      ? usePluginSettingsStore.subscribe(callback)
      : () => {}
  const onStorage = (event: StorageEvent) => {
    if (event.key === APP_PROFILE_STORAGE_KEY || event.key === APP_PROFILE_SYNC_KEY) {
      callback()
    }
  }
  if (typeof window !== 'undefined') {
    window.addEventListener('storage', onStorage)
  }
  return () => {
    listeners.delete(callback)
    unsubStore()
    if (typeof window !== 'undefined') {
      window.removeEventListener('storage', onStorage)
    }
  }
}

export function useAppProfile(): AppProfile {
  return useSyncExternalStore(subscribe, getAppProfile, () => 'developer')
}

export function useIsOfficeProfile(): boolean {
  return useAppProfile() === 'office'
}

/**
 * True when either the entire app is in Office Profile OR the specific session
 * belongs to a workspace marked as Office Space.
 */
export function useIsOfficeExperience(
  session?: Pick<Session, 'workspace' | 'worktree_root_workspace' | 'project_id' | 'source_id' | 'connection_id' | 'agent_id'> | null,
): boolean {
  const profile = useAppProfile()
  const registry = useOfficeModeRegistry()
  if (profile === 'office') return true
  return isSessionInOfficeMode(registry, session)
}

export function useIsProjectOfficeExperience(
  projectKey?: string | null,
  workspace?: string | null,
): boolean {
  const profile = useAppProfile()
  const registry = useOfficeModeRegistry()
  if (profile === 'office') return true
  return isProjectInOfficeMode(registry, projectKey, workspace)
}

/**
 * Navigation item IDs hidden in global Office Profile so office users see only
 * 对话、待办、会议、复盘 + 常用网站 + 设置.
 */
export const DEV_ONLY_PRIMARY_NAV_IDS = new Set<string>([
  'bots',
  'monitor',
  'groups',
  'analytics',
  'backups',
  'agentAccounts',
  'learning',
])
