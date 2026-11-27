import { useSyncExternalStore } from 'react'
import type { PluginView } from '../../api/extensions'
import {
  APP_PROFILE_PLUGIN_KEY,
  APP_PROFILE_STORAGE_KEY,
  APP_PROFILE_SYNC_KEY,
  getAppProfile,
  type AppProfile,
} from '../office/appProfile'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { useSidecarStore } from '../sidecar/sidecarStore'
import {
  useExtensionStore,
  type EsmPluginRuntimeState,
} from './extensionStore'
import {
  createScopedPluginContextWithHandle,
  disposePluginSubscriptions,
  type AstrorderPluginContext,
  type AstrorderPluginModule,
} from './pluginSdk'

export type RawEsmPluginModule =
  | AstrorderPluginModule
  | {
      default?: AstrorderPluginModule
      activate?: AstrorderPluginModule['activate']
      deactivate?: AstrorderPluginModule['deactivate']
    }

export type EsmModuleImporter = (url: string) => Promise<RawEsmPluginModule>
export type EsmStyleLoader = (pluginId: string, href: string) => Promise<HTMLLinkElement>

export interface EsmPluginLoaderOptions {
  importer?: EsmModuleImporter
  styleLoader?: EsmStyleLoader
  /** Set true in tests that explicitly dispatch load/error events on <link> in jsdom. */
  awaitCssInJsdom?: boolean
  styleTimeoutMs?: number
  syncExtensionStore?: boolean
}

export interface EsmPluginLoadResult {
  ok: boolean
  pluginId: string
  digest?: string
  error?: string
}

interface LoadedEsmPluginEntry {
  pluginId: string
  digest: string
  module: AstrorderPluginModule
  ctx: AstrorderPluginContext
  registeredViewerIds: Set<string>
  registeredPageIds: Set<string>
  styleHrefs: string[]
}

const CSS_ATTR = 'data-astrorder-ext-css'
const DEFAULT_STYLE_TIMEOUT_MS = 10_000
const OFFICE_HIDDEN_PLUGIN_CATEGORIES = new Set<string>(['development', 'agent', 'runtime'])

const defaultModuleImporter: EsmModuleImporter = (url: string) =>
  import(/* @vite-ignore */ url) as Promise<RawEsmPluginModule>

function isJsdomEnvironment(): boolean {
  return typeof navigator !== 'undefined' && /jsdom/i.test(navigator.userAgent || '')
}

function validateRelativeAssetPath(rel: string): string {
  const trimmed = rel.trim()
  if (!trimmed) {
    throw new Error('插件静态资源路径不能为空')
  }
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(trimmed) || trimmed.startsWith('//') || trimmed.includes('\\')) {
    throw new Error(`非法的插件资源路径: ${rel}`)
  }
  const clean = trimmed.replace(/^\/+/, '')
  const segments = clean.split('/')
  if (segments.some((seg) => !seg || seg === '..' || seg === '.')) {
    throw new Error(`插件资源路径包含非法片段: ${rel}`)
  }
  return clean
}

export function buildEsmAssetUrl(
  plugin: Pick<PluginView, 'id' | 'esm_base_url' | 'content_digest'>,
  relativePath: string,
  cacheBustNonce?: number,
): string {
  const cleanPath = validateRelativeAssetPath(relativePath)
  const base = plugin.esm_base_url
    ? plugin.esm_base_url.replace(/\/+$/, '')
    : `/api/v1/ext/${encodeURIComponent(plugin.id)}/_esm`
  const params = new URLSearchParams()
  const digest = (plugin.content_digest || '').trim()
  if (digest) {
    params.set('v', digest)
  }
  if (cacheBustNonce !== undefined) {
    params.set('r', String(cacheBustNonce))
  }
  const qs = params.toString()
  return qs ? `${base}/${cleanPath}?${qs}` : `${base}/${cleanPath}`
}

