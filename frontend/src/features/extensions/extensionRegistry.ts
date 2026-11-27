import { useSyncExternalStore } from 'react'
import type { PluginView } from '../../api/extensions'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { artifactViewerRegistry } from '../sidecar/registry'
import type { ArtifactViewer } from '../sidecar/types'
import {
  extensionPageKey,
  type BuiltInExtensionRegistration,
  type BuiltInPanelRegistration,
  type Disposable,
  type ExtensionCommandRegistration,
  type ExtensionPageRegistration,
  type ExtensionQueryOptions,
} from './types'

function resolveOfficeFilter(options?: ExtensionQueryOptions): boolean {
  if (typeof options?.isOfficeMode === 'boolean') return options.isOfficeMode
  return options?.profile === 'office'
}

function resolveEnabledMap(options?: ExtensionQueryOptions): Record<string, boolean> {
  if (options?.enabledPlugins) return options.enabledPlugins
  return usePluginSettingsStore.getState().enabledPlugins
}

function isPluginActive(pluginId: string, enabledMap: Record<string, boolean>): boolean {
  return enabledMap[pluginId] !== false
}

class ExtensionRegistry {
  private builtins = new Map<string, BuiltInExtensionRegistration>()
  private pages = new Map<string, ExtensionPageRegistration>()
  private commands = new Map<string, ExtensionCommandRegistration>()
  private registeredViewerDisposables = new Map<string, Disposable>()
  private listeners = new Set<() => void>()
  private version = 0

  private emit() {
    this.version += 1
    for (const listener of this.listeners) {
      listener()
    }
  }

  registerBuiltin(reg: BuiltInExtensionRegistration): Disposable {
    this.builtins.set(reg.pluginId, reg)
    this.emit()
    return {
      dispose: () => {
        if (this.builtins.get(reg.pluginId) === reg) {
          this.builtins.delete(reg.pluginId)
          this.emit()
        }
      },
    }
  }

  unregisterBuiltin(pluginId: string): void {
    if (this.builtins.delete(pluginId)) {
      this.emit()
    }
  }

  registerPage(spec: ExtensionPageRegistration): Disposable {
    const key = extensionPageKey(spec.pluginId, spec.id)
    this.pages.set(key, spec)
    this.emit()
    return {
      dispose: () => {
        if (this.pages.get(key) === spec) {
          this.pages.delete(key)
          this.emit()
        }
      },
    }
  }

  unregisterPage(pluginId: string, pageId?: string): boolean {
    if (pageId !== undefined) {
      const removed = this.pages.delete(extensionPageKey(pluginId, pageId))
      if (removed) this.emit()
      return removed
    }
    let removedAny = false
    for (const [key, page] of Array.from(this.pages.entries())) {
      if (page.pluginId === pluginId) {
        this.pages.delete(key)
        removedAny = true
      }
    }
    if (removedAny) this.emit()
    return removedAny
  }

  getPageById(pluginId: string, pageId: string): ExtensionPageRegistration | undefined {
    return this.pages.get(extensionPageKey(pluginId, pageId))
  }

  getAllPages(): ExtensionPageRegistration[] {
    return Array.from(this.pages.values()).sort((a, b) => (a.order ?? 200) - (b.order ?? 200))
  }

  getActivePages(options?: ExtensionQueryOptions): ExtensionPageRegistration[] {
    const isOffice = resolveOfficeFilter(options)
    const enabledMap = resolveEnabledMap(options)
    return this.getAllPages().filter((page) => {
      if (!options?.includeDisabled && !isPluginActive(page.pluginId, enabledMap)) {
        return false
      }
      if (isOffice && page.hideInOfficeMode) {
        return false
      }
      return true
    })
  }

  registerCommand(spec: ExtensionCommandRegistration): Disposable {
    const key = `${spec.pluginId}:${spec.id}`
    this.commands.set(key, spec)
    this.emit()
    return {
      dispose: () => {
        if (this.commands.get(key) === spec) {
          this.commands.delete(key)
          this.emit()
        }
      },
    }
  }

  unregisterCommand(pluginId: string, commandId?: string): boolean {
    if (commandId !== undefined) {
      const removed = this.commands.delete(`${pluginId}:${commandId}`)
      if (removed) this.emit()
      return removed
    }
    let removedAny = false
    for (const [key, command] of Array.from(this.commands.entries())) {
      if (command.pluginId === pluginId) {
        this.commands.delete(key)
        removedAny = true
      }
    }
    if (removedAny) this.emit()
    return removedAny
  }

  getAllCommands(): ExtensionCommandRegistration[] {
    return Array.from(this.commands.values()).sort((a, b) => (a.order ?? 200) - (b.order ?? 200))
  }

  getActiveCommands(options?: ExtensionQueryOptions): ExtensionCommandRegistration[] {
    const isOffice = resolveOfficeFilter(options)
    const enabledMap = resolveEnabledMap(options)
    return this.getAllCommands().filter((command) => {
      if (!options?.includeDisabled && !isPluginActive(command.pluginId, enabledMap)) {
        return false
      }
      if (isOffice && command.hideInOfficeMode) {
        return false
      }
      return true
    })
  }

  registerViewer(viewer: ArtifactViewer): Disposable {
    const disposable = artifactViewerRegistry.register(viewer)
    const wrapped: Disposable = {
      dispose: () => {
        if (this.registeredViewerDisposables.get(viewer.id) === wrapped) {
          this.registeredViewerDisposables.delete(viewer.id)
        }
        disposable.dispose()
        this.emit()
      },
    }
    this.registeredViewerDisposables.set(viewer.id, wrapped)
    this.emit()
    return wrapped
  }

  unregisterViewer(id: string): boolean {
    this.registeredViewerDisposables.delete(id)
    const removed = artifactViewerRegistry.unregister(id)
    if (removed) {
      this.emit()
    }
    return removed
  }

  clear(): void {
    const hadAny =
      this.builtins.size > 0 ||
      this.pages.size > 0 ||
      this.commands.size > 0 ||
      this.registeredViewerDisposables.size > 0
    this.builtins.clear()
    this.pages.clear()
    this.commands.clear()
    for (const id of Array.from(this.registeredViewerDisposables.keys())) {
      artifactViewerRegistry.unregister(id)
    }
    this.registeredViewerDisposables.clear()
    if (hadAny) {
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
