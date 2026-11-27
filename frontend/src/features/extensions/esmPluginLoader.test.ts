import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { notifications } from '@mantine/notifications'
import { QueryClient } from '@tanstack/react-query'
import type { PluginView } from '../../api/extensions'
import { extensionsApi } from '../../api/extensions'
import type { ArtifactRef } from '../../domain/artifact'
import type { EventEnvelope, Session } from '../../domain/types'
import { useEventStream } from '../../hooks/useEventStream'
import { useAstrorderStore } from '../../state/store'
import { APP_PROFILE_PLUGIN_KEY, clearStoredAppProfileForTest, setAppProfile } from '../office/appProfile'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { databaseToolRegistry, useHasDatabaseTool } from '../sidecar/databaseTool'
import { artifactViewerRegistry } from '../sidecar/registry'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { extensionRegistry } from './extensionRegistry'
import { useExtensionStore } from './extensionStore'
import {
  buildEsmAssetUrl,
  createEsmPluginLoader,
  isEsmPluginHiddenInOfficeMode,
  shouldLoadEsmPlugin,
  type RawEsmPluginModule,
} from './esmPluginLoader'
import {
  createScopedPluginContext,
  defineAstrorderPlugin,
  disposePluginSubscriptions,
  type AstrorderPluginContext,
} from './pluginSdk'
import {
  installPluginRuntimeBridge,
  PLUGIN_RUNTIME_GLOBAL_KEY,
  PLUGIN_RUNTIME_V1,
} from './runtimeBridge'

const streamMocks = vi.hoisted(() => ({
  connectEventStream: vi.fn(),
}))

vi.mock('../../api/eventStream', () => ({
  connectEventStream: streamMocks.connectEventStream,
}))

function makeEsmPlugin(overrides: Partial<PluginView> = {}): PluginView {
  return {
    id: 'three-viewer',
    name: '3D 模型/CAD 预览',
    version: '1.0.0',
    category: 'media',
    description: '官方 3D/CAD 预览热加载插件',
    source: 'official',
    enabled: true,
    permissions: [],
    granted_permissions: [],
    pending_permissions: [],
    permissions_granted: true,
    runtime_state: 'ready',
    backend_type: 'none',
    default_enabled: false,
    content_digest: 'digest-v1-aaaaaa',
    signature_verified: true,
    esm_base_url: '/api/v1/ext/three-viewer/_esm',
    frontend: {
      mode: 'esm',
      entry: 'ui/entry.js',
      styles: ['ui/entry.css'],
      sdk_version: '^1.0',
    },
    contributes: {
      viewers: [
        {
          id: 'three-viewer',
          title: '3D 模型/CAD 预览',
          extensions: ['.stl', '.obj', '.gltf', '.glb'],
        },
      ],
    },
    ...overrides,
  }
}

afterEach(() => {
  vi.restoreAllMocks()
  streamMocks.connectEventStream.mockReset()
  localStorage.clear()
  clearStoredAppProfileForTest()
  document.documentElement.removeAttribute('data-mantine-color-scheme')
  document.querySelectorAll('link[data-astrorder-ext-css]').forEach((el) => el.remove())
  usePluginSettingsStore.getState().reset()
  useExtensionStore.getState().reset()
  useSidecarStore.setState({ tabs: [], mountedTabs: {}, activeTabId: '', activeSessionKey: null })
  useAstrorderStore.getState().resetRuntime()
  extensionRegistry.clear()
  artifactViewerRegistry.unregister('three-viewer')
  artifactViewerRegistry.unregister('bad-viewer')
  artifactViewerRegistry.unregister('good-viewer')
  artifactViewerRegistry.unregister('drawio-viewer')
  artifactViewerRegistry.unregister('dbx-viewer')
  artifactViewerRegistry.unregister('gitlab-ci-viewer')
  databaseToolRegistry.unregister('dbx-viewer')
  delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop
})

