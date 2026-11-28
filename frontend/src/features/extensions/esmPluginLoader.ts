import { useSyncExternalStore } from 'react'
import { extensionsApi, type PluginView } from '../../api/extensions'
import {
  APP_PROFILE_PLUGIN_KEY,
  APP_PROFILE_STORAGE_KEY,
  APP_PROFILE_SYNC_KEY,
  getAppProfile,
  type AppProfile,
} from '../office/appProfile'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { artifactViewerRegistry } from '../sidecar/registry'
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
const PRELOAD_ATTR = 'data-astrorder-ext-preload'
export const AUTH_READY_EVENT = 'astrorder:auth-ready'
const DEFAULT_STYLE_TIMEOUT_MS = 10_000
const OFFICE_HIDDEN_PLUGIN_CATEGORIES = new Set<string>(['development', 'agent', 'runtime'])
const SHARED_SDK_PRELOAD_PATHS = [
  '/api/v1/ext/_sdk/react.js',
  '/api/v1/ext/_sdk/jsx-runtime.js',
  '/api/v1/ext/_sdk/mantine-core.js',
  '/api/v1/ext/_sdk/sdk.js',
  '/api/v1/ext/_sdk/workspace-bridge.js',
  '/api/v1/ext/_sdk/react-router-dom.js',
] as const

function isNonRetryablePluginLoadError(err: unknown): boolean {
  if (err instanceof SyntaxError) return true
  const msg = err instanceof Error ? err.message : String(err || '')
  return /does not provide an export named|syntaxerror|插件未声明有效的 frontend\.entry/i.test(msg)
}

let sharedSdkPreloaded = false

function preloadSharedSdkModules(): void {
  if (sharedSdkPreloaded || typeof document === 'undefined' || isJsdomEnvironment()) return
  sharedSdkPreloaded = true
  for (const href of SHARED_SDK_PRELOAD_PATHS) {
    if (document.querySelector(`link[rel="modulepreload"][href="${href}"]`)) continue
    const link = document.createElement('link')
    link.rel = 'modulepreload'
    link.href = href
    document.head.appendChild(link)
  }
}

function preloadPluginEntryModule(pluginId: string, entryUrl: string): void {
  if (typeof document === 'undefined' || isJsdomEnvironment()) return
  removePluginPreloads(pluginId)
  const link = document.createElement('link')
  link.rel = 'modulepreload'
  link.setAttribute(PRELOAD_ATTR, pluginId)
  link.href = entryUrl
  document.head.appendChild(link)
}

