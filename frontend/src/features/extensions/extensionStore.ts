import { create } from 'zustand'
import {
  extensionsApi,
  type PluginView,
  type SetExtensionEnabledResult,
} from '../../api/extensions'
import { PLUGIN_SETTINGS_SYNC_KEY, usePluginSettingsStore } from '../plugins/pluginSettingsStore'

export const EXTENSION_STORE_SYNC_KEY = 'astrorder:extensions:sync'

interface ExtensionSyncPayload {
  ts: number
  action: 'setEnabled' | 'install' | 'uninstall' | 'restart' | 'reload'
  id?: string
  enabled?: boolean
}

function broadcastExtensionSync(
  action: ExtensionSyncPayload['action'],
  extra?: { id?: string; enabled?: boolean; syncPluginSettings?: boolean },
) {
  if (typeof localStorage === 'undefined') return
  const now = Date.now()
  try {
    const payload: ExtensionSyncPayload = {
      ts: now,
      action,
      ...(extra?.id ? { id: extra.id } : {}),
      ...(extra?.enabled !== undefined ? { enabled: extra.enabled } : {}),
    }
    localStorage.setItem(EXTENSION_STORE_SYNC_KEY, JSON.stringify(payload))
    if (extra?.syncPluginSettings) {
      localStorage.setItem(PLUGIN_SETTINGS_SYNC_KEY, String(now))
    }
  } catch {
    // ignore storage failures in restricted browsing contexts
  }
}

export type EsmPluginLoadStatus = 'idle' | 'loading' | 'loaded' | 'error'

export interface EsmPluginRuntimeState {
  status: EsmPluginLoadStatus
  digest?: string
  error?: string | null
  updatedAt: number
}

export interface ExtensionStoreState {
  items: PluginView[]
  loading: boolean
  hydrated: boolean
  error: string | null
  esmStates: Record<string, EsmPluginRuntimeState>
  hydrate: (force?: boolean) => Promise<void>
  refresh: () => Promise<void>
  reload: () => Promise<PluginView[]>
  installFromPath: (sourcePath: string) => Promise<PluginView>
  installArchive: (file: File) => Promise<PluginView>
  uninstall: (id: string) => Promise<void>
  restart: (id: string) => Promise<PluginView>
  setEnabled: (
    id: string,
    enabled: boolean,
    grantPermissions?: string[],
  ) => Promise<SetExtensionEnabledResult>
  setEsmRuntimeState: (id: string, next: EsmPluginRuntimeState | null) => void
  getById: (id: string) => PluginView | undefined
  reset: () => void
}

let hydrateInFlight: Promise<void> | null = null

function upsertPlugin(items: PluginView[], next: PluginView): PluginView[] {
  const index = items.findIndex((item) => item.id === next.id)
  if (index === -1) {
    return [...items, next]
  }
  const copy = [...items]
  copy[index] = next
  return copy
}

function syncPluginSettingsEnabled(id: string, enabled: boolean, broadcast = true) {
  usePluginSettingsStore.setState((state) => ({
    enabledPlugins: {
      ...state.enabledPlugins,
      [id]: enabled,
    },
  }))
  if (broadcast) {
    broadcastExtensionSync('setEnabled', { id, enabled, syncPluginSettings: true })
  }
}

function removePluginSettingsEnabled(id: string) {
  usePluginSettingsStore.setState((state) => {
    if (!(id in state.enabledPlugins)) return state
    const nextEnabled = { ...state.enabledPlugins }
    delete nextEnabled[id]
    return { enabledPlugins: nextEnabled }
  })
}

function syncManagedPluginsEnabled(items: PluginView[]) {
  const managed = items.filter((item) => item.source === 'official' || item.source === 'external')
  if (managed.length === 0) return
  usePluginSettingsStore.setState((state) => {
    let changed = false
    const nextEnabled = { ...state.enabledPlugins }
    for (const item of managed) {
      if (nextEnabled[item.id] !== item.enabled) {
        nextEnabled[item.id] = item.enabled
        changed = true
      }
    }
    return changed ? { enabledPlugins: nextEnabled } : state
  })
}

