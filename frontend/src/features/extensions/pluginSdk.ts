import { createElement, useSyncExternalStore, type ComponentType } from 'react'
import { notifications } from '@mantine/notifications'
import { authenticatedApiFetch } from '../../api/client'
import { extensionsApi, type PluginView } from '../../api/extensions'
import type { ArtifactRef } from '../../domain/artifact'
import type { Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { useAppProfile, type AppProfile } from '../office/appProfile'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import {
  databaseToolRegistry,
  type DatabaseToolProvider,
  type OpenSqlFileParams,
  type OpenSqlSnippetParams,
} from '../sidecar/databaseTool'
import { downloadFile as hostDownloadFile } from '../sidecar/fileTransfer'
import { artifactViewerRegistry } from '../sidecar/registry'
import { useSidecarStore } from '../sidecar/sidecarStore'
import type {
  ArtifactViewer,
  ArtifactViewerComponent,
  PluginCategory,
  ViewerConfigOption,
} from '../sidecar/types'
import { extensionRegistry } from './extensionRegistry'
import type {
  BuiltInExtensionRegistration,
  BuiltInGitMenuItem,
  BuiltInPanelRegistration,
  BuiltInWelcomeMenuItem,
  Disposable,
  ExtensionCommandRegistration,
  ExtensionPageComponent,
  ExtensionPageIcon,
  ExtensionPageRegistration,
  StatusBarSlotContext,
} from './types'

export const SDK_VERSION = 1 as const

export type { DatabaseToolProvider, Disposable, OpenSqlFileParams, OpenSqlSnippetParams }

export interface PluginNotifyOptions {
  message: string
  title?: string
  color?: string
  autoClose?: number | boolean
}

export interface PluginOpenPanelOptions {
  title?: string
  mediaType?: string
  viewerId?: string
  sessionId?: string
  agentId?: string
  workspacePath?: string
  connectionId?: string
  params?: Record<string, unknown>
}

export interface PluginOpenSettingsOptions {
  tab?: string
  pluginId?: string
}

export type ComposerQuoteTarget =
  | Pick<Session, 'id' | 'agent_id'>
  | { sessionId?: string; agentId?: string }

export interface PluginPageContribution
  extends Omit<ExtensionPageRegistration, 'pluginId' | 'source'> {
  id: string
  title: string
  component: ExtensionPageComponent
  icon?: ExtensionPageIcon
}

export interface PluginViewerContribution {
  id?: string
  title: string
  description?: string
  version?: string
  category?: PluginCategory
  capabilitySlot?: string
  capabilityLabel?: string
  providerConfigKey?: string
  implementationName?: string
  badgeLabel?: string
  tabColor?: string
  icon?: ComponentType<{ size?: number; className?: string }>
  extensions?: string[]
  mimeTypes?: string[]
  supports?: (artifact: ArtifactRef) => number
  component: ArtifactViewerComponent
  renderBadge?: ArtifactViewer['renderBadge']
  capabilities?: {
    canEdit: boolean
  }
  configOptions?: ViewerConfigOption[]
  settingsAction?: ArtifactViewer['settingsAction']
  documentation?: ArtifactViewer['documentation']
}

export interface PluginCommandContribution
  extends Omit<ExtensionCommandRegistration, 'pluginId' | 'source' | 'keywords'> {
  id: string
  title: string
  keywords?: string | string[]
  run: () => void | Promise<void>
}

export interface PluginSlotContribution {
  settingsTitle?: string
  settingsButtonLabel?: string
  settingsComponent?: ComponentType<{ embedded?: boolean; onClose?: () => void }>
  statusBarComponent?: ComponentType<StatusBarSlotContext>
  gitMenuItems?: BuiltInGitMenuItem[]
  welcomeMenuItems?: BuiltInWelcomeMenuItem[]
  panels?: Record<string, BuiltInPanelRegistration>
}

export interface AstrorderPluginApi {
  fetch(path: string, init?: RequestInit): Promise<Response>
  fetchExt<T = unknown>(subpath: string, init?: RequestInit): Promise<T>
  notify(opts: PluginNotifyOptions): void
  navigate(path: string): void
  openArtifact(artifact: ArtifactRef, viewerId?: string): void
  openPluginPanel(panelId: string, opts?: PluginOpenPanelOptions): void
  openDatabaseWorkbench(
    mediaType: string,
    viewerId?: string,
    sessionId?: string,
    agentId?: string,
    connectionId?: string,
  ): void
  openSettings(opts?: PluginOpenSettingsOptions): void
  quoteToComposer(session: ComposerQuoteTarget, text: string): void
  openExternal(url: string): void
  downloadFile(url: string, filename: string): Promise<void>
  getConfig<T = Record<string, unknown>>(): T
  onConfigChange(cb: (cfg: Record<string, unknown>) => void): Disposable
  usePluginConfig<T = unknown>(key: string, fallback: T): T
  getActiveSession(): Session | null
  getSessions(): Session[]
  useAppProfile(): AppProfile
  useCurrentSession(): Session | null
  useThemeMode(): 'light' | 'dark'
}

export interface AstrorderPluginContext {
  readonly pluginId: string
  readonly version: string
  readonly subscriptions: Disposable[]
  registerPage(spec: PluginPageContribution): Disposable
  registerViewer(spec: PluginViewerContribution): Disposable
  registerSlot(spec: PluginSlotContribution): Disposable
  registerCommand(spec: PluginCommandContribution): Disposable
  registerDatabaseTool(provider: DatabaseToolProvider): Disposable
  readonly api: AstrorderPluginApi
}

export interface AstrorderPluginModule {
  activate(ctx: AstrorderPluginContext): void | Promise<void>
  deactivate?(): void | Promise<void>
}

export interface ScopedPluginMetadata {
  id: string
  version?: string
  category?: string
}

const VALID_PLUGIN_CATEGORIES = new Set<PluginCategory>([
  'agent',
  'development',
  'office',
  'workspace',
  'diagram',
  'media',
  'runtime',
])

function resolvePluginCategory(raw?: string): PluginCategory | undefined {
  if (raw && VALID_PLUGIN_CATEGORIES.has(raw as PluginCategory)) {
    return raw as PluginCategory
  }
  return undefined
}

function DefaultEsmPluginIcon({
  size = 16,
  className,
}: {
  size?: number
  className?: string
}) {
  return createElement(
    'svg',
    {
      width: size,
      height: size,
      viewBox: '0 0 24 24',
      fill: 'none',
      stroke: 'currentColor',
      strokeWidth: 1.8,
      strokeLinecap: 'round',
      strokeLinejoin: 'round',
      className,
      'aria-hidden': 'true',
    },
    createElement('path', {
      d: 'M12 3l8 4.5v9L12 21l-8-4.5v-9L12 3z',
    }),
    createElement('path', {
      d: 'M12 12l8-4.5',
    }),
    createElement('path', {
      d: 'M12 12v9',
    }),
    createElement('path', {
      d: 'M12 12L4 7.5',
    }),
  )
}

function resolveActiveSessionFromStores(
  activeSessionKey: string | null,
  sessions: Record<string, Session>,
): Session | null {
  if (activeSessionKey && sessions[activeSessionKey]) {
    return sessions[activeSessionKey]
  }

  if (typeof window !== 'undefined') {
    const match = window.location.pathname.match(/^\/(?:mobile\/)?chat\/([^/?#]+)/)
    if (match) {
      let sessionId = match[1]
      try {
        sessionId = decodeURIComponent(match[1])
      } catch {
        // keep raw segment if malformed
      }
      const agentId = new URLSearchParams(window.location.search).get('agent_id')
      if (agentId) {
        const exact = sessions[`${agentId}::${sessionId}`]
        if (exact) return exact
      }
      const matched = Object.values(sessions).find((item) => item.id === sessionId)
      if (matched) return matched
    }
  }

  return null
}

export function defineAstrorderPlugin<T extends AstrorderPluginModule>(mod: T): T {
  return mod
}

export function notify(opts: PluginNotifyOptions): void {
  notifications.show({
    message: opts.message,
    title: opts.title,
    color: opts.color,
    autoClose: opts.autoClose,
  })
}

export function navigate(path: string): void {
  if (typeof window === 'undefined') return
  const normalized = path.trim()
  if (!normalized.startsWith('/') || normalized.startsWith('//')) {
    return
  }
  window.dispatchEvent(new CustomEvent('astrorder:navigate', { detail: { to: normalized } }))
}

export function openArtifact(artifact: ArtifactRef, viewerId?: string): void {
  const targetViewerId = viewerId || artifactViewerRegistry.findViewer(artifact)?.id || 'monaco-viewer'
  useSidecarStore.getState().openArtifact(artifact, targetViewerId)
}

export function getActiveSession(): Session | null {
  return resolveActiveSessionFromStores(
    useSidecarStore.getState().activeSessionKey,
    useAstrorderStore.getState().sessions,
  )
}

export function getSessions(): Session[] {
  return Object.values(useAstrorderStore.getState().sessions)
}

export function openPluginPanel(
  pluginId: string,
  panelId: string,
  opts?: PluginOpenPanelOptions,
): void {
  const active = getActiveSession()
  const sessionId = opts?.sessionId ?? active?.id ?? ''
  const agentId = opts?.agentId ?? active?.agent_id ?? ''
  const fallbackSession = opts?.sessionId === undefined ? active : null
  useSidecarStore.getState().openPluginPanel(pluginId, panelId, {
    title: opts?.title,
    mediaType: opts?.mediaType,
    viewerId: opts?.viewerId,
    sessionId,
    agentId,
    workspacePath: opts?.workspacePath ?? (fallbackSession?.workspace || undefined),
    connectionId: opts?.connectionId ?? (fallbackSession?.connection_id || undefined),
    params: opts?.params,
  })
}

export function openDatabaseWorkbench(
  mediaType: string,
  viewerId: string,
  sessionId?: string,
  agentId?: string,
  connectionId?: string,
): void {
  useSidecarStore
    .getState()
    .openDatabaseWorkbench(mediaType, viewerId, sessionId, agentId, connectionId)
}

export function openSettings(opts?: PluginOpenSettingsOptions): void {
  if (typeof window === 'undefined') return
  window.dispatchEvent(
    new CustomEvent('astrorder:open-settings', {
      detail: opts ? { ...opts } : {},
    }),
  )
}

export function quoteToComposer(session: ComposerQuoteTarget, text: string): void {
  if (typeof window === 'undefined') return
  const agentId = 'agent_id' in session ? session.agent_id : session.agentId || ''
  const sessionId = 'id' in session ? session.id : session.sessionId || ''
  window.dispatchEvent(
    new CustomEvent('astrorder:quote-to-composer', {
      detail: {
        agentId,
        sessionId,
        text,
      },
    }),
  )
}

export function openExternal(url: string): void {
  if (typeof window === 'undefined') return
  const trimmed = url.trim()
  if (!trimmed) return
  let targetUrl = trimmed
  try {
    const parsed = new URL(trimmed, window.location.origin)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return
    }
    targetUrl = parsed.toString()
  } catch {
    return
  }
  const desktop = (
    window as unknown as {
      astrorderDesktop?: { openExternal?: (address: string) => Promise<void> | void }
    }
  ).astrorderDesktop
  if (typeof desktop?.openExternal === 'function') {
    void Promise.resolve(desktop.openExternal(targetUrl)).catch(() => {
      window.open(targetUrl, '_blank', 'noopener,noreferrer')
    })
    return
  }
  window.open(targetUrl, '_blank', 'noopener,noreferrer')
}

export function downloadFile(url: string, filename: string): Promise<void> {
  return hostDownloadFile(url, filename)
}

export function usePluginConfig<T = unknown>(
  pluginId: string,
  key: string,
  fallback: T,
): T {
  return usePluginSettingsStore((state) => {
    const conf = state.pluginConfigs[pluginId]
    if (conf && conf[key] !== undefined) {
      return conf[key] as T
    }
    return fallback
  })
}

export function useCurrentSession(): Session | null {
  const activeSessionKey = useSidecarStore((s) => s.activeSessionKey)
  const sessions = useAstrorderStore((s) => s.sessions)
  return resolveActiveSessionFromStores(activeSessionKey, sessions)
}

function readThemeModeSnapshot(): 'light' | 'dark' {
  if (typeof document !== 'undefined') {
    const attr = document.documentElement.getAttribute('data-mantine-color-scheme')
    if (attr === 'dark' || attr === 'light') {
      return attr
    }
  }
  if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
    try {
      if (window.matchMedia('(prefers-color-scheme: dark)').matches) {
        return 'dark'
      }
    } catch {
      // ignore matchMedia errors in restricted environments
    }
  }
  return 'light'
}

function subscribeThemeMode(onStoreChange: () => void): () => void {
  const cleanups: Array<() => void> = []
  if (typeof document !== 'undefined' && typeof MutationObserver !== 'undefined') {
    const observer = new MutationObserver(() => onStoreChange())
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-mantine-color-scheme'],
    })
    cleanups.push(() => observer.disconnect())
  }
  if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
    try {
      const mql = window.matchMedia('(prefers-color-scheme: dark)')
      const listener = () => onStoreChange()
      if (typeof mql.addEventListener === 'function') {
        mql.addEventListener('change', listener)
        cleanups.push(() => mql.removeEventListener('change', listener))
      }
    } catch {
      // ignore matchMedia subscription errors
    }
  }
  return () => {
    for (const fn of cleanups) {
      fn()
    }
  }
}