function removePluginPreloads(pluginId: string): void {
  if (typeof document === 'undefined') return
  const escaped = CSS.escape ? CSS.escape(pluginId) : pluginId.replace(/"/g, '\\"')
  document.querySelectorAll<HTMLLinkElement>(`link[${PRELOAD_ATTR}="${escaped}"]`).forEach((el) => el.remove())
}

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
  private nonRetryableFailedPlugins = new Set<string>()
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
    const prev = this.runtimeStates.get(pluginId)
    if (next === null) {
      if (!prev) return
      this.runtimeStates.delete(pluginId)
    } else {
      if (
        prev &&
        prev.status === next.status &&
        prev.digest === next.digest &&
        prev.error === next.error
      ) {
        return
      }
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
        this.nonRetryableFailedPlugins.delete(pluginId)
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
        this.nonRetryableFailedPlugins.delete(pluginId)
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
      this.nonRetryableFailedPlugins.add(pluginId)
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
      const entryUrl = buildEsmAssetUrl(plugin, entry, nonce)
      // Preload shared SDK modules and the plugin entry in parallel while waiting for
      // stylesheets so CSS -> entry.js -> _sdk/*.js collapses into a single round-trip
      // without executing entry JS before stylesheets finish loading.
      preloadSharedSdkModules()
      preloadPluginEntryModule(pluginId, entryUrl)

      const styles = Array.isArray(plugin.frontend?.styles) ? plugin.frontend.styles : []
      for (const stylePath of styles) {
        if (!stylePath?.trim()) continue
        const href = buildEsmAssetUrl(plugin, stylePath, nonce)
        styleHrefs.push(href)
        await this.styleLoader(pluginId, href)
        if (isSupersededByDisable()) {
          removePluginStylesheets(pluginId)
          removePluginPreloads(pluginId)
          this.emitState(pluginId, null)
          return { ok: true, pluginId, digest }
        }
      }

      const rawModule = await this.importer(entryUrl)
      removePluginPreloads(pluginId)
      if (isSupersededByDisable()) {
        removePluginStylesheets(pluginId)
        this.emitState(pluginId, null)
        return { ok: true, pluginId, digest }
      }

      // Sync persisted extension KV / schema defaults into pluginSettingsStore BEFORE activate()
      // so ctx.api.getConfig() and usePluginConfig() observe the latest values immediately.
      if (plugin.settings?.mode === 'schema' || plugin.settings?.mode === 'custom_ui') {
        const defaults: Record<string, unknown> = {}
        for (const field of plugin.settings.schema || []) {
          if (field.type !== 'secret' && field.default_value !== undefined) {
            defaults[field.key] = field.default_value
          }
        }
        try {
          const cfgRes = await extensionsApi.getExtensionConfig(pluginId)
          const merged = { ...defaults, ...(cfgRes?.values || {}) }
          if (Object.keys(merged).length > 0) {
            usePluginSettingsStore.getState().syncExtensionConfigLocal(pluginId, merged, false)
          }
        } catch {
          if (Object.keys(defaults).length > 0) {
            usePluginSettingsStore.getState().syncExtensionConfigLocal(pluginId, defaults, false)
          }
        }
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
      this.nonRetryableFailedPlugins.delete(pluginId)
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
      removePluginPreloads(pluginId)
      const message = err instanceof Error ? err.message : String(err || '加载 ESM 插件失败')
      this.failedDigestByPlugin.set(pluginId, digest)
      if (isNonRetryablePluginLoadError(err)) {
        this.nonRetryableFailedPlugins.add(pluginId)
      } else {
        this.nonRetryableFailedPlugins.delete(pluginId)
      }
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
      this.nonRetryableFailedPlugins.delete(plugin.id)
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
    artifactViewerRegistry.syncDeclarativeIframeViewers(items)
    const allItemsById = new Map<string, PluginView>()
    const esmItemsById = new Map<string, PluginView>()
    for (const item of items) {
      allItemsById.set(item.id, item)
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

    const currentEnabledMap =
      enabledOverrideMap ?? usePluginSettingsStore.getState().enabledPlugins

    const toUnload: string[] = []
    const wantLoadMap = new Map<string, PluginView>()

    for (const id of trackedIds) {
      const latest = this.desiredPlugins.get(id) ?? null
      const wantLoaded =
        latest !== null && shouldLoadEsmPlugin(latest, currentEnabledMap, appProfile)
      if (!wantLoaded) {
        toUnload.push(id)
      } else {
        wantLoadMap.set(id, latest)
      }
    }

    // 1. Unload any plugins that are no longer desired.
    if (toUnload.length > 0) {
      await Promise.all(
        toUnload.map((id) =>
          this.enqueue(id, async () => {
            const latest = this.desiredPlugins.get(id) ?? null
            const liveEnabledMap =
              enabledOverrideMap ?? usePluginSettingsStore.getState().enabledPlugins
            const stillWanted =
              latest !== null && shouldLoadEsmPlugin(latest, liveEnabledMap, appProfile)
            if (!stillWanted && (this.loaded.has(id) || this.runtimeStates.has(id))) {
              await this.unloadInternal(id)
            }
          }),
        ),
      )
    }

    if (wantLoadMap.size === 0) return

    // 2. Validate depends_on & partition into topological load waves (Kahn's algorithm).
    const isDepAvailable = (depId: string): boolean => {
      if (wantLoadMap.has(depId)) return true
      const item = allItemsById.get(depId)
      if (!item) return false
      if (item.frontend?.mode === 'esm') return false
      if (!item.enabled) return false
      if (currentEnabledMap[depId] === false) return false
      return true
    }

    const blockedErrors = new Map<string, string>()
    const inDegree = new Map<string, number>()
    const dependents = new Map<string, string[]>()

    for (const [id, plugin] of wantLoadMap.entries()) {
      const deps = Array.isArray(plugin.depends_on) ? plugin.depends_on : []
      const missingDeps = deps.filter((d) => !isDepAvailable(d))
      if (missingDeps.length > 0) {
        blockedErrors.set(id, `缺少或未启用前置依赖插件：${missingDeps.join(', ')}`)
        continue
      }
      const esmDeps = deps.filter((d) => wantLoadMap.has(d))
      inDegree.set(id, esmDeps.length)
      for (const depId of esmDeps) {
        const list = dependents.get(depId) ?? []
        list.push(id)
        dependents.set(depId, list)
      }
    }

    // Propagate missing-dependency blocks transitively.
    const markBlockedCascade = (rootId: string, reason: string) => {
      const queue = [...(dependents.get(rootId) ?? [])]
      while (queue.length > 0) {
        const child = queue.shift()!
        if (blockedErrors.has(child)) continue
        blockedErrors.set(child, reason)
        inDegree.delete(child)
        for (const grand of dependents.get(child) ?? []) {
          queue.push(grand)
        }
      }
    }
    for (const [blockedId, reason] of Array.from(blockedErrors.entries())) {
      markBlockedCascade(blockedId, reason)
    }

    const loadSingle = (id: string): Promise<boolean> =>
      this.enqueue(id, async () => {
        const latest = this.desiredPlugins.get(id) ?? null
        const liveEnabledMap =
          enabledOverrideMap ?? usePluginSettingsStore.getState().enabledPlugins
        if (!latest || !shouldLoadEsmPlugin(latest, liveEnabledMap, appProfile)) {
          if (this.loaded.has(id) || this.runtimeStates.has(id)) {
            await this.unloadInternal(id)
          }
          return false
        }
        const existing = this.loaded.get(id)
        const nextDigest = (latest.content_digest || '').trim()
        if (existing && existing.digest === nextDigest) {
          return true
        }
        if (
          !existing &&
          this.failedDigestByPlugin.has(id) &&
          this.failedDigestByPlugin.get(id) === nextDigest
        ) {
          return false
        }
        if (existing) {
          await this.unloadInternal(id, true)
        }
        const res = await this.loadInternal(latest, {
          checkDesiredAfterAsync: true,
          appProfile,
        })
        return res.ok
      })

    // 3. Execute topological waves in order (parallel within each wave).
    const processed = new Set<string>()
    let wave = Array.from(inDegree.entries())
      .filter(([, deg]) => deg === 0)
      .map(([id]) => id)

    while (wave.length > 0) {
      const waveResults = await Promise.all(
        wave.map(async (id) => ({ id, ok: await loadSingle(id) })),
      )
      const nextWave: string[] = []
      for (const { id, ok } of waveResults) {
        processed.add(id)
        if (!ok) {
          markBlockedCascade(id, `前置依赖插件 "${id}" 加载失败`)
          continue
        }
        for (const child of dependents.get(id) ?? []) {
          if (blockedErrors.has(child) || !inDegree.has(child)) continue
          const nextDeg = (inDegree.get(child) ?? 1) - 1
          inDegree.set(child, nextDeg)
          if (nextDeg <= 0) {
            nextWave.push(child)
          }
        }
      }
      wave = nextWave
    }

    // 4. Any remaining nodes in inDegree form a dependency cycle.
    const cyclicIds = Array.from(inDegree.keys()).filter(
      (id) => !processed.has(id) && !blockedErrors.has(id),
    )
    for (const id of cyclicIds) {
      blockedErrors.set(id, `检测到插件循环依赖：${cyclicIds.join(' -> ')}`)
    }

    // 5. Surface blocked/cyclic errors cleanly and unload stale instances if any.
    for (const [id, error] of blockedErrors.entries()) {
      const plugin = wantLoadMap.get(id)
      const digest = (plugin?.content_digest || '').trim()
      if (this.loaded.has(id)) {
        await this.unloadInternal(id, true)
      }
      this.failedDigestByPlugin.set(id, digest)
      this.emitState(id, {
        status: 'error',
        digest,
        error,
        updatedAt: Date.now(),
      })
    }
  }

  async retryFailed(items?: PluginView[]): Promise<void> {
    if (this.failedDigestByPlugin.size === 0) return
    const failedIds: string[] = []
    for (const id of this.failedDigestByPlugin.keys()) {
      if (this.nonRetryableFailedPlugins.has(id)) continue
      failedIds.push(id)
    }
    if (failedIds.length === 0) return
    for (const id of failedIds) {
      this.failedDigestByPlugin.delete(id)
    }
    const list = items ?? useExtensionStore.getState().items
    const byId = new Map(list.map((item) => [item.id, item]))
    const tasks: Array<Promise<unknown>> = []
    for (const id of failedIds) {
      const plugin = byId.get(id)
      if (!plugin) continue
      this.desiredPlugins.set(id, plugin)
      tasks.push(
        this.enqueue(id, async () => {
          const latest = this.desiredPlugins.get(id) ?? null
          const enabledMap = usePluginSettingsStore.getState().enabledPlugins
          if (!latest || !shouldLoadEsmPlugin(latest, enabledMap)) {
            await this.unloadInternal(id)
            return
          }
          if (this.loaded.has(id)) return
          await this.loadInternal(latest, { forceCacheBust: true, checkDesiredAfterAsync: true })
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
    const onRecoverableReady = () => {
      if (this.failedDigestByPlugin.size > 0) {
        void this.retryFailed()
      }
    }
    if (typeof window !== 'undefined') {
      window.addEventListener('storage', onStorage)
      window.addEventListener(AUTH_READY_EVENT, onRecoverableReady)
      window.addEventListener('online', onRecoverableReady)
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
        window.removeEventListener(AUTH_READY_EVENT, onRecoverableReady)
        window.removeEventListener('online', onRecoverableReady)
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
    this.nonRetryableFailedPlugins.clear()
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