describe('Host Runtime Bridge (__ASTRORDER_PLUGIN_RUNTIME_V1__)', () => {
  it('installs a frozen runtime bridge with explicit named exports and no TablerIcons bloat', () => {
    const bridge = installPluginRuntimeBridge()
    const repeat = installPluginRuntimeBridge()

    expect(bridge).toBe(PLUGIN_RUNTIME_V1)
    expect(repeat).toBe(bridge)
    expect((globalThis as Record<string, unknown>)[PLUGIN_RUNTIME_GLOBAL_KEY]).toBe(bridge)
    expect(Object.isFrozen(bridge)).toBe(true)
    expect(bridge.apiVersion).toBe(1)

    // Strict alignment with backend sdk_esm.go symbol counts
    expect(Object.keys(bridge.react).sort()).toHaveLength(37)
    expect(Object.keys(bridge.jsxRuntime).sort()).toEqual(['Fragment', 'jsx', 'jsxs'])
    expect(Object.keys(bridge.reactDom).sort()).toHaveLength(12)
    expect(Object.keys(bridge.reactDomClient).sort()).toEqual(['createRoot', 'hydrateRoot'])
    expect(Object.keys(bridge.mantineCore).sort()).toHaveLength(63)
    expect(Object.keys(bridge.mantineNotifications).sort()).toHaveLength(12)
    expect(Object.keys(bridge.sdk).sort()).toEqual([
      'SDK_VERSION',
      'defineAstrorderPlugin',
      'downloadFile',
      'getActiveSession',
      'getHostRuntime',
      'getSessions',
      'navigate',
      'notify',
      'openArtifact',
      'openDatabaseWorkbench',
      'openExternal',
      'openPluginPanel',
      'openSettings',
      'quoteToComposer',
      'useAppProfile',
      'useCurrentSession',
      'usePluginConfig',
      'useThemeMode',
    ])
    expect('tablerIcons' in bridge).toBe(false)

    // Ensure zero undefined exports across every shared runtime namespace
    for (const ns of [
      bridge.react,
      bridge.jsxRuntime,
      bridge.reactDom,
      bridge.reactDomClient,
      bridge.mantineCore,
      bridge.mantineNotifications,
      bridge.sdk,
    ]) {
      for (const [key, value] of Object.entries(ns)) {
        expect(value, `missing runtime export ${key}`).toBeDefined()
      }
    }

    // Non-writable & non-configurable global property
    const desc = Object.getOwnPropertyDescriptor(globalThis, PLUGIN_RUNTIME_GLOBAL_KEY)
    expect(desc?.writable).toBe(false)
    expect(desc?.configurable).toBe(false)
  })
})