export function isEsmPluginHiddenInOfficeMode(plugin: PluginView): boolean {
  if (plugin.source === 'external' && !plugin.signature_verified) {
    return true
  }
  const pages = plugin.contributes?.pages || []
  const viewers = plugin.contributes?.viewers || []
  const total = pages.length + viewers.length
  if (total > 0) {
    const allExplicitlyHidden =
      pages.every((p) => p.hide_in_office_mode === true) &&
      viewers.every((v) => v.hide_in_office_mode === true)
    if (allExplicitlyHidden) return true
    const anyExplicitlyAllowed =
      pages.some((p) => p.hide_in_office_mode === false) ||
      viewers.some((v) => v.hide_in_office_mode === false)
    if (anyExplicitlyAllowed) return false
  }
  return OFFICE_HIDDEN_PLUGIN_CATEGORIES.has(plugin.category)
}

export function shouldLoadEsmPlugin(
  plugin: PluginView,
  enabledOverrideMap?: Record<string, boolean>,
  appProfile?: AppProfile,
): boolean {
  if (plugin.frontend?.mode !== 'esm') return false
  if (!plugin.frontend.entry?.trim()) return false
  if (!plugin.enabled) return false
  if (
    plugin.source === 'builtin'
      ? plugin.permissions_granted === false
      : plugin.permissions_granted !== true
  ) {
    return false
  }
  if (plugin.pending_permissions && plugin.pending_permissions.length > 0) return false
  if (enabledOverrideMap && enabledOverrideMap[plugin.id] === false) return false
  const resolvedProfile = appProfile ?? getAppProfile()
  if (resolvedProfile === 'office' && isEsmPluginHiddenInOfficeMode(plugin)) {
    return false
  }
  return true
}

function resolvePluginModule(raw: RawEsmPluginModule | null | undefined): AstrorderPluginModule {
  if (!raw || typeof raw !== 'object') {
    throw new Error('ESM 插件未导出有效模块对象')
  }
  const defaultExport =
    'default' in raw && raw.default && typeof raw.default === 'object' ? raw.default : undefined
  const activate =
    typeof defaultExport?.activate === 'function'
      ? defaultExport.activate.bind(defaultExport)
      : typeof raw.activate === 'function'
        ? raw.activate.bind(raw)
        : null
  if (!activate) {
    throw new Error('ESM 插件缺少 activate(ctx) 生命周期入口')
  }
  const deactivate =
    typeof defaultExport?.deactivate === 'function'
      ? defaultExport.deactivate.bind(defaultExport)
      : typeof raw.deactivate === 'function'
        ? raw.deactivate.bind(raw)
        : undefined
  return { activate, deactivate }
}

function removePluginStylesheets(pluginId: string): void {
  if (typeof document === 'undefined') return
  const links = document.querySelectorAll<HTMLLinkElement>(
    `link[${CSS_ATTR}="${CSS.escape ? CSS.escape(pluginId) : pluginId.replace(/"/g, '\\"')}"]`,
  )
  links.forEach((link) => link.remove())
}

function createDefaultStyleLoader(
  awaitCssInJsdom: boolean,
  timeoutMs: number,
): EsmStyleLoader {
  return (pluginId: string, href: string) =>
    new Promise<HTMLLinkElement>((resolve, reject) => {
      if (typeof document === 'undefined') {
        reject(new Error('当前环境缺少 document，无法注入插件样式'))
        return
      }
      const link = document.createElement('link')
      link.rel = 'stylesheet'
      link.setAttribute(CSS_ATTR, pluginId)
      link.href = href

      if (isJsdomEnvironment() && !awaitCssInJsdom) {
        document.head.appendChild(link)
        queueMicrotask(() => resolve(link))
        return
      }

      let settled = false
      let timer: ReturnType<typeof setTimeout> | undefined
      const cleanup = () => {
        if (timer !== undefined) {
          clearTimeout(timer)
          timer = undefined
        }
        link.removeEventListener('load', onLoad)
        link.removeEventListener('error', onError)
      }
      const onLoad = () => {
        if (settled) return
        settled = true
        cleanup()
        resolve(link)
      }
      const onError = () => {
        if (settled) return
        settled = true
        cleanup()
        link.remove()
        reject(new Error(`加载插件样式表失败: ${href}`))
      }
      link.addEventListener('load', onLoad)
      link.addEventListener('error', onError)
      timer = setTimeout(() => {
        if (settled) return
        settled = true
        cleanup()
        // Resolve after timeout so a slow optional stylesheet never deadlocks the host,
        // while keeping the link attached if it finishes shortly after.
        resolve(link)
      }, timeoutMs)

      document.head.appendChild(link)
    })
}

