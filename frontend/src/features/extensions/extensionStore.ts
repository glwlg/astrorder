import { create } from 'zustand'
import {
  extensionsApi,
  type PluginView,
  type SetExtensionEnabledResult,
} from '../../api/extensions'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'

export interface ExtensionStoreState {
  items: PluginView[]
  loading: boolean
  hydrated: boolean
  error: string | null
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

function syncPluginSettingsEnabled(id: string, enabled: boolean) {
  usePluginSettingsStore.setState((state) => ({
    enabledPlugins: {
      ...state.enabledPlugins,
      [id]: enabled,
    },
  }))
}

export const useExtensionStore = create<ExtensionStoreState>((set, get) => ({
  items: [],
  loading: false,
  hydrated: false,
  error: null,

  hydrate: async (force = false) => {
    if (!force && get().hydrated) return
    if (hydrateInFlight) return hydrateInFlight
    set({ loading: true, error: null })
    hydrateInFlight = (async () => {
      try {
        const res = await extensionsApi.listExtensions()
        const items = Array.isArray(res?.items) ? res.items : []
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
      set({ items, loading: false, hydrated: true, error: null })
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
    return res.item
  },

  installArchive: async (file: File) => {
    const res = await extensionsApi.installExtensionArchive(file)
    if (!res?.item) {
      throw new Error('安装响应缺少扩展信息')
    }
    set((state) => ({ items: upsertPlugin(state.items, res.item), hydrated: true, error: null }))
    return res.item
  },

  uninstall: async (id: string) => {
    await extensionsApi.uninstallExtension(id)
    set((state) => ({
      items: state.items.filter((item) => item.id !== id),
    }))
    usePluginSettingsStore.setState((state) => {
      if (!(id in state.enabledPlugins)) return state
      const nextEnabled = { ...state.enabledPlugins }
      delete nextEnabled[id]
      return { enabledPlugins: nextEnabled }
    })
  },

  restart: async (id: string) => {
    const res = await extensionsApi.restartExtension(id)
    if (!res?.item) {
      throw new Error('重启响应缺少扩展信息')
    }
    set((state) => ({ items: upsertPlugin(state.items, res.item) }))
    return res.item
  },

  setEnabled: async (id: string, enabled: boolean, grantPermissions?: string[]) => {
    const result = await extensionsApi.setExtensionEnabled(id, enabled, grantPermissions)
    if (result.item) {
      set((state) => ({ items: upsertPlugin(state.items, result.item!) }))
    }
    if (!result.requires_permission_grant) {
      const effectiveEnabled = result.item ? result.item.enabled : enabled
      syncPluginSettingsEnabled(id, effectiveEnabled)
    }
    return result
  },

  getById: (id: string) => get().items.find((item) => item.id === id),

  reset: () => {
    hydrateInFlight = null
    set({ items: [], loading: false, hydrated: false, error: null })
  },
}))
