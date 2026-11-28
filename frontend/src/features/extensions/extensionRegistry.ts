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
  type MarkdownBlockRendererRegistration,
  type MessageDecoratorRegistration,
  type MessageInterceptorRegistration,
  type MessageInterceptorResult,
  type MessageSendContext,
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
  private messageInterceptors = new Map<string, MessageInterceptorRegistration>()
  private markdownBlockRenderers = new Map<string, MarkdownBlockRendererRegistration>()
  private messageDecorators = new Map<string, MessageDecoratorRegistration>()
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

  registerMessageInterceptor(spec: MessageInterceptorRegistration): Disposable {
    const key = `${spec.pluginId}:${spec.id}`
    this.messageInterceptors.set(key, spec)
    this.emit()
    return {
      dispose: () => {
        if (this.messageInterceptors.get(key) === spec) {
          this.messageInterceptors.delete(key)
          this.emit()
        }
      },
    }
  }

  unregisterMessageInterceptor(pluginId: string, id?: string): boolean {
    if (id !== undefined) {
      const removed = this.messageInterceptors.delete(`${pluginId}:${id}`)
      if (removed) this.emit()
      return removed
    }
    let removedAny = false
    for (const [key, item] of Array.from(this.messageInterceptors.entries())) {
      if (item.pluginId === pluginId) {
        this.messageInterceptors.delete(key)
        removedAny = true
      }
    }
    if (removedAny) this.emit()
    return removedAny
  }

  getActiveMessageInterceptors(options?: ExtensionQueryOptions): MessageInterceptorRegistration[] {
    const enabledMap = resolveEnabledMap(options)
    return Array.from(this.messageInterceptors.values())
      .filter((item) => options?.includeDisabled || isPluginActive(item.pluginId, enabledMap))
      .sort((a, b) => (a.order ?? 200) - (b.order ?? 200))
  }

  async runMessageInterceptors(
    initialCtx: MessageSendContext,
    options?: ExtensionQueryOptions & { timeoutMs?: number },
  ): Promise<{
    text: string
    metadata: Record<string, unknown>
    cancelled: boolean
    reason?: string
    cancelledBy?: string
  }> {
    const interceptors = this.getActiveMessageInterceptors(options)
    let text = initialCtx.text
    const metadata: Record<string, unknown> = { ...initialCtx.metadata }
    const timeoutMs = options?.timeoutMs ?? 3000

    for (const interceptor of interceptors) {
      let timer: ReturnType<typeof setTimeout> | undefined
      try {
        const result = await Promise.race<void | MessageInterceptorResult>([
          Promise.resolve(
            interceptor.beforeSend({
              session: initialCtx.session,
              text,
              attachments: initialCtx.attachments,
              metadata: { ...metadata },
            }),
          ),
          new Promise<void>((_, reject) => {
            timer = setTimeout(
              () => reject(new Error(`interceptor timeout (${timeoutMs}ms)`)),
              timeoutMs,
            )
          }),
        ])
        if (result && typeof result === 'object') {
          if (result.metadata && typeof result.metadata === 'object') {
            Object.assign(metadata, result.metadata)
          }
          if (result.cancel) {
            return {
              text,
              metadata,
              cancelled: true,
              reason: result.reason,
              cancelledBy: interceptor.pluginId,
            }
          }
          if (typeof result.text === 'string') {
            text = result.text
          }
        }
      } catch (err) {
        console.error(
          `[extensionRegistry] message interceptor "${interceptor.pluginId}:${interceptor.id}" failed:`,
          err,
        )
      } finally {
        if (timer !== undefined) clearTimeout(timer)
      }
    }

    return { text, metadata, cancelled: false }
  }

  registerMarkdownBlockRenderer(spec: MarkdownBlockRendererRegistration): Disposable {
    const key = `${spec.pluginId}:${spec.id}`
    const normalized: MarkdownBlockRendererRegistration = {
      ...spec,
      languages: Array.from(
        new Set(
          (spec.languages || [])
            .map((lang) => lang.trim().toLowerCase())
            .filter(Boolean),
        ),
      ),
    }
    this.markdownBlockRenderers.set(key, normalized)
    this.emit()
    return {
      dispose: () => {
        if (this.markdownBlockRenderers.get(key) === normalized) {
          this.markdownBlockRenderers.delete(key)
          this.emit()
        }
      },
    }
  }

  unregisterMarkdownBlockRenderer(pluginId: string, id?: string): boolean {
    if (id !== undefined) {
      const removed = this.markdownBlockRenderers.delete(`${pluginId}:${id}`)
      if (removed) this.emit()
      return removed
    }
    let removedAny = false
    for (const [key, item] of Array.from(this.markdownBlockRenderers.entries())) {
      if (item.pluginId === pluginId) {
        this.markdownBlockRenderers.delete(key)
        removedAny = true
      }
    }
    if (removedAny) this.emit()
    return removedAny
  }

  findMarkdownBlockRenderer(
    language: string,
    options?: ExtensionQueryOptions,
  ): MarkdownBlockRendererRegistration | null {
    const cleanLang = language.trim().toLowerCase()
    if (!cleanLang) return null
    const enabledMap = resolveEnabledMap(options)
    let best: MarkdownBlockRendererRegistration | null = null
    let bestPriority = -Infinity
    for (const item of this.markdownBlockRenderers.values()) {
      if (!options?.includeDisabled && !isPluginActive(item.pluginId, enabledMap)) {
        continue
      }
      if (!item.languages.includes(cleanLang)) continue
      const prio = item.priority ?? 100
      if (prio > bestPriority) {
        bestPriority = prio
        best = item
      }
    }
    return best
  }

  registerMessageDecorator(spec: MessageDecoratorRegistration): Disposable {
    const key = `${spec.pluginId}:${spec.id}`
    this.messageDecorators.set(key, spec)
    this.emit()
    return {
      dispose: () => {
        if (this.messageDecorators.get(key) === spec) {
          this.messageDecorators.delete(key)
          this.emit()
        }
      },
    }
  }

  unregisterMessageDecorator(pluginId: string, id?: string): boolean {
    if (id !== undefined) {
      const removed = this.messageDecorators.delete(`${pluginId}:${id}`)
      if (removed) this.emit()
      return removed
    }
    let removedAny = false
    for (const [key, item] of Array.from(this.messageDecorators.entries())) {
      if (item.pluginId === pluginId) {
        this.messageDecorators.delete(key)
        removedAny = true
      }
    }
    if (removedAny) this.emit()
    return removedAny
  }

  getActiveMessageDecorators(
    position: 'header' | 'footer',
    options?: ExtensionQueryOptions,
  ): MessageDecoratorRegistration[] {
    const enabledMap = resolveEnabledMap(options)
    return Array.from(this.messageDecorators.values())
      .filter(
        (item) =>
          item.position === position &&
          (options?.includeDisabled || isPluginActive(item.pluginId, enabledMap)),
      )
      .sort((a, b) => (a.order ?? 200) - (b.order ?? 200))
  }

  clear(): void {
    const hadAny =
      this.builtins.size > 0 ||
      this.pages.size > 0 ||
      this.commands.size > 0 ||
      this.messageInterceptors.size > 0 ||
      this.markdownBlockRenderers.size > 0 ||
      this.messageDecorators.size > 0 ||
      this.registeredViewerDisposables.size > 0
    this.builtins.clear()
    this.pages.clear()
    this.commands.clear()
    this.messageInterceptors.clear()
    this.markdownBlockRenderers.clear()
    this.messageDecorators.clear()
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