function cleanupUnloadedPluginUI(pluginId: string, viewerIds: Set<string>): void {
  const sidecar = useSidecarStore.getState()
  const ownedViewerIds = new Set<string>([pluginId, ...viewerIds])
  const tabsToClose = sidecar.tabs.filter((tab) => {
    if (tab.viewerId && ownedViewerIds.has(tab.viewerId)) return true
    if (tab.id.startsWith(`ext:${pluginId}:`)) return true
    const metaPluginId =
      tab.artifact?.metadata && typeof tab.artifact.metadata === 'object'
        ? (tab.artifact.metadata as Record<string, unknown>).pluginId
        : undefined
    return metaPluginId === pluginId
  })
  for (const tab of tabsToClose) {
    sidecar.closeTab(tab.id)
  }

  if (typeof window !== 'undefined') {
    const encodedId = encodeURIComponent(pluginId)
    const path = window.location.pathname
    if (path.startsWith(`/ext-page/${encodedId}/`) || path === `/ext-page/${encodedId}`) {
      window.dispatchEvent(new CustomEvent('astrorder:navigate', { detail: { to: '/chat' } }))
    } else if (
      path.startsWith(`/mobile/ext-page/${encodedId}/`) ||
      path === `/mobile/ext-page/${encodedId}`
    ) {
      window.dispatchEvent(new CustomEvent('astrorder:navigate', { detail: { to: '/mobile/chat' } }))
    }
  }
}

export class EsmPluginLoader {
  private importer: EsmModuleImporter
  private styleLoader: EsmStyleLoader
  private syncExtensionStore: boolean
  private loaded = new Map<string, LoadedEsmPluginEntry>()
  private runtimeStates = new Map<string, EsmPluginRuntimeState>()
  private desiredPlugins = new Map<string, PluginView | null>()
  private failedDigestByPlugin = new Map<string, string>()
  private queues = new Map<string, Promise<void>>()
  private listeners = new Set<() => void>()
  private snapshot: Record<string, EsmPluginRuntimeState> = {}
  private reloadNonce = 0
  private stopSub: (() => void) | null = null

  constructor(options: EsmPluginLoaderOptions = {}) {
    this.importer = options.importer ?? defaultModuleImporter
    this.styleLoader =
      options.styleLoader ??
      createDefaultStyleLoader(
        Boolean(options.awaitCssInJsdom),
        options.styleTimeoutMs ?? DEFAULT_STYLE_TIMEOUT_MS,
      )
    this.syncExtensionStore = options.syncExtensionStore ?? true
  }

  private emitState(pluginId: string, next: EsmPluginRuntimeState | null): void {
    if (next === null) {
      this.runtimeStates.delete(pluginId)
    } else {
      this.runtimeStates.set(pluginId, next)
    }
    const nextSnapshot: Record<string, EsmPluginRuntimeState> = {}
    for (const [id, st] of this.runtimeStates.entries()) {
      nextSnapshot[id] = st
    }
    this.snapshot = nextSnapshot
    if (this.syncExtensionStore) {
      useExtensionStore.getState().setEsmRuntimeState(pluginId, next)
    }
    for (const listener of this.listeners) {
      listener()
    }
  }

  private enqueue<T>(pluginId: string, task: () => Promise<T>): Promise<T> {
    const prev = this.queues.get(pluginId) ?? Promise.resolve()
    const run = prev.catch(() => {}).then(task)
    const tail = run.then(
      () => undefined,
      () => undefined,
    )
    this.queues.set(pluginId, tail)
    void tail.finally(() => {
      if (this.queues.get(pluginId) === tail) {
        this.queues.delete(pluginId)
      }
    })
    return run
  }