export function useThemeMode(): 'light' | 'dark' {
  return useSyncExternalStore(subscribeThemeMode, readThemeModeSnapshot, () => 'light')
}

export function getHostRuntime(): unknown {
  return (globalThis as Record<string, unknown>).__ASTRORDER_PLUGIN_RUNTIME_V1__
}

export const sdkRuntimeExports = Object.freeze({
  SDK_VERSION,
  defineAstrorderPlugin,
  downloadFile,
  getActiveSession,
  getHostRuntime,
  getSessions,
  navigate,
  notify,
  openArtifact,
  openDatabaseWorkbench,
  openExternal,
  openPluginPanel,
  openSettings,
  quoteToComposer,
  useAppProfile,
  useCurrentSession,
  usePluginConfig,
  useThemeMode,
})

export type AstrorderSdkRuntimeExports = typeof sdkRuntimeExports

export function disposePluginSubscriptions(subscriptions: Disposable[]): void {
  for (let i = subscriptions.length - 1; i >= 0; i -= 1) {
    try {
      subscriptions[i]?.dispose()
    } catch (err) {
      console.error('[pluginSdk] Failed to dispose subscription:', err)
    }
  }
  subscriptions.length = 0
}

function validateExtSubpath(subpath: string): string {
  const trimmed = subpath.trim()
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(trimmed) || trimmed.startsWith('//')) {
    throw new Error(`Invalid extension subpath: ${subpath}`)
  }
  const clean = trimmed.replace(/^\/+/, '')
  const pathOnly = clean.split(/[?#]/)[0]
  const segments = pathOnly.split('/')
  if (segments.some((seg) => seg === '..' || seg.includes('\\'))) {
    throw new Error(`Path traversal is not allowed in extension subpath: ${subpath}`)
  }
  return clean
}

export interface ScopedPluginContextHandle {
  ctx: AstrorderPluginContext
  registeredViewerIds: Set<string>
  registeredPageIds: Set<string>
}

export function createScopedPluginContextWithHandle(
  plugin: PluginView | ScopedPluginMetadata,
): ScopedPluginContextHandle {
  const pluginId = plugin.id
  const version = plugin.version || '0.0.0'
  const defaultCategory = resolvePluginCategory(plugin.category)
  const subscriptions: Disposable[] = []
  const registeredViewerIds = new Set<string>()
  const registeredPageIds = new Set<string>()

  const track = (disposable: Disposable): Disposable => {
    let disposed = false
    const safeDisposable: Disposable = {
      dispose: () => {
        if (disposed) return
        disposed = true
        disposable.dispose()
      },
    }
    subscriptions.push(safeDisposable)
    return safeDisposable
  }

  const api: AstrorderPluginApi = {
    fetch: (path: string, init?: RequestInit) => authenticatedApiFetch(path, init),

    fetchExt: <T = unknown>(subpath: string, init?: RequestInit) => {
      const safeSubpath = validateExtSubpath(subpath)
      return extensionsApi.fetchExtensionEndpoint<T>(pluginId, safeSubpath, undefined, init)
    },

    notify,
    navigate,
    openArtifact,

    openPluginPanel: (panelId: string, opts?: PluginOpenPanelOptions) => {
      openPluginPanel(pluginId, panelId, opts)
    },

    openDatabaseWorkbench: (
      mediaType: string,
      viewerId?: string,
      sessionId?: string,
      agentId?: string,
      connectionId?: string,
    ) => {
      openDatabaseWorkbench(mediaType, viewerId || pluginId, sessionId, agentId, connectionId)
    },

    openSettings: (opts?: PluginOpenSettingsOptions) => {
      openSettings(opts ?? { tab: 'plugins', pluginId })
    },

    quoteToComposer,
    openExternal,
    downloadFile,

    getConfig: <T = Record<string, unknown>>(): T =>
      ((usePluginSettingsStore.getState().pluginConfigs[pluginId] || {}) as T),

    onConfigChange: (cb: (cfg: Record<string, unknown>) => void): Disposable => {
      const unsubscribe = usePluginSettingsStore.subscribe((state, prevState) => {
        const nextCfg = state.pluginConfigs[pluginId]
        const prevCfg = prevState.pluginConfigs[pluginId]
        if (nextCfg !== prevCfg) {
          cb(nextCfg || {})
        }
      })
      return track({ dispose: unsubscribe })
    },

    usePluginConfig: <T = unknown>(key: string, fallback: T): T =>
      usePluginConfig<T>(pluginId, key, fallback),

    getActiveSession,
    getSessions,
    useAppProfile,
    useCurrentSession,
    useThemeMode,
  }

  const ctx: AstrorderPluginContext = {
    pluginId,
    version,
    subscriptions,

    registerPage: (spec: PluginPageContribution): Disposable => {
      const registration: ExtensionPageRegistration = {
        ...spec,
        pluginId,
        source: 'esm',
      }
      registeredPageIds.add(spec.id)
      const base = extensionRegistry.registerPage(registration)
      return track({
        dispose: () => {
          registeredPageIds.delete(spec.id)
          base.dispose()
        },
      })
    },

    registerViewer: (spec: PluginViewerContribution): Disposable => {
      const viewerId = spec.id?.trim() || pluginId
      const viewer: ArtifactViewer = {
        ...spec,
        id: viewerId,
        pluginId,
        source: 'esm',
        version: spec.version || version,
        category: spec.category || defaultCategory,
        icon: spec.icon || DefaultEsmPluginIcon,
        capabilities: spec.capabilities ?? { canEdit: false },
      }
      registeredViewerIds.add(viewerId)
      const base = extensionRegistry.registerViewer(viewer)
      return track({
        dispose: () => {
          registeredViewerIds.delete(viewerId)
          base.dispose()
        },
      })
    },

    registerSlot: (spec: PluginSlotContribution): Disposable => {
      const registration: BuiltInExtensionRegistration = {
        ...spec,
        pluginId,
      }
      const base = extensionRegistry.registerBuiltin(registration)
      return track(base)
    },

    registerCommand: (spec: PluginCommandContribution): Disposable => {
      const keywords = Array.isArray(spec.keywords) ? spec.keywords.join(' ') : spec.keywords
      const registration: ExtensionCommandRegistration = {
        ...spec,
        keywords,
        pluginId,
        source: 'esm',
      }
      const base = extensionRegistry.registerCommand(registration)
      return track(base)
    },

    registerDatabaseTool: (provider: DatabaseToolProvider): Disposable => {
      const normalized: DatabaseToolProvider = {
        ...provider,
        id: provider.id?.trim() || pluginId,
      }
      const base = databaseToolRegistry.register(normalized)
      return track(base)
    },

    api,
  }

  return {
    ctx,
    registeredViewerIds,
    registeredPageIds,
  }
}

export function createScopedPluginContext(
  plugin: PluginView | ScopedPluginMetadata,
): AstrorderPluginContext {
  return createScopedPluginContextWithHandle(plugin).ctx
}