export const useExtensionStore = create<ExtensionStoreState>((set, get) => ({
  items: [],
  loading: false,
  hydrated: false,
  error: null,
  esmStates: {},

  hydrate: async (force = false) => {
    if (!force && get().hydrated) return
    if (hydrateInFlight) return hydrateInFlight
    set({ loading: true, error: null })
    hydrateInFlight = (async () => {
      try {
        const res = await extensionsApi.listExtensions()
        const items = Array.isArray(res?.items) ? res.items : []
        syncManagedPluginsEnabled(items)
        set({ items, loading: false, hydrated: true, error: null })
      } catch (err) {
        const message = err instanceof Error ? err.message : '加载扩展列表失败'
        set({ loading: false, error: message })
      } finally {
        hydrateInFlight = null
      }
    })()
    return hydrateInFlight
  },

  refresh: async () => {
    await get().hydrate(true)
  },

  reload: async () => {
    set({ loading: true, error: null })
    try {
      const res = await extensionsApi.reloadExtensions()
      const items = Array.isArray(res?.items) ? res.items : []
      syncManagedPluginsEnabled(items)
      set({ items, loading: false, hydrated: true, error: null })
      broadcastExtensionSync('reload')
      return items
    } catch (err) {
      const message = err instanceof Error ? err.message : '重新扫描扩展目录失败'
      set({ loading: false, error: message })
      throw err
    }
  },

  installFromPath: async (sourcePath: string) => {
    const res = await extensionsApi.installExtensionFromPath(sourcePath)
    if (!res?.item) {
      throw new Error('安装响应缺少扩展信息')
    }
    set((state) => ({ items: upsertPlugin(state.items, res.item), hydrated: true, error: null }))
    broadcastExtensionSync('install', { id: res.item.id, enabled: res.item.enabled })
    return res.item
  },

  installArchive: async (file: File) => {
    const res = await extensionsApi.installExtensionArchive(file)
    if (!res?.item) {
      throw new Error('安装响应缺少扩展信息')
    }
    set((state) => ({ items: upsertPlugin(state.items, res.item), hydrated: true, error: null }))
    broadcastExtensionSync('install', { id: res.item.id, enabled: res.item.enabled })
    return res.item
  },

  uninstall: async (id: string) => {
    await extensionsApi.uninstallExtension(id)
    set((state) => ({
      items: state.items.filter((item) => item.id !== id),
    }))
    removePluginSettingsEnabled(id)
    broadcastExtensionSync('uninstall', { id, syncPluginSettings: true })
  },

  restart: async (id: string) => {
    const res = await extensionsApi.restartExtension(id)
    if (!res?.item) {
      throw new Error('重启响应缺少扩展信息')
    }
    set((state) => ({ items: upsertPlugin(state.items, res.item) }))
    broadcastExtensionSync('restart', { id: res.item.id, enabled: res.item.enabled })
    return res.item
  },

  setEnabled: async (id: string, enabled: boolean, grantPermissions?: string[]) => {
    const result = await extensionsApi.setExtensionEnabled(id, enabled, grantPermissions)
    if (result.item) {
      set((state) => ({ items: upsertPlugin(state.items, result.item!) }))
    }
    if (!result.requires_permission_grant) {
      const effectiveEnabled = result.item ? result.item.enabled : enabled
      syncPluginSettingsEnabled(id, effectiveEnabled, true)
    }
    return result
  },

  setEsmRuntimeState: (id: string, next: EsmPluginRuntimeState | null) => {
    set((state) => {
      if (next === null) {
        if (!(id in state.esmStates)) return state
        const copy = { ...state.esmStates }
        delete copy[id]
        return { esmStates: copy }
      }
      return {
        esmStates: {
          ...state.esmStates,
          [id]: next,
        },
      }
    })
  },

  getById: (id: string) => get().items.find((item) => item.id === id),

  reset: () => {
    hydrateInFlight = null
    set({ items: [], loading: false, hydrated: false, error: null, esmStates: {} })
  },
}))

function handleExtensionStorageEvent(event: StorageEvent) {
  if (event.key === EXTENSION_STORE_SYNC_KEY) {
    if (event.newValue) {
      try {
        const parsed = JSON.parse(event.newValue) as Partial<ExtensionSyncPayload>
        if (parsed.action === 'setEnabled' && parsed.id && typeof parsed.enabled === 'boolean') {
          syncPluginSettingsEnabled(parsed.id, parsed.enabled, false)
          useExtensionStore.setState((state) => ({
            items: state.items.map((item) =>
              item.id === parsed.id ? { ...item, enabled: parsed.enabled! } : item,
            ),
          }))
        } else if (parsed.action === 'uninstall' && parsed.id) {
          removePluginSettingsEnabled(parsed.id)
          useExtensionStore.setState((state) => ({
            items: state.items.filter((item) => item.id !== parsed.id),
          }))
        }
      } catch {
        // ignore malformed payload and still rehydrate below
      }
    }
    void useExtensionStore.getState().hydrate(true)
    return
  }

  if (event.key === PLUGIN_SETTINGS_SYNC_KEY) {
    void usePluginSettingsStore.getState().hydrate(true)
    if (useExtensionStore.getState().hydrated) {
      void useExtensionStore.getState().hydrate(true)
    }
  }
}

let storageSyncBound = false

export function ensureExtensionStoreCrossWindowSync() {
  if (storageSyncBound || typeof window === 'undefined') return
  storageSyncBound = true
  window.addEventListener('storage', handleExtensionStorageEvent)
}

ensureExtensionStoreCrossWindowSync()