describe('EsmPluginLoader lifecycle, rollback, hot-swap & serialization', () => {
  it('performs zero loads when plugin is disabled, unapproved, or not mode=esm', async () => {
    const importer = vi.fn<(_: string) => Promise<RawEsmPluginModule>>()
    const styleLoader = vi.fn()
    const loader = createEsmPluginLoader({ importer, styleLoader })

    const disabledPlugin = makeEsmPlugin({ enabled: false })
    const unapprovedPlugin = makeEsmPlugin({
      id: 'acme.unapproved',
      source: 'external',
      enabled: true,
      permissions: ['ui:slots'],
      pending_permissions: ['ui:slots'],
      permissions_granted: false,
    })
    const iframePlugin = makeEsmPlugin({
      id: 'acme.iframe',
      frontend: { mode: 'iframe', entry: 'panel.html' },
    })

    expect(shouldLoadEsmPlugin(disabledPlugin)).toBe(false)
    expect(shouldLoadEsmPlugin(unapprovedPlugin)).toBe(false)
    expect(shouldLoadEsmPlugin(iframePlugin)).toBe(false)

    await loader.syncLoadedPlugins([disabledPlugin, unapprovedPlugin, iframePlugin])

    expect(importer).not.toHaveBeenCalled()
    expect(styleLoader).not.toHaveBeenCalled()
    expect(document.querySelectorAll('link[data-astrorder-ext-css]')).toHaveLength(0)
    expect(loader.isLoaded('three-viewer')).toBe(false)
    expect(artifactViewerRegistry.getViewerById('three-viewer')).toBeNull()
  })

  it('injects CSS before importing, invokes activate(ctx), and cleans up fully on disable', async () => {
    const order: string[] = []
    const disposeOrder: string[] = []
    const deactivateSpy = vi.fn(() => {
      order.push('deactivate')
    })

    const importer = vi.fn(async (url: string): Promise<RawEsmPluginModule> => {
      order.push(`import:${url}`)
      return {
        default: defineAstrorderPlugin({
          activate(ctx: AstrorderPluginContext) {
            order.push(`activate:${ctx.pluginId}@${ctx.version}`)
            ctx.registerViewer({
              id: 'three-viewer',
              title: '3D 模型/CAD 预览',
              extensions: ['.glb', '.stl'],
              component: () => null,
            })
            ctx.registerPage({
              id: 'studio',
              title: '3D 工作室',
              component: () => null,
            })
            ctx.registerCommand({
              id: 'reset-cam',
              title: '重置 3D 视角',
              run: () => {},
            })
            ctx.subscriptions.push({ dispose: () => disposeOrder.push('first') })
            ctx.subscriptions.push({ dispose: () => disposeOrder.push('second') })
          },
          deactivate: deactivateSpy,
        }),
      }
    })

    const loader = createEsmPluginLoader({ importer })
    const plugin = makeEsmPlugin()

    await loader.syncLoadedPlugins([plugin])

    // CSS injected with data-astrorder-ext-css and digest cache-buster
    const cssLink = document.querySelector<HTMLLinkElement>('link[data-astrorder-ext-css="three-viewer"]')
    expect(cssLink).not.toBeNull()
    expect(cssLink?.getAttribute('href')).toBe('/api/v1/ext/three-viewer/_esm/ui/entry.css?v=digest-v1-aaaaaa')
    expect(order).toEqual([
      'import:/api/v1/ext/three-viewer/_esm/ui/entry.js?v=digest-v1-aaaaaa',
      'activate:three-viewer@1.0.0',
    ])
    expect(loader.isLoaded('three-viewer')).toBe(true)
    expect(useExtensionStore.getState().esmStates['three-viewer']?.status).toBe('loaded')
    expect(artifactViewerRegistry.getViewerById('three-viewer')).not.toBeNull()
    expect(extensionRegistry.getPageById('three-viewer', 'studio')).not.toBeUndefined()
    expect(extensionRegistry.getAllCommands().some((c) => c.pluginId === 'three-viewer')).toBe(true)

    // Open a Sidecar tab belonging to this plugin to verify unload closes it
    const artifact: ArtifactRef = {
      id: 'artifact:model.glb',
      name: 'model.glb',
      kind: 'workspace_file',
      mediaType: 'model/gltf-binary',
      readUrl: '/api/v1/files/raw?path=model.glb',
      writable: false,
      sessionId: 'sess-1',
      agentId: 'agent-1',
    }
    useSidecarStore.getState().openArtifact(artifact, 'three-viewer')
    expect(useSidecarStore.getState().tabs).toHaveLength(1)

    // Disable -> deactivate -> reverse dispose -> remove CSS -> close tab
    await loader.syncLoadedPlugins([{ ...plugin, enabled: false }])

    expect(deactivateSpy).toHaveBeenCalledTimes(1)
    expect(disposeOrder).toEqual(['second', 'first'])
    expect(document.querySelectorAll('link[data-astrorder-ext-css="three-viewer"]')).toHaveLength(0)
    expect(artifactViewerRegistry.getViewerById('three-viewer')).toBeNull()
    expect(extensionRegistry.getPageById('three-viewer', 'studio')).toBeUndefined()
    expect(extensionRegistry.getAllCommands().some((c) => c.pluginId === 'three-viewer')).toBe(false)
    expect(useSidecarStore.getState().tabs).toHaveLength(0)
    expect(loader.isLoaded('three-viewer')).toBe(false)
    expect(useExtensionStore.getState().esmStates['three-viewer']).toBeUndefined()
  })

  it('hot-swaps loaded plugin in-place when content_digest changes without page refresh', async () => {
    const events: string[] = []
    const navEvents: string[] = []
    const onNavigate = (e: Event) => {
      const to = (e as CustomEvent<{ to?: string }>).detail?.to
      if (to) navEvents.push(to)
    }
    window.addEventListener('astrorder:navigate', onNavigate)
    const prevPath = `${window.location.pathname}${window.location.search}`

    try {
      const importer = vi.fn(async (url: string): Promise<RawEsmPluginModule> => {
        const isV2 = url.includes('v2-bbbbbb')
        return {
          activate(ctx) {
            events.push(isV2 ? 'activate:v2' : 'activate:v1')
            ctx.registerViewer({
              id: 'three-viewer',
              title: isV2 ? '3D Viewer v2' : '3D Viewer v1',
              extensions: ['.glb'],
              component: () => null,
            })
            ctx.subscriptions.push({
              dispose: () => events.push(isV2 ? 'dispose:v2' : 'dispose:v1'),
            })
          },
          deactivate() {
            events.push(isV2 ? 'deactivate:v2' : 'deactivate:v1')
          },
        }
      })

      const loader = createEsmPluginLoader({ importer })
      const v1 = makeEsmPlugin({ content_digest: 'v1-aaaaaa' })
      await loader.syncLoadedPlugins([v1])

      // Open a Sidecar tab & visit an extension page route belonging to this plugin
      window.history.pushState({}, '', '/ext-page/three-viewer/studio')
      const artifact: ArtifactRef = {
        id: 'artifact:hot-swap.glb',
        name: 'hot-swap.glb',
        kind: 'workspace_file',
        mediaType: 'model/gltf-binary',
        readUrl: '/api/v1/files/raw?path=hot-swap.glb',
        writable: false,
        sessionId: 'sess-1',
        agentId: 'agent-1',
      }
      useSidecarStore.getState().openArtifact(artifact, 'three-viewer')
      expect(useSidecarStore.getState().tabs).toHaveLength(1)

      // Same digest re-sync must be a no-op
      await loader.syncLoadedPlugins([v1])
      expect(importer).toHaveBeenCalledTimes(1)
      expect(artifactViewerRegistry.getViewerById('three-viewer')?.title).toBe('3D Viewer v1')

      // Upgrade to v2 digest -> in-place hot-swap MUST keep open Sidecar tab and NOT navigate away
      const v2 = makeEsmPlugin({ version: '1.1.0', content_digest: 'v2-bbbbbb' })
      await loader.syncLoadedPlugins([v2])

      expect(importer).toHaveBeenCalledTimes(2)
      expect(importer).toHaveBeenLastCalledWith('/api/v1/ext/three-viewer/_esm/ui/entry.js?v=v2-bbbbbb')
      expect(events).toEqual(['activate:v1', 'deactivate:v1', 'dispose:v1', 'activate:v2'])
      expect(artifactViewerRegistry.getViewerById('three-viewer')?.title).toBe('3D Viewer v2')
      expect(useSidecarStore.getState().tabs).toHaveLength(1)
      expect(useSidecarStore.getState().tabs[0].id).toBe('artifact:hot-swap.glb')
      expect(navEvents).toEqual([])

      // Manual reloadPlugin also keeps open Sidecar tab and route intact
      const reloadRes = await loader.reloadPlugin(v2)
      expect(reloadRes.ok).toBe(true)
      expect(useSidecarStore.getState().tabs).toHaveLength(1)
      expect(navEvents).toEqual([])

      const links = document.querySelectorAll<HTMLLinkElement>('link[data-astrorder-ext-css="three-viewer"]')
      expect(links).toHaveLength(1)
      expect(links[0].getAttribute('href')).toMatch(/^\/api\/v1\/ext\/three-viewer\/_esm\/ui\/entry\.css\?v=v2-bbbbbb&r=\d+$/)
      expect(loader.getLoadedDigest('three-viewer')).toBe('v2-bbbbbb')

      // Only an actual disable/unload closes the Sidecar tab and navigates away from the extension route
      await loader.syncLoadedPlugins([{ ...v2, enabled: false }])
      expect(useSidecarStore.getState().tabs).toHaveLength(0)
      expect(navEvents).toEqual(['/chat'])
    } finally {
      window.removeEventListener('astrorder:navigate', onNavigate)
      window.history.pushState({}, '', prevPath)
    }
  })

  it('rolls back subscriptions and CSS on activation failure without breaking sibling plugins', async () => {
    const partialDispose = vi.fn()
    let failBadPlugin = true

    const importer = vi.fn(async (url: string): Promise<RawEsmPluginModule> => {
      if (url.includes('/bad-plugin/')) {
        return {
          activate(ctx) {
            ctx.registerViewer({
              id: 'bad-viewer',
              title: 'Bad Viewer',
              extensions: ['.bad'],
              component: () => null,
            })
            ctx.subscriptions.push({ dispose: partialDispose })
            if (failBadPlugin) {
              throw new Error('WebGL init exploded')
            }
          },
        }
      }
      return {
        activate(ctx) {
          ctx.registerViewer({
            id: 'good-viewer',
            title: 'Good Viewer',
            extensions: ['.good'],
            component: () => null,
          })
        },
      }
    })

    const loader = createEsmPluginLoader({ importer })
    const badPlugin = makeEsmPlugin({
      id: 'bad-plugin',
      esm_base_url: '/api/v1/ext/bad-plugin/_esm',
      content_digest: 'bad-1',
    })
    const goodPlugin = makeEsmPlugin({
      id: 'good-plugin',
      esm_base_url: '/api/v1/ext/good-plugin/_esm',
      content_digest: 'good-1',
    })

    await loader.syncLoadedPlugins([badPlugin, goodPlugin])

    // Bad plugin rolled back cleanly
    expect(partialDispose).toHaveBeenCalledTimes(1)
    expect(artifactViewerRegistry.getViewerById('bad-viewer')).toBeNull()
    expect(document.querySelectorAll('link[data-astrorder-ext-css="bad-plugin"]')).toHaveLength(0)
    expect(loader.isLoaded('bad-plugin')).toBe(false)
    expect(loader.getState('bad-plugin')?.status).toBe('error')
    expect(loader.getState('bad-plugin')?.error).toContain('WebGL init exploded')
    expect(useExtensionStore.getState().esmStates['bad-plugin']?.status).toBe('error')

    // Good sibling plugin loaded unaffected
    expect(loader.isLoaded('good-plugin')).toBe(true)
    expect(artifactViewerRegistry.getViewerById('good-viewer')).not.toBeNull()

    // Same digest re-sync does NOT infinite-retry the broken plugin
    await loader.syncLoadedPlugins([badPlugin, goodPlugin])
    expect(importer).toHaveBeenCalledTimes(2)

    // Explicit reload recovers with cache-busting query param
    failBadPlugin = false
    const reloadRes = await loader.reloadPlugin(badPlugin)
    expect(reloadRes.ok).toBe(true)
    expect(importer).toHaveBeenLastCalledWith(expect.stringMatching(/entry\.js\?v=bad-1&r=\d+/))
    expect(loader.isLoaded('bad-plugin')).toBe(true)
    expect(artifactViewerRegistry.getViewerById('bad-viewer')).not.toBeNull()
  })

  it('serializes concurrent rapid enable/disable toggles without race conditions or leaked CSS', async () => {
    let releaseFirstImport!: () => void
    const firstImportGate = new Promise<void>((resolve) => {
      releaseFirstImport = resolve
    })
    let callCount = 0
    let activeCount = 0
    let maxActiveCount = 0

    const importer = vi.fn(async (): Promise<RawEsmPluginModule> => {
      callCount += 1
      if (callCount === 1) {
        await firstImportGate
      }
      return {
        activate(ctx) {
          activeCount += 1
          maxActiveCount = Math.max(maxActiveCount, activeCount)
          ctx.registerViewer({
            id: 'three-viewer',
            title: '3D Viewer',
            extensions: ['.glb'],
            component: () => null,
          })
          ctx.subscriptions.push({
            dispose: () => {
              activeCount -= 1
            },
          })
        },
      }
    })

    const loader = createEsmPluginLoader({ importer })
    const pluginOn = makeEsmPlugin({ enabled: true })
    const pluginOff = makeEsmPlugin({ enabled: false })

    // Rapid toggle storm: ON -> OFF -> ON -> OFF while first import is still pending
    const p1 = loader.syncLoadedPlugins([pluginOn])
    const p2 = loader.syncLoadedPlugins([pluginOff])
    const p3 = loader.syncLoadedPlugins([pluginOn])
    const p4 = loader.syncLoadedPlugins([pluginOff])

    releaseFirstImport()
    await Promise.all([p1, p2, p3, p4])

    expect(maxActiveCount).toBeLessThanOrEqual(1)
    expect(activeCount).toBe(0)
    expect(loader.isLoaded('three-viewer')).toBe(false)
    expect(artifactViewerRegistry.getViewerById('three-viewer')).toBeNull()
    expect(document.querySelectorAll('link[data-astrorder-ext-css="three-viewer"]')).toHaveLength(0)

    // Final toggle ON converges cleanly with exactly one CSS link and one active instance
    await loader.syncLoadedPlugins([pluginOn])
    expect(activeCount).toBe(1)
    expect(loader.isLoaded('three-viewer')).toBe(true)
    expect(document.querySelectorAll('link[data-astrorder-ext-css="three-viewer"]')).toHaveLength(1)
  })

  it('rejects path traversal in asset URLs and refreshes extensions on SSE extension.changed', async () => {
    const plugin = makeEsmPlugin()
    expect(buildEsmAssetUrl(plugin, 'ui/entry.js')).toBe(
      '/api/v1/ext/three-viewer/_esm/ui/entry.js?v=digest-v1-aaaaaa',
    )
    expect(() => buildEsmAssetUrl(plugin, '../secret.js')).toThrow()
    expect(() => buildEsmAssetUrl(plugin, 'https://evil.example/a.js')).toThrow()

    let streamOptions: { onEvent: (event: EventEnvelope) => void } | undefined
    streamMocks.connectEventStream.mockImplementation((opts) => {
      streamOptions = opts
      return vi.fn()
    })
    const refreshSpy = vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({
      items: [makeEsmPlugin({ content_digest: 'sse-v2' })],
    })

    const qc = new QueryClient()
    const { unmount } = renderHook(() => useEventStream(true, qc))

    act(() => {
      streamOptions?.onEvent({
        id: 'evt-ext-1',
        cursor: 10,
        type: 'extension.changed',
        agent_id: '',
        session_id: '',
        data: { action: 'reloaded', plugin_id: 'three-viewer', content_digest: 'sse-v2' },
      })
    })

    await vi.waitFor(() => {
      expect(refreshSpy).toHaveBeenCalledTimes(1)
      expect(useExtensionStore.getState().getById('three-viewer')?.content_digest).toBe('sse-v2')
    })

    unmount()
  })

  it('awaits stylesheet load before importing and aborts cleanly on CSS load error', async () => {
    const importer = vi.fn(async (): Promise<RawEsmPluginModule> => ({
      activate() {},
    }))
    const loader = createEsmPluginLoader({ importer, awaitCssInJsdom: true })
    const plugin = makeEsmPlugin({ content_digest: 'css-gate' })

    const loadPromise = loader.syncLoadedPlugins([plugin])
    await vi.waitFor(() => {
      expect(document.querySelector('link[data-astrorder-ext-css="three-viewer"]')).not.toBeNull()
    })
    // Must not import JS before CSS fires load event
    expect(importer).not.toHaveBeenCalled()

    const link = document.querySelector<HTMLLinkElement>('link[data-astrorder-ext-css="three-viewer"]')!
    link.dispatchEvent(new Event('error'))
    await loadPromise

    expect(importer).not.toHaveBeenCalled()
    expect(document.querySelector('link[data-astrorder-ext-css="three-viewer"]')).toBeNull()
    expect(loader.getState('three-viewer')?.status).toBe('error')
    expect(loader.isLoaded('three-viewer')).toBe(false)

    // Retry with new digest and fire CSS load -> proceeds to import & activate
    const okPlugin = makeEsmPlugin({ content_digest: 'css-ok' })
    const okPromise = loader.syncLoadedPlugins([okPlugin])
    await vi.waitFor(() => {
      expect(document.querySelector('link[data-astrorder-ext-css="three-viewer"]')).not.toBeNull()
    })
    document
      .querySelector<HTMLLinkElement>('link[data-astrorder-ext-css="three-viewer"]')!
      .dispatchEvent(new Event('load'))
    await okPromise

    expect(importer).toHaveBeenCalledTimes(1)
    expect(loader.isLoaded('three-viewer')).toBe(true)
  })

  it('reacts automatically to useExtensionStore and usePluginSettingsStore updates when started', async () => {
    const activateSpy = vi.fn()
    const deactivateSpy = vi.fn()
    const importer = vi.fn(async (): Promise<RawEsmPluginModule> => ({
      activate: activateSpy,
      deactivate: deactivateSpy,
    }))
    const loader = createEsmPluginLoader({ importer })
    const stop = loader.start()

    try {
      const plugin = makeEsmPlugin({ enabled: true, content_digest: 'live-1' })
      useExtensionStore.setState({ items: [plugin], hydrated: true })
      await vi.waitFor(() => expect(loader.isLoaded('three-viewer')).toBe(true))
      expect(activateSpy).toHaveBeenCalledTimes(1)

      // Disabling via usePluginSettingsStore immediately unloads the ESM module
      usePluginSettingsStore.setState({ enabledPlugins: { 'three-viewer': false } })
      await vi.waitFor(() => expect(loader.isLoaded('three-viewer')).toBe(false))
      expect(deactivateSpy).toHaveBeenCalledTimes(1)
    } finally {
      stop()
      await loader.reset()
    }
  })

  it('provides controlled ctx.api capabilities and cleans up config subscriptions on dispose', async () => {
    localStorage.setItem('astrorder:token', 'sdk-test-token')
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const notifySpy = vi.spyOn(notifications, 'show').mockReturnValue('n-1')
    const navEvents: string[] = []
    const onNavigate = (e: Event) => {
      const to = (e as CustomEvent<{ to?: string }>).detail?.to
      if (to) navEvents.push(to)
    }
    window.addEventListener('astrorder:navigate', onNavigate)

    const ctx = createScopedPluginContext(makeEsmPlugin())

    // 1. authenticated fetch & scoped fetchExt
    await ctx.api.fetch('/api/v1/health')
    expect(new Headers(fetchSpy.mock.calls[0][1]?.headers).get('Authorization')).toBe(
      'Bearer sdk-test-token',
    )
    await ctx.api.fetchExt('/ping')
    expect(fetchSpy.mock.calls[1][0]).toBe('/api/v1/ext/three-viewer/ping')
    expect(() => ctx.api.fetchExt('../other/secret')).toThrow()

    // 2. notify & safe navigate
    ctx.api.notify({ title: '提示', message: '已就绪', color: 'teal' })
    expect(notifySpy).toHaveBeenCalledWith(expect.objectContaining({ message: '已就绪' }))
    ctx.api.navigate('/ext-page/three-viewer/studio')
    ctx.api.navigate('//evil.example/phish')
    expect(navEvents).toEqual(['/ext-page/three-viewer/studio'])

    // 3. openArtifact
    const artifact: ArtifactRef = {
      id: 'artifact:cube.glb',
      name: 'cube.glb',
      kind: 'workspace_file',
      mediaType: 'model/gltf-binary',
      readUrl: '/api/v1/files/raw?path=cube.glb',
      writable: false,
      sessionId: 'sess-1',
      agentId: 'agent-1',
    }
    ctx.api.openArtifact(artifact, 'monaco-viewer')
    expect(useSidecarStore.getState().tabs.some((t) => t.id === 'artifact:cube.glb')).toBe(true)

    // 4. getConfig & onConfigChange (auto-cleaned on dispose)
    const configChanges: Array<Record<string, unknown>> = []
    ctx.api.onConfigChange((cfg) => configChanges.push(cfg))
    usePluginSettingsStore.setState({
      pluginConfigs: { 'three-viewer': { wireframe: true } },
    })
    expect(ctx.api.getConfig<{ wireframe?: boolean }>()).toEqual({ wireframe: true })
    expect(configChanges).toEqual([{ wireframe: true }])

    disposePluginSubscriptions(ctx.subscriptions)
    usePluginSettingsStore.setState({
      pluginConfigs: { 'three-viewer': { wireframe: false } },
    })
    expect(configChanges).toHaveLength(1)

    // 5. reactive hooks: useAppProfile, useCurrentSession, useThemeMode
    setAppProfile('office', { syncPluginStore: false })
    document.documentElement.setAttribute('data-mantine-color-scheme', 'dark')
    const sampleSession: Session = {
      id: 'sess-9',
      agent_id: 'codex-1',
      title: 'SDK Test Session',
      workspace: '/repo/demo',
      status: 'idle',
      updated_at: '2026-11-27T00:00:00Z',
    }
    useAstrorderStore.setState({ sessions: { 'codex-1::sess-9': sampleSession } })
    useSidecarStore.setState({ activeSessionKey: 'codex-1::sess-9' })

    const { result } = renderHook(() => ({
      profile: ctx.api.useAppProfile(),
      session: ctx.api.useCurrentSession(),
      theme: ctx.api.useThemeMode(),
      cfgTheme: ctx.api.usePluginConfig<string>('theme', 'min'),
    }))
    expect(result.current.profile).toBe('office')
    expect(result.current.session?.id).toBe('sess-9')
    expect(result.current.theme).toBe('dark')
    expect(result.current.cfgTheme).toBe('min')
    expect(ctx.api.getActiveSession()?.id).toBe('sess-9')
    expect(ctx.api.getSessions().map((s) => s.id)).toEqual(['sess-9'])

    // Reactive usePluginConfig updates live when pluginSettingsStore changes
    act(() => {
      usePluginSettingsStore.setState({
        pluginConfigs: { 'three-viewer': { theme: 'dark' } },
      })
    })
    expect(result.current.cfgTheme).toBe('dark')

    // 6. openPluginPanel (defaults pluginId = ctx.pluginId and falls back to active session)
    ctx.api.openPluginPanel('inspector', { title: '3D Inspector', params: { mesh: 'body' } })
    expect(useSidecarStore.getState().tabs.some((t) => t.id === 'ext:three-viewer:inspector:sess-9')).toBe(true)

    // 7. openDatabaseWorkbench (defaults viewerId = ctx.pluginId)
    ctx.api.openDatabaseWorkbench('application/x-astrorder-dbx', undefined, 'sess-9', 'codex-1')
    expect(
      useSidecarStore
        .getState()
        .tabs.some((t) => t.id === 'database:workbench' && t.viewerId === 'three-viewer'),
    ).toBe(true)

    // 8. quoteToComposer (supports both Session and { sessionId, agentId })
    const quotes: Array<{ agentId: string; sessionId: string; text: string }> = []
    const onQuote = (e: Event) => {
      quotes.push((e as CustomEvent<{ agentId: string; sessionId: string; text: string }>).detail)
    }
    window.addEventListener('astrorder:quote-to-composer', onQuote)
    ctx.api.quoteToComposer(sampleSession, 'pipeline ok')
    ctx.api.quoteToComposer({ sessionId: 'sess-alt', agentId: 'agent-alt' }, 'log snippet')
    expect(quotes).toEqual([
      { agentId: 'codex-1', sessionId: 'sess-9', text: 'pipeline ok' },
      { agentId: 'agent-alt', sessionId: 'sess-alt', text: 'log snippet' },
    ])
    window.removeEventListener('astrorder:quote-to-composer', onQuote)

    // 9. openExternal (desktop bridge -> browser fallback -> blocks javascript:)
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
    ctx.api.openExternal('https://gitlab.example/p/1')
    expect(openSpy).toHaveBeenCalledWith('https://gitlab.example/p/1', '_blank', 'noopener,noreferrer')
    openSpy.mockClear()
    ctx.api.openExternal('javascript:alert(1)')
    expect(openSpy).not.toHaveBeenCalled()

    const desktopOpenExternal = vi.fn().mockResolvedValue(undefined)
    ;(window as unknown as { astrorderDesktop?: { openExternal: typeof desktopOpenExternal } }).astrorderDesktop = {
      openExternal: desktopOpenExternal,
    }
    ctx.api.openExternal('https://gitlab.example/p/2')
    expect(desktopOpenExternal).toHaveBeenCalledWith('https://gitlab.example/p/2')
    expect(openSpy).not.toHaveBeenCalled()

    // 10. openSettings
    const settingsEvents: Array<{ tab?: string; pluginId?: string }> = []
    const onSettings = (e: Event) => {
      settingsEvents.push((e as CustomEvent<{ tab?: string; pluginId?: string }>).detail)
    }
    window.addEventListener('astrorder:open-settings', onSettings)
    ctx.api.openSettings()
    expect(settingsEvents).toEqual([{ tab: 'plugins', pluginId: 'three-viewer' }])
    window.removeEventListener('astrorder:open-settings', onSettings)

    window.removeEventListener('astrorder:navigate', onNavigate)
  })

  it('skips hide_in_office_mode ESM plugins in office profile (0 loads), hot-loads on switch to developer, and hot-unloads back to office', async () => {
    const drawioPlugin = makeEsmPlugin({
      id: 'drawio-viewer',
      name: '流程图与架构图编辑器',
      category: 'diagram',
      esm_base_url: '/api/v1/ext/drawio-viewer/_esm',
      content_digest: 'drawio-v1',
      contributes: {
        viewers: [
          {
            id: 'drawio-viewer',
            title: '矢量流程图',
            extensions: ['.drawio'],
            hide_in_office_mode: false,
          },
        ],
      },
    })
    const dbxPlugin = makeEsmPlugin({
      id: 'dbx-viewer',
      name: 'DBX 数据库工作台',
      category: 'development',
      esm_base_url: '/api/v1/ext/dbx-viewer/_esm',
      content_digest: 'dbx-v1',
      contributes: {
        viewers: [
          {
            id: 'dbx-viewer',
            title: '数据库管理工具',
            mime_types: ['application/x-astrorder-dbx'],
            hide_in_office_mode: true,
          },
        ],
      },
    })
    const gitlabHybridPlugin = makeEsmPlugin({
      id: 'gitlab-ci-viewer',
      name: 'GitLab CI/CD 流水线',
      category: 'development',
      source: 'builtin',
      backend_type: 'builtin',
      runtime_state: 'builtin',
      esm_base_url: '/api/v1/ext/gitlab-ci-viewer/_esm',
      content_digest: 'gitlab-v1',
      contributes: {
        viewers: [
          {
            id: 'gitlab-ci-viewer',
            title: 'GitLab CI 流水线',
            mime_types: ['application/x-gitlab-ci'],
            hide_in_office_mode: true,
          },
        ],
      },
    })

    expect(isEsmPluginHiddenInOfficeMode(drawioPlugin)).toBe(false)
    expect(isEsmPluginHiddenInOfficeMode(dbxPlugin)).toBe(true)
    expect(isEsmPluginHiddenInOfficeMode(gitlabHybridPlugin)).toBe(true)

    expect(shouldLoadEsmPlugin(drawioPlugin, undefined, 'office')).toBe(true)
    expect(shouldLoadEsmPlugin(dbxPlugin, undefined, 'office')).toBe(false)
    expect(shouldLoadEsmPlugin(gitlabHybridPlugin, undefined, 'office')).toBe(false)
    expect(shouldLoadEsmPlugin(dbxPlugin, undefined, 'developer')).toBe(true)
    expect(shouldLoadEsmPlugin(gitlabHybridPlugin, undefined, 'developer')).toBe(true)

    // Start in office mode: dev-only plugins must incur 0 imports and 0 CSS loads
    usePluginSettingsStore.setState({
      pluginConfigs: { [APP_PROFILE_PLUGIN_KEY]: { mode: 'office' } },
    })

    const deactivateCalls: string[] = []
    const importer = vi.fn(async (url: string): Promise<RawEsmPluginModule> => {
      if (url.includes('/drawio-viewer/')) {
        return {
          activate(ctx) {
            ctx.registerViewer({
              id: 'drawio-viewer',
              title: '矢量流程图',
              extensions: ['.drawio'],
              component: () => null,
            })
          },
          deactivate() {
            deactivateCalls.push('drawio-viewer')
          },
        }
      }
      if (url.includes('/dbx-viewer/')) {
        return {
          activate(ctx) {
            ctx.registerViewer({
              id: 'dbx-viewer',
              title: '数据库管理工具',
              mimeTypes: ['application/x-astrorder-dbx'],
              component: () => null,
            })
            ctx.registerDatabaseTool({
              id: 'dbx-viewer',
              implementationName: 'DBX',
              mediaType: 'application/x-astrorder-dbx',
              openWorkbench: vi.fn(),
              openSqlFile: vi.fn(async () => {}),
              openSqlSnippet: vi.fn(),
            })
          },
          deactivate() {
            deactivateCalls.push('dbx-viewer')
          },
        }
      }
      return {
        activate(ctx) {
          ctx.registerViewer({
            id: 'gitlab-ci-viewer',
            title: 'GitLab CI 流水线',
            mimeTypes: ['application/x-gitlab-ci'],
            component: () => null,
          })
          ctx.registerSlot({
            settingsTitle: 'GitLab CI 配置',
            settingsButtonLabel: '配置实例',
            settingsComponent: () => null,
          })
        },
        deactivate() {
          deactivateCalls.push('gitlab-ci-viewer')
        },
      }
    })

    const { result: dbToolProbe } = renderHook(() => useHasDatabaseTool())
    expect(dbToolProbe.current).toBe(false)

    const loader = createEsmPluginLoader({ importer })
    const stop = loader.start()
    try {
      useExtensionStore.setState({
        items: [drawioPlugin, dbxPlugin, gitlabHybridPlugin],
        hydrated: true,
      })

      await vi.waitFor(() => expect(loader.isLoaded('drawio-viewer')).toBe(true))
      // Zero requests for office-hidden dev plugins
      expect(importer).toHaveBeenCalledTimes(1)
      expect(importer).toHaveBeenCalledWith('/api/v1/ext/drawio-viewer/_esm/ui/entry.js?v=drawio-v1')
      expect(loader.isLoaded('dbx-viewer')).toBe(false)
      expect(loader.isLoaded('gitlab-ci-viewer')).toBe(false)
      expect(document.querySelector('link[data-astrorder-ext-css="dbx-viewer"]')).toBeNull()
      expect(document.querySelector('link[data-astrorder-ext-css="gitlab-ci-viewer"]')).toBeNull()
      expect(dbToolProbe.current).toBe(false)

      // Switch to developer profile -> dbx-viewer and hybrid builtin gitlab-ci-viewer hot-load automatically
      act(() => {
        usePluginSettingsStore.setState({
          pluginConfigs: { [APP_PROFILE_PLUGIN_KEY]: { mode: 'developer' } },
        })
      })

      await vi.waitFor(() => {
        expect(loader.isLoaded('dbx-viewer')).toBe(true)
        expect(loader.isLoaded('gitlab-ci-viewer')).toBe(true)
      })
      expect(dbToolProbe.current).toBe(true)
      expect(databaseToolRegistry.getActiveProvider()?.id).toBe('dbx-viewer')
      expect(extensionRegistry.getBuiltin('gitlab-ci-viewer')?.settingsButtonLabel).toBe('配置实例')
      expect(artifactViewerRegistry.getViewerById('gitlab-ci-viewer')).not.toBeNull()

      // Switch back to office profile -> dev-only ESM plugins hot-unload automatically while drawio stays loaded
      act(() => {
        usePluginSettingsStore.setState({
          pluginConfigs: { [APP_PROFILE_PLUGIN_KEY]: { mode: 'office' } },
        })
      })

      await vi.waitFor(() => {
        expect(loader.isLoaded('dbx-viewer')).toBe(false)
        expect(loader.isLoaded('gitlab-ci-viewer')).toBe(false)
      })
      expect(loader.isLoaded('drawio-viewer')).toBe(true)
      expect(dbToolProbe.current).toBe(false)
      expect(databaseToolRegistry.getActiveProvider()).toBeNull()
      expect(extensionRegistry.getBuiltin('gitlab-ci-viewer')).toBeUndefined()
      expect(artifactViewerRegistry.getViewerById('dbx-viewer')).toBeNull()
      expect(artifactViewerRegistry.getViewerById('gitlab-ci-viewer')).toBeNull()
      expect(deactivateCalls.sort()).toEqual(['dbx-viewer', 'gitlab-ci-viewer'])
    } finally {
      stop()
      await loader.reset()
    }
  })
})
