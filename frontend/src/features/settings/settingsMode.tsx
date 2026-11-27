import { createContext, useContext } from 'react'

export type SettingsMode = 'basic' | 'advanced'

export const SETTINGS_MODE_STORAGE_KEY = 'astrorder:settings-mode'

export const ADVANCED_SETTINGS_TABS = new Set(['connections', 'skills', 'rtk', 'network'])

export function isAdvancedSettingsTab(tab: string): boolean {
  return ADVANCED_SETTINGS_TABS.has(tab)
}

export function readStoredSettingsMode(): SettingsMode {
  if (typeof localStorage === 'undefined') return 'basic'
  try {
    return localStorage.getItem(SETTINGS_MODE_STORAGE_KEY) === 'advanced' ? 'advanced' : 'basic'
  } catch {
    return 'basic'
  }
}

export function writeStoredSettingsMode(mode: SettingsMode) {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(SETTINGS_MODE_STORAGE_KEY, mode)
  } catch {}
}

export interface SettingsModeContextValue {
  mode: SettingsMode
  setMode: (next: SettingsMode) => void
  /** True when the global app profile is 'office', locking settings into simplified office mode. */
  officeLocked?: boolean
  /** False when a settings sub-page is rendered standalone outside SettingsWorkbench (e.g. unit tests). */
  managedByWorkbench: boolean
}

// Default to 'advanced' outside SettingsWorkbench so direct component renders expose all controls.
export const SettingsModeContext = createContext<SettingsModeContextValue>({
  mode: 'advanced',
  setMode: () => {},
  officeLocked: false,
  managedByWorkbench: false,
})

export function useSettingsMode() {
  return useContext(SettingsModeContext)
}
