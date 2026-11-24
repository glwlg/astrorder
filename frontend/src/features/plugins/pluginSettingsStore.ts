import { create } from 'zustand'
import { notifications } from '@mantine/notifications'
import { api, type PluginSettingsResponse } from '../../api/client'
import { syncAccentFromPluginConfigs } from '../../themeAccent'

export const SIDEBAR_INTEGRATIONS_PLUGIN_ID = 'sidebar-integrations'

const LEGACY_STORAGE_KEY_ENABLED = 'astrorder:plugins:enabled'
const LEGACY_STORAGE_KEY_CONFIG = 'astrorder:plugins:config'

export interface PluginSettingsState {
  // pluginId -> boolean (是否启用，缺省视为 true)
  enabledPlugins: Record<string, boolean>
  // pluginId -> { optionKey: value }
  pluginConfigs: Record<string, Record<string, unknown>>
  hydrated: boolean
  loading: boolean
  hydrate: (force?: boolean) => Promise<void>
  togglePlugin: (pluginId: string, enabled?: boolean) => void
  setPluginConfig: (pluginId: string, key: string, value: unknown) => void
  setPluginConfigPatch: (pluginId: string, patch: Record<string, unknown>) => void
  isPluginEnabled: (pluginId: string) => boolean
  getPluginConfig: <T = unknown>(pluginId: string, key: string, fallback?: T) => T
  reset: () => void
}

function readLegacyLocalStorage(): { enabled: Record<string, boolean>; configs: Record<string, Record<string, unknown>>; hasAny: boolean } {
  let enabled: Record<string, boolean> = {}
  let configs: Record<string, Record<string, unknown>> = {}
  if (typeof localStorage === 'undefined') {
    return { enabled, configs, hasAny: false }
  }
  try {
    const rawEnabled = localStorage.getItem(LEGACY_STORAGE_KEY_ENABLED)
    if (rawEnabled) {
      const parsed = JSON.parse(rawEnabled)
      if (parsed && typeof parsed === 'object') enabled = parsed
    }
  } catch {}
  try {
    const rawConfigs = localStorage.getItem(LEGACY_STORAGE_KEY_CONFIG)
    if (rawConfigs) {
      const parsed = JSON.parse(rawConfigs)
      if (parsed && typeof parsed === 'object') configs = parsed
    }
  } catch {}
  const hasAny = Object.keys(enabled).length > 0 || Object.keys(configs).length > 0
  return { enabled, configs, hasAny }
}

function clearLegacyLocalStorage() {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.removeItem(LEGACY_STORAGE_KEY_ENABLED)
    localStorage.removeItem(LEGACY_STORAGE_KEY_CONFIG)
  } catch {}
}

let writeQueue: Promise<void> = Promise.resolve()
let hydrateInFlight: Promise<void> | null = null

function applyResponse(data: PluginSettingsResponse): Pick<PluginSettingsState, 'enabledPlugins' | 'pluginConfigs'> {
  const configs = data?.configs && typeof data.configs === 'object' ? { ...data.configs } : {}
  syncAccentFromPluginConfigs(configs)
  return {
    enabledPlugins: data?.enabled && typeof data.enabled === 'object' ? { ...data.enabled } : {},
    pluginConfigs: configs,
  }
}

export const usePluginSettingsStore = create<PluginSettingsState>((set, get) => ({
  enabledPlugins: {},
  pluginConfigs: {},
  hydrated: false,
  loading: false,

  hydrate: async (force = false) => {
    if (!force && get().hydrated) return
    if (hydrateInFlight) return hydrateInFlight
    set({ loading: true })
    hydrateInFlight = (async () => {
      try {
        const legacy = readLegacyLocalStorage()
        let response: PluginSettingsResponse
        if (legacy.hasAny) {
          response = await api.updatePluginSettings({
            enabled: legacy.enabled,
            configs: legacy.configs,
            import_only: true,
          })
          clearLegacyLocalStorage()
        } else {
          response = await api.getPluginSettings()
        }
        set({ ...applyResponse(response), hydrated: true, loading: false })
      } catch {
        set({ loading: false })
      } finally {
        hydrateInFlight = null
      }
    })()
    return hydrateInFlight
  },

  togglePlugin: (pluginId: string, enabled?: boolean) => {
    const prevEnabled = get().enabledPlugins
    const nextVal = enabled !== undefined ? enabled : prevEnabled[pluginId] === false ? true : false
    const nextEnabled = { ...prevEnabled, [pluginId]: nextVal }
    set({ enabledPlugins: nextEnabled })
    writeQueue = writeQueue.then(async () => {
      try {
        const response = await api.updatePluginSettings({ enabled: { [pluginId]: nextVal } })
        set({ ...applyResponse(response), hydrated: true })
      } catch (error) {
        set({ enabledPlugins: prevEnabled })
        notifications.show({
          color: 'red',
          message: `保存插件开关失败：${error instanceof Error ? error.message : '请重试'}`,
        })
      }
    })
  },

  setPluginConfig: (pluginId: string, key: string, value: unknown) => {
    get().setPluginConfigPatch(pluginId, { [key]: value })
  },

  setPluginConfigPatch: (pluginId: string, patch: Record<string, unknown>) => {
    const prevConfigs = get().pluginConfigs
    const pluginConf = prevConfigs[pluginId] || {}
    const nextConfigs = { ...prevConfigs, [pluginId]: { ...pluginConf, ...patch } }
    set({ pluginConfigs: nextConfigs })
    writeQueue = writeQueue.then(async () => {
      try {
        const response = await api.updatePluginSettings({ configs: { [pluginId]: patch } })
        set({ ...applyResponse(response), hydrated: true })
      } catch (error) {
        set({ pluginConfigs: prevConfigs })
        notifications.show({
          color: 'red',
          message: `保存插件配置失败：${error instanceof Error ? error.message : '请重试'}`,
        })
      }
    })
  },

  isPluginEnabled: (pluginId: string) => {
    const val = get().enabledPlugins[pluginId]
    // 默认均为开启状态 (true)
    return val !== false
  },

  getPluginConfig: <T = unknown>(pluginId: string, key: string, fallback?: T): T => {
    const pluginConf = get().pluginConfigs[pluginId]
    if (pluginConf && pluginConf[key] !== undefined) {
      return pluginConf[key] as T
    }
    return fallback as T
  },

  reset: () => {
    hydrateInFlight = null
    writeQueue = Promise.resolve()
    set({ enabledPlugins: {}, pluginConfigs: {}, hydrated: false, loading: false })
  },
}))