  getState(pluginId: string): EsmPluginRuntimeState | undefined {
    return this.runtimeStates.get(pluginId)
  }

  getAllStates(): Record<string, EsmPluginRuntimeState> {
    return this.snapshot
  }

  isLoaded(pluginId: string): boolean {
    return this.loaded.has(pluginId)
  }

  getLoadedDigest(pluginId: string): string | undefined {
    return this.loaded.get(pluginId)?.digest
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  private async unloadInternal(pluginId: string, preserveState = false): Promise<void> {
    const existing = this.loaded.get(pluginId)
    if (!existing) {
      removePluginStylesheets(pluginId)
      if (!preserveState) {
        this.failedDigestByPlugin.delete(pluginId)
        this.emitState(pluginId, null)
      }
      return
    }

    this.loaded.delete(pluginId)
    try {
      await existing.module.deactivate?.()
    } catch (err) {
      console.error(`[esmPluginLoader] deactivate() failed for "${pluginId}":`, err)
    } finally {
      disposePluginSubscriptions(existing.ctx.subscriptions)
      removePluginStylesheets(pluginId)
      if (!preserveState) {
        cleanupUnloadedPluginUI(pluginId, existing.registeredViewerIds)
        this.failedDigestByPlugin.delete(pluginId)
        this.emitState(pluginId, null)
      }
    }
  }

  private async loadInternal(
    plugin: PluginView,
    options?: {
      forceCacheBust?: boolean
      checkDesiredAfterAsync?: boolean
      appProfile?: AppProfile
    },
  ): Promise<EsmPluginLoadResult> {
    const pluginId = plugin.id
    const digest = (plugin.content_digest || '').trim()
    const entry = plugin.frontend?.entry?.trim()
    if (!entry) {
      const error = 'ESM 插件未声明有效的 frontend.entry 入口文件'
      this.failedDigestByPlugin.set(pluginId, digest)
      this.emitState(pluginId, { status: 'error', digest, error, updatedAt: Date.now() })
      return { ok: false, pluginId, digest, error }
    }

    // Remove any stale stylesheet from a previous attempt before loading.
    removePluginStylesheets(pluginId)
    this.emitState(pluginId, { status: 'loading', digest, error: null, updatedAt: Date.now() })

    const nonce = options?.forceCacheBust ? ++this.reloadNonce : undefined
    const handle = createScopedPluginContextWithHandle(plugin)
    const styleHrefs: string[] = []

    const isSupersededByDisable = (): boolean => {
      if (!options?.checkDesiredAfterAsync) return false
      if (!this.desiredPlugins.has(pluginId)) return false
      const latest = this.desiredPlugins.get(pluginId)
      if (!latest) return true
      const enabledMap = usePluginSettingsStore.getState().enabledPlugins
      return !shouldLoadEsmPlugin(latest, enabledMap, options?.appProfile)
    }

    try {
      const styles = Array.isArray(plugin.frontend?.styles) ? plugin.frontend.styles : []
      for (const stylePath of styles) {
        if (!stylePath?.trim()) continue
        const href = buildEsmAssetUrl(plugin, stylePath, nonce)
        styleHrefs.push(href)
        await this.styleLoader(pluginId, href)
        if (isSupersededByDisable()) {
          removePluginStylesheets(pluginId)
          this.emitState(pluginId, null)
          return { ok: true, pluginId, digest }
        }
      }

      const entryUrl = buildEsmAssetUrl(plugin, entry, nonce)
      const rawModule = await this.importer(entryUrl)
      if (isSupersededByDisable()) {
        removePluginStylesheets(pluginId)
        this.emitState(pluginId, null)
        return { ok: true, pluginId, digest }
      }

      const resolved = resolvePluginModule(rawModule)
      await resolved.activate(handle.ctx)

      if (isSupersededByDisable()) {
        try {
          await resolved.deactivate?.()
        } catch (err) {
          console.error(`[esmPluginLoader] deactivate() during abort failed for "${pluginId}":`, err)
        } finally {
          disposePluginSubscriptions(handle.ctx.subscriptions)
          removePluginStylesheets(pluginId)
          cleanupUnloadedPluginUI(pluginId, handle.registeredViewerIds)
          this.emitState(pluginId, null)
        }
        return { ok: true, pluginId, digest }
      }

      this.loaded.set(pluginId, {
        pluginId,
        digest,
        module: resolved,
        ctx: handle.ctx,
        registeredViewerIds: handle.registeredViewerIds,
        registeredPageIds: handle.registeredPageIds,
        styleHrefs,
      })
      this.failedDigestByPlugin.delete(pluginId)
      this.emitState(pluginId, {
        status: 'loaded',
        digest,
        error: null,
        updatedAt: Date.now(),
      })
      return { ok: true, pluginId, digest }
    } catch (err) {
      disposePluginSubscriptions(handle.ctx.subscriptions)
      removePluginStylesheets(pluginId)
      const message = err instanceof Error ? err.message : String(err || '加载 ESM 插件失败')
      this.failedDigestByPlugin.set(pluginId, digest)
      this.emitState(pluginId, {
        status: 'error',
        digest,
        error: message,
        updatedAt: Date.now(),
      })
      return { ok: false, pluginId, digest, error: message }
    }
  }

  loadPlugin(plugin: PluginView): Promise<EsmPluginLoadResult> {
    this.desiredPlugins.set(plugin.id, plugin)
    return this.enqueue(plugin.id, async () => {
      const enabledMap = usePluginSettingsStore.getState().enabledPlugins
      if (!shouldLoadEsmPlugin(plugin, enabledMap)) {
        await this.unloadInternal(plugin.id)
        const error =
          getAppProfile() === 'office' && isEsmPluginHiddenInOfficeMode(plugin)
            ? '当前处于办公模式，该开发专属插件已跳过加载'
            : '插件未启用或未获批权限'
        return { ok: false, pluginId: plugin.id, error }
      }
      const existing = this.loaded.get(plugin.id)
      const nextDigest = (plugin.content_digest || '').trim()
      if (existing && existing.digest === nextDigest) {
        return { ok: true, pluginId: plugin.id, digest: existing.digest }
      }
      if (existing) {
        await this.unloadInternal(plugin.id, true)
      }
      return this.loadInternal(plugin)
    })
  }

  unloadPlugin(pluginId: string): Promise<void> {
    this.desiredPlugins.set(pluginId, null)
    return this.enqueue(pluginId, () => this.unloadInternal(pluginId))
  }

  reloadPlugin(plugin: PluginView): Promise<EsmPluginLoadResult> {
    this.desiredPlugins.set(plugin.id, plugin)
    return this.enqueue(plugin.id, async () => {
      const enabledMap = usePluginSettingsStore.getState().enabledPlugins
      if (!shouldLoadEsmPlugin(plugin, enabledMap)) {
        await this.unloadInternal(plugin.id)
        const error =
          getAppProfile() === 'office' && isEsmPluginHiddenInOfficeMode(plugin)
            ? '当前处于办公模式，该开发专属插件已跳过加载'
            : '插件未启用或未获批权限'
        return { ok: false, pluginId: plugin.id, error }
      }
      this.failedDigestByPlugin.delete(plugin.id)
      if (this.loaded.has(plugin.id)) {
        await this.unloadInternal(plugin.id, true)
      }
      return this.loadInternal(plugin, { forceCacheBust: true })
    })
  }

  async syncLoadedPlugins(
    items: PluginView[],
    enabledOverrideMap?: Record<string, boolean>,
    appProfile?: AppProfile,
  ): Promise<void> {
    const esmItemsById = new Map<string, PluginView>()
    for (const item of items) {
      if (item.frontend?.mode === 'esm') {
        esmItemsById.set(item.id, item)
      }
    }

    const trackedIds = new Set<string>([
      ...this.loaded.keys(),
      ...this.runtimeStates.keys(),
      ...esmItemsById.keys(),
    ])

    for (const id of trackedIds) {
      this.desiredPlugins.set(id, esmItemsById.get(id) ?? null)
    }

    const tasks: Array<Promise<unknown>> = []
    for (const id of trackedIds) {
      tasks.push(
        this.enqueue(id, async () => {
          const latest = this.desiredPlugins.get(id) ?? null
          const currentEnabledMap =
            enabledOverrideMap ?? usePluginSettingsStore.getState().enabledPlugins
          const wantLoaded =
            latest !== null && shouldLoadEsmPlugin(latest, currentEnabledMap, appProfile)
          const existing = this.loaded.get(id)

          if (!wantLoaded) {
            if (existing || this.runtimeStates.has(id)) {
              await this.unloadInternal(id)
            }
            return
          }

          const nextDigest = (latest.content_digest || '').trim()

          // Already loaded at the exact same digest: no-op.
          if (existing && existing.digest === nextDigest) {
            return
          }

          // Previously failed at this exact same digest: do not infinite-retry until digest changes or explicit reload.
          if (
            !existing &&
            this.failedDigestByPlugin.has(id) &&
            this.failedDigestByPlugin.get(id) === nextDigest
          ) {
            return
          }

          // Upgrade hot-swap: unload old digest before loading new digest.
          if (existing) {
            await this.unloadInternal(id, true)
          }

          await this.loadInternal(latest, { checkDesiredAfterAsync: true, appProfile })
        }),
      )
    }

    await Promise.all(tasks)
  }

  start(): () => void {
    if (this.stopSub) return this.stopSub

    const unsubExt = useExtensionStore.subscribe((state, prevState) => {
      if (state.items !== prevState.items) {
        void this.syncLoadedPlugins(state.items)
      }
    })

    const unsubSettings = usePluginSettingsStore.subscribe((state, prevState) => {
      const enabledChanged = state.enabledPlugins !== prevState.enabledPlugins
      const profileChanged =
        state.pluginConfigs[APP_PROFILE_PLUGIN_KEY] !==
        prevState.pluginConfigs[APP_PROFILE_PLUGIN_KEY]
      if (enabledChanged || profileChanged) {
        void this.syncLoadedPlugins(useExtensionStore.getState().items, state.enabledPlugins)
      }
    })

    const onStorage = (event: StorageEvent) => {
      if (event.key === APP_PROFILE_STORAGE_KEY || event.key === APP_PROFILE_SYNC_KEY) {
        void this.syncLoadedPlugins(useExtensionStore.getState().items)
      }
    }
    if (typeof window !== 'undefined') {
      window.addEventListener('storage', onStorage)
    }

    // Sync once immediately with any already-hydrated extensions.
    const currentItems = useExtensionStore.getState().items
    if (currentItems.length > 0) {
      void this.syncLoadedPlugins(currentItems)
    }

    this.stopSub = () => {
      unsubExt()
      unsubSettings()
      if (typeof window !== 'undefined') {
        window.removeEventListener('storage', onStorage)
      }
      this.stopSub = null
    }
    return this.stopSub
  }

  stop(): void {
    this.stopSub?.()
  }

  async reset(): Promise<void> {
    this.stop()
    const ids = Array.from(new Set([...this.loaded.keys(), ...this.runtimeStates.keys()]))
    await Promise.all(ids.map((id) => this.unloadPlugin(id)))
    this.desiredPlugins.clear()
    this.failedDigestByPlugin.clear()
    this.queues.clear()
    this.runtimeStates.clear()
    this.snapshot = {}
    this.reloadNonce = 0
  }
}

export function createEsmPluginLoader(options?: EsmPluginLoaderOptions): EsmPluginLoader {
  return new EsmPluginLoader(options)
}

export const esmPluginLoader = new EsmPluginLoader()

export function startEsmPluginLoader(): () => void {
  return esmPluginLoader.start()
}

export function useEsmPluginStates(): Record<string, EsmPluginRuntimeState> {
  return useSyncExternalStore(
    (onStoreChange) => esmPluginLoader.subscribe(onStoreChange),
    () => esmPluginLoader.getAllStates(),
    () => esmPluginLoader.getAllStates(),
  )
}

export function useEsmPluginState(pluginId: string): EsmPluginRuntimeState | undefined {
  const states = useEsmPluginStates()
  return states[pluginId]
}
