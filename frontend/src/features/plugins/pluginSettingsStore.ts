import { create } from 'zustand'

const STORAGE_KEY_ENABLED = 'astrorder:plugins:enabled'
const STORAGE_KEY_CONFIG = 'astrorder:plugins:config'

export interface PluginSettingsState {
  // pluginId -> boolean (是否启用)
  enabledPlugins: Record<string, boolean>
  // pluginId -> { optionKey: value }
  pluginConfigs: Record<string, Record<string, unknown>>
  togglePlugin: (pluginId: string, enabled?: boolean) => void
  setPluginConfig: (pluginId: string, key: string, value: unknown) => void
  isPluginEnabled: (pluginId: string) => boolean
  getPluginConfig: <T = unknown>(pluginId: string, key: string, fallback?: T) => T
}

function loadInitialEnabled(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY_ENABLED)
    if (raw) return JSON.parse(raw)
  } catch {}
  return {}
}

function loadInitialConfigs(): Record<string, Record<string, unknown>> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY_CONFIG)
    if (raw) return JSON.parse(raw)
  } catch {}
  return {}
}

export const usePluginSettingsStore = create<PluginSettingsState>((set, get) => ({
  enabledPlugins: loadInitialEnabled(),
  pluginConfigs: loadInitialConfigs(),

  togglePlugin: (pluginId: string, enabled?: boolean) => {
    const current = get().enabledPlugins
    const nextVal = enabled !== undefined ? enabled : current[pluginId] === false ? true : false
    const next = { ...current, [pluginId]: nextVal }
    set({ enabledPlugins: next })
    try {
      localStorage.setItem(STORAGE_KEY_ENABLED, JSON.stringify(next))
    } catch {}
  },

  setPluginConfig: (pluginId: string, key: string, value: unknown) => {
    const current = get().pluginConfigs
    const pluginConf = current[pluginId] || {}
    const nextPluginConf = { ...pluginConf, [key]: value }
    const next = { ...current, [pluginId]: nextPluginConf }
    set({ pluginConfigs: next })
    try {
      localStorage.setItem(STORAGE_KEY_CONFIG, JSON.stringify(next))
    } catch {}
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
}))
