import { useSyncExternalStore } from 'react'
import type { PluginView } from '../../api/extensions'
import type { BuiltInExtensionRegistration, BuiltInPanelRegistration } from './types'

class ExtensionRegistry {
  private builtins = new Map<string, BuiltInExtensionRegistration>()
  private listeners = new Set<() => void>()
  private version = 0

  private emit() {
    this.version += 1
    for (const listener of this.listeners) {
      listener()
    }
  }

  registerBuiltin(reg: BuiltInExtensionRegistration): void {
    this.builtins.set(reg.pluginId, reg)
    this.emit()
  }

  unregisterBuiltin(pluginId: string): void {
    if (this.builtins.delete(pluginId)) {
      this.emit()
    }
  }

  clear(): void {
    if (this.builtins.size > 0) {
      this.builtins.clear()
      this.emit()
    }
  }

  getBuiltin(pluginId: string): BuiltInExtensionRegistration | undefined {
    return this.builtins.get(pluginId)
  }

  getAllBuiltins(): BuiltInExtensionRegistration[] {
    return Array.from(this.builtins.values())
  }

  findBuiltinPanel(
    pluginId?: string | null,
    panelId?: string | null,
    mediaType?: string | null,
  ): { pluginId: string; panelId: string; panel: BuiltInPanelRegistration } | null {
    if (pluginId) {
      const reg = this.builtins.get(pluginId)
      if (reg?.panels) {
        if (panelId && reg.panels[panelId]) {
          return { pluginId, panelId, panel: reg.panels[panelId] }
        }
        if (mediaType) {
          for (const [id, panel] of Object.entries(reg.panels)) {
            if (panel.mediaType === mediaType) {
              return { pluginId, panelId: id, panel }
            }
          }
        }
      }
    }
    if (mediaType) {
      for (const reg of this.builtins.values()) {
        if (!reg.panels) continue
        for (const [id, panel] of Object.entries(reg.panels)) {
          if (panel.mediaType === mediaType) {
            return { pluginId: reg.pluginId, panelId: id, panel }
          }
        }
      }
    }
    return null
  }

  hasSettingsDrawer(pluginId: string, manifest?: PluginView | null): boolean {
    if (this.builtins.get(pluginId)?.settingsComponent) {
      return true
    }
    const mode = manifest?.settings?.mode
    return mode === 'schema' || mode === 'custom_ui'
  }

  getSnapshotVersion(): number {
    return this.version
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }
}

export const extensionRegistry = new ExtensionRegistry()

export function useExtensionRegistryVersion(): number {
  return useSyncExternalStore(
    (onStoreChange) => extensionRegistry.subscribe(onStoreChange),
    () => extensionRegistry.getSnapshotVersion(),
    () => extensionRegistry.getSnapshotVersion(),
  )
}
