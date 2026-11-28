import { existsSync, readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { extensionsApi, type PluginView } from '../../api/extensions'
import type { ArtifactRef } from '../../domain/artifact'
import type { Session } from '../../domain/types'
import { MarkdownContent } from '../../components/MarkdownContent'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { artifactViewerRegistry } from '../sidecar/registry'
import type { ViewerContext } from '../sidecar/types'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { SidecarHost } from '../sidecar/SidecarHost'
import { createEsmPluginLoader } from './esmPluginLoader'
import { extensionRegistry } from './extensionRegistry'
import { useExtensionStore } from './extensionStore'
import { pluginEventBus } from './pluginEventBus'
import { createScopedPluginContext, disposePluginSubscriptions, useService } from './pluginSdk'
import { PLUGIN_RUNTIME_V1 } from './runtimeBridge'
import { PluginMarketplaceView } from './PluginMarketplaceView'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(__dirname, '..', '..', '..', '..')

function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 'sess-1',
    agent_id: 'codex',
    title: 'Test Session',
    workspace: '/tmp/workspace',
    status: 'idle',
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

function makeEsmPluginView(overrides: Partial<PluginView> & { id: string }): PluginView {
  return {
    name: overrides.id,
    version: '1.0.0',
    category: 'workspace',
    source: 'official',
    enabled: true,
    permissions_granted: true,
    runtime_state: 'ready',
    backend_type: 'none',
    content_digest: 'digest0000000001',
    esm_base_url: `/api/v1/ext/${overrides.id}/_esm`,
    frontend: {
      mode: 'esm',
      entry: 'ui/entry.js',
      sdk_version: '^1.0',
    },
    ...overrides,
  }
}

describe('Plugin architecture capabilities', () => {
  beforeEach(() => {
    pluginEventBus.clear()
    extensionRegistry.clear()
    usePluginSettingsStore.setState({ enabledPlugins: {}, pluginConfigs: {} })
  })

  afterEach(() => {
    cleanup()
    pluginEventBus.clear()
    extensionRegistry.clear()
    artifactViewerRegistry.syncDeclarativeIframeViewers([])
    vi.restoreAllMocks()
  })

  it('maintains 0-diff export parity between runtimeBridge.ts and sdk_esm.go', () => {
    const goSrc = readFileSync(
      path.join(repoRoot, 'app-server-go', 'internal', 'pluginhost', 'sdk_esm.go'),
      'utf8',
    )
    const blockRe =
      /"([^"]+\.js)":\s*\{\s*runtimeProp:\s*"([^"]+)",\s*label:\s*"[^"]+",\s*exports:\s*\[\]string\{([\s\S]*?)\},/g
    const parsedSpecs: Array<{ file: string; runtimeProp: string; exports: string[] }> = []
    let m: RegExpExecArray | null
    while ((m = blockRe.exec(goSrc)) !== null) {
      const syms = [...m[3].matchAll(/"([^"]+)"/g)].map((x) => x[1])
      parsedSpecs.push({ file: m[1], runtimeProp: m[2], exports: syms })
    }

    expect(parsedSpecs.length).toBe(10)
    const runtime = PLUGIN_RUNTIME_V1 as Record<string, unknown>

    for (const spec of parsedSpecs) {
      const bridgeObj = runtime[spec.runtimeProp] as Record<string, unknown> | undefined
      expect(bridgeObj, `missing runtime prop ${spec.runtimeProp} for ${spec.file}`).toBeDefined()
      const bridgeKeys = Object.keys(bridgeObj!).sort()
      const goKeys = [...spec.exports].sort()
      expect(bridgeKeys, `export mismatch for ${spec.file} (${spec.runtimeProp})`).toEqual(goKeys)
    }
  })

  it('ensures every built official plugin entry and lazy chunk only imports symbols exported by sdk_esm.go', () => {
    const goSrc = readFileSync(
      path.join(repoRoot, 'app-server-go', 'internal', 'pluginhost', 'sdk_esm.go'),
      'utf8',
    )
    const blockRe =
      /"([^"]+\.js)":\s*\{\s*runtimeProp:\s*"[^"]+",\s*label:\s*"([^"]+)",\s*exports:\s*\[\]string\{([\s\S]*?)\},/g
    const exportsByLabel = new Map<string, Set<string>>()
    let m: RegExpExecArray | null
    while ((m = blockRe.exec(goSrc)) !== null) {
      const syms = [...m[3].matchAll(/"([^"]+)"/g)].map((x) => x[1])
      exportsByLabel.set(m[2], new Set(syms))
    }

    const officialDist = path.join(repoRoot, 'plugins', 'official-dist')
    if (!existsSync(officialDist)) return

    const jsFiles: string[] = []
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name)
        if (entry.isDirectory()) walk(full)
        else if (entry.isFile() && entry.name.endsWith('.js')) jsFiles.push(full)
      }
    }
    walk(officialDist)
    expect(jsFiles.length).toBeGreaterThan(0)

    const missing: string[] = []
    const namedImportRe = /import\s*\{([^}]+)\}\s*from\s*["']([^"']+)["']/g
    for (const file of jsFiles) {
      const src = readFileSync(file, 'utf8')
      let im: RegExpExecArray | null
      while ((im = namedImportRe.exec(src)) !== null) {
        const mod = im[2]
        if (mod.startsWith('.')) continue
        const allowed = exportsByLabel.get(mod)
        if (!allowed) {
          missing.push(`${path.relative(officialDist, file)} imports unknown host module "${mod}"`)
          continue
        }
        const names = im[1]
          .split(',')
          .map((part) => part.trim().split(/\s+as\s+/)[0].trim())
          .filter(Boolean)
        for (const sym of names) {
          if (!allowed.has(sym)) {
            missing.push(`${path.relative(officialDist, file)} missing "${sym}" from "${mod}"`)
          }
        }
      }
    }
    expect(missing).toEqual([])
  })

  it('routes host events, session events, cross-plugin pub/sub, and reactive services cleanly', () => {
    const ctxA = createScopedPluginContext({ id: 'plugin-a', version: '1.0.0' })
    const ctxB = createScopedPluginContext({ id: 'plugin-b', version: '1.0.0' })

    const hostHits: string[] = []
    const sessionHits: string[] = []
    const busHits: Array<{ from: string; topic: string; value: number }> = []

    ctxA.api.onHostEvent('message.*', (ev) => hostHits.push(ev.type))
    ctxA.api.onHostEvent('*', () => {
      throw new Error('faulty listener should be isolated')
    })
    ctxA.api.onSessionEvent('sess-42', (ev) => sessionHits.push(ev.type))
    ctxB.api.onPluginEvent<{ value: number }>('metrics.*', (payload, meta) => {
      busHits.push({ from: meta.fromPluginId, topic: meta.topic, value: payload.value })
    })

    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    pluginEventBus.emitHostEvent({ type: 'message.upsert', session_id: 'sess-42' })
    pluginEventBus.emitHostEvent({ type: 'message.delta', session_id: 'sess-99' })
    pluginEventBus.emitHostEvent({ type: 'session.upsert', session_id: 'sess-42' })
    expect(hostHits).toEqual(['message.upsert', 'message.delta'])
    expect(sessionHits).toEqual(['message.upsert', 'session.upsert'])
    expect(errSpy).toHaveBeenCalled()

    ctxA.api.emitPluginEvent('metrics.latency', { value: 42 })
    expect(busHits).toEqual([{ from: 'plugin-a', topic: 'metrics.latency', value: 42 }])

    // Service registration & reactive hook
    function ServiceConsumer() {
      const svc = useService<{ label: string }>('plugin-a.svc')
      return <div data-testid="svc-val">{svc ? svc.label : 'missing'}</div>
    }
    render(<ServiceConsumer />)
    expect(screen.getByTestId('svc-val').textContent).toBe('missing')

    ctxA.registerService('plugin-a.svc', { label: 'ready-v1' })
    return waitFor(() => {
      expect(screen.getByTestId('svc-val').textContent).toBe('ready-v1')
    }).then(() => {
      disposePluginSubscriptions(ctxA.subscriptions)
      disposePluginSubscriptions(ctxB.subscriptions)
      expect(ctxB.api.getService('plugin-a.svc')).toBeUndefined()
    })
  })

  it('runs message interceptors sequentially with transform, cancel, and error isolation', async () => {
    const ctx = createScopedPluginContext({ id: 'guard-plugin', version: '1.0.0' })
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    ctx.registerMessageInterceptor({
      id: 'first-rewrite',
      order: 10,
      beforeSend: ({ text }) => ({ text: text.replace('#tag', '[TAGGED]'), metadata: { tagged: true } }),
    })
    ctx.registerMessageInterceptor({
      id: 'throws-ignored',
      order: 20,
      beforeSend: () => {
        throw new Error('boom')
      },
    })
    ctx.registerMessageInterceptor({
      id: 'secret-block',
      order: 30,
      beforeSend: ({ text }) => {
        if (text.includes('SECRET_KEY')) {
          return { cancel: true, reason: '包含敏感密钥，禁止发送' }
        }
        return { text: `${text} --ok` }
      },
    })

    const session = makeSession()
    const transformed = await extensionRegistry.runMessageInterceptors({
      session,
      text: 'hello #tag',
      attachments: [],
      metadata: {},
    })
    expect(transformed.cancelled).toBe(false)
    expect(transformed.text).toBe('hello [TAGGED] --ok')
    expect(transformed.metadata.tagged).toBe(true)
    expect(errSpy).toHaveBeenCalled()

    const blocked = await extensionRegistry.runMessageInterceptors({
      session,
      text: 'leaked SECRET_KEY',
      attachments: [],
      metadata: {},
    })
    expect(blocked.cancelled).toBe(true)
    expect(blocked.reason).toBe('包含敏感密钥，禁止发送')

    disposePluginSubscriptions(ctx.subscriptions)
  })

  it('renders custom Markdown code-block renderers with source toggle and error boundary', () => {
    const ctx = createScopedPluginContext({ id: 'kpi-plugin', version: '1.0.0' })
    ctx.registerMarkdownBlockRenderer({
      id: 'kpi-renderer',
      title: 'KPI 卡片',
      languages: ['kpi'],
      component: ({ code }) => <div data-testid="custom-kpi">KPI:{code}</div>,
    })

    const qc = new QueryClient()
    render(
      <QueryClientProvider client={qc}>
        <MantineProvider env="test">
          <MarkdownContent value={'```kpi\nrevenue=100\n```'} session={makeSession()} />
        </MantineProvider>
      </QueryClientProvider>,
    )

    expect(screen.getByTestId('custom-kpi').textContent).toBe('KPI:revenue=100')
    fireEvent.click(screen.getByText('查看源码'))
    expect(screen.queryByTestId('custom-kpi')).toBeNull()
    expect(screen.getByText('切回KPI 卡片视图')).toBeTruthy()

    disposePluginSubscriptions(ctx.subscriptions)
  })

  it('loads ESM plugins in depends_on topological order and flags missing/cyclic dependencies', async () => {
    const loadOrder: string[] = []
    const loader = createEsmPluginLoader({
      syncExtensionStore: false,
      importer: async (url) => {
        if (url.includes('/dep-a/')) {
          return {
            activate(ctx) {
              loadOrder.push('dep-a')
              ctx.registerService('dep-a.api', { ok: true })
            },
          }
        }
        if (url.includes('/app-b/')) {
          return {
            activate(ctx) {
              const dep = ctx.api.getService<{ ok: boolean }>('dep-a.api')
              if (!dep?.ok) throw new Error('dep-a service missing during app-b activate')
              loadOrder.push('app-b')
            },
          }
        }
        return { activate() {} }
      },
    })

    // Provide app-b BEFORE dep-a in the list to prove topological sorting reorders them.
    await loader.syncLoadedPlugins([
      makeEsmPluginView({ id: 'app-b', depends_on: ['dep-a'] }),
      makeEsmPluginView({ id: 'dep-a' }),
      makeEsmPluginView({ id: 'orphan-c', depends_on: ['nonexistent-plugin'] }),
      makeEsmPluginView({ id: 'cycle-1', depends_on: ['cycle-2'] }),
      makeEsmPluginView({ id: 'cycle-2', depends_on: ['cycle-1'] }),
    ])

    expect(loadOrder).toEqual(['dep-a', 'app-b'])
    expect(loader.isLoaded('dep-a')).toBe(true)
    expect(loader.isLoaded('app-b')).toBe(true)
    expect(loader.getState('orphan-c')?.status).toBe('error')
    expect(loader.getState('orphan-c')?.error).toContain('nonexistent-plugin')
    expect(loader.getState('cycle-1')?.status).toBe('error')
    expect(loader.getState('cycle-1')?.error).toContain('循环依赖')
    expect(loader.getState('cycle-2')?.status).toBe('error')

    await loader.reset()
  })

  it('supports registerFileHandler, declarative iframe viewers, findAllViewers, and SidecarHost viewer switching', () => {
    const ctx = createScopedPluginContext({ id: 'custom-ext-plugin', version: '1.0.0' })
    ctx.registerFileHandler({
      id: 'custom-toml-viewer',
      title: 'TOML 可视化编辑器',
      extensions: ['toml', '.cfg.toml'],
      defaultForExtensions: true,
      canEdit: true,
      component: ({ artifact, toolbarAction }: ViewerContext) => (
        <div data-testid="custom-toml-view">
          <span>TOML:{artifact.name}</span>
          {toolbarAction}
        </div>
      ),
    })

    const artifact: ArtifactRef = {
      id: 'file:config.toml',
      sessionId: 'sess-1',
      agentId: 'codex',
      name: 'config.toml',
      path: '/tmp/workspace/config.toml',
      kind: 'workspace_file',
      readUrl: '/api/v1/files/raw?path=%2Ftmp%2Fworkspace%2Fconfig.toml',
      mediaType: 'text/plain',
      writable: true,
    }

    const allViewers = artifactViewerRegistry.findAllViewers(artifact)
    expect(allViewers.length).toBeGreaterThanOrEqual(2)
    expect(allViewers[0].id).toBe('custom-toml-viewer')
    expect(allViewers.some((v) => v.id === 'monaco-viewer')).toBe(true)

    // Declarative iframe viewer registration & cleanup
    artifactViewerRegistry.syncDeclarativeIframeViewers([
      {
        id: 'logfmt-iframe',
        name: 'Logfmt Iframe',
        version: '1.0.0',
        category: 'development',
        source: 'external',
        enabled: true,
        permissions_granted: true,
        runtime_state: 'ready',
        backend_type: 'none',
        ui_base_url: '/api/v1/ext/logfmt-iframe/_ui',
        frontend: { mode: 'iframe' },
        contributes: {
          viewers: [
            {
              id: 'logfmt-iframe-viewer',
              title: 'Logfmt 日志视图',
              extensions: ['.logfmt'],
              ui_entry: 'ui/viewer.html',
              read_only: true,
              default_for_extensions: true,
            },
          ],
        },
      },
    ])
    const logArtifact: ArtifactRef = {
      ...artifact,
      id: 'file:app.logfmt',
      name: 'app.logfmt',
      path: '/tmp/workspace/app.logfmt',
    }
    expect(artifactViewerRegistry.findViewer(logArtifact)?.id).toBe('logfmt-iframe-viewer')

    // SidecarHost honors explicit tab.viewerId and switches viewers via setTabViewer
    useSidecarStore.setState({
      isOpen: true,
      activeSessionKey: 'codex:sess-1',
      activeTabId: artifact.id,
      tabs: [
        {
          id: artifact.id,
          type: 'artifact',
          title: artifact.name,
          artifact,
          viewerId: 'custom-toml-viewer',
          closable: true,
        },
      ],
      mountedTabs: {},
      dirtyTabs: {},
    })

    render(
      <MantineProvider env="test">
        <SidecarHost
          session={makeSession()}
          commands={[]}
          messages={[]}
          approvals={[]}
          onApproval={() => {}}
          onCloseSidecar={() => {}}
        />
      </MantineProvider>,
    )

    expect(screen.getByTestId('custom-toml-view')).toBeTruthy()
    expect(screen.getByLabelText('打开方式')).toBeTruthy()

    useSidecarStore.getState().setTabViewer(artifact.id, 'monaco-viewer')
    expect(useSidecarStore.getState().tabs[0].viewerId).toBe('monaco-viewer')

    disposePluginSubscriptions(ctx.subscriptions)
  })

  it('renders PluginMarketplaceView entries and triggers remote installation', async () => {
    const installedPlugin = makeEsmPluginView({ id: 'remote-demo', name: 'Remote Demo' })
    vi.spyOn(extensionsApi, 'getMarketplace').mockResolvedValue({
      items: [
        {
          id: 'remote-demo',
          name: 'Remote Demo Plugin',
          version: '1.2.0',
          category: 'development',
          description: '支持 .demo 文件预览',
          author: 'Community',
          download_url: 'https://plugins.example.com/remote-demo.zip',
          sha256: 'abc123',
          extensions: ['.demo'],
          permissions: ['ui:slots'],
          frontend_mode: 'esm',
          registry_id: 'community',
          registry_name: '社区源',
          official: false,
          installed: false,
        },
      ],
      registries: [
        { id: 'official', name: '官方插件目录', url: 'builtin://official', enabled: true, official: true },
        { id: 'community', name: '社区源', url: 'https://plugins.example.com/index.json', enabled: true, official: false },
      ],
      errors: [],
    })
    const installSpy = vi
      .spyOn(extensionsApi, 'installRemotePlugin')
      .mockResolvedValue({ item: installedPlugin })
    vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({ items: [installedPlugin] })
    const onInstalled = vi.fn()

    render(
      <MantineProvider env="test">
        <PluginMarketplaceView onInstalled={onInstalled} />
      </MantineProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText('Remote Demo Plugin')).toBeTruthy()
    })
    expect(screen.getByText('.demo')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '安装' }))
    await waitFor(() => {
      expect(installSpy).toHaveBeenCalledWith({
        download_url: 'https://plugins.example.com/remote-demo.zip',
        sha256: 'abc123',
      })
      expect(onInstalled).toHaveBeenCalledWith(installedPlugin, true)
    })
  })

  it('persists per-extension default viewer preference and renders host toolbarAction inside iframe file viewers', () => {
    const ctx = createScopedPluginContext({ id: 'pref-ext', version: '1.0.0' })
    ctx.registerFileHandler({
      id: 'pref-toml-viewer',
      title: 'TOML 卡片视图',
      extensions: ['.toml'],
      defaultForExtensions: true,
      component: () => <div>TOML</div>,
    })

    const tomlArtifact: ArtifactRef = {
      id: 'file:Cargo.toml',
      sessionId: 'sess-1',
      agentId: 'codex',
      name: 'Cargo.toml',
      path: '/tmp/workspace/Cargo.toml',
      kind: 'workspace_file',
      readUrl: '/api/v1/files/raw?path=%2Ftmp%2Fworkspace%2FCargo.toml',
      mediaType: 'text/plain',
      writable: true,
    }

    // Before user preference, specialized viewer wins.
    expect(artifactViewerRegistry.findViewer(tomlArtifact)?.id).toBe('pref-toml-viewer')

    // User sets monaco-viewer as permanent default for .toml.
    usePluginSettingsStore.getState().syncExtensionConfigLocal('file-viewer-defaults', {
      '.toml': 'monaco-viewer',
    })
    expect(artifactViewerRegistry.findViewer(tomlArtifact)?.id).toBe('monaco-viewer')

    // Clearing restores automatic priority selection.
    usePluginSettingsStore.getState().syncExtensionConfigLocal('file-viewer-defaults', {
      '.toml': '',
    })
    expect(artifactViewerRegistry.findViewer(tomlArtifact)?.id).toBe('pref-toml-viewer')

    // Declarative iframe file viewer renders host header + toolbarAction so "打开方式" stays accessible.
    artifactViewerRegistry.syncDeclarativeIframeViewers([
      {
        id: 'ndjson-iframe',
        name: 'NDJSON Iframe',
        version: '1.0.0',
        category: 'development',
        source: 'external',
        enabled: true,
        permissions_granted: true,
        runtime_state: 'ready',
        backend_type: 'none',
        ui_base_url: '/api/v1/ext/ndjson-iframe/_ui',
        frontend: { mode: 'iframe' },
        contributes: {
          viewers: [
            {
              id: 'ndjson-iframe-viewer',
              title: 'NDJSON 查看器',
              badge_label: '[NDJSON]',
              extensions: ['.ndjson'],
              ui_entry: 'ui/viewer.html',
              read_only: true,
              default_for_extensions: true,
            },
          ],
        },
      },
    ])
    const ndjsonArtifact: ArtifactRef = {
      ...tomlArtifact,
      id: 'file:events.ndjson',
      name: 'events.ndjson',
      path: '/tmp/workspace/events.ndjson',
    }
    const iframeViewer = artifactViewerRegistry.findViewer(ndjsonArtifact)
    expect(iframeViewer?.id).toBe('ndjson-iframe-viewer')
    const IframeComp = iframeViewer!.component
    render(
      <MantineProvider env="test">
        <IframeComp
          artifact={ndjsonArtifact}
          session={makeSession()}
          toolbarAction={<button data-testid="iframe-host-toolbar">打开方式</button>}
        />
      </MantineProvider>,
    )
    expect(screen.getByTestId('iframe-host-toolbar')).toBeTruthy()
    expect(screen.getByText('events.ndjson')).toBeTruthy()
    expect(screen.getByText('[NDJSON]')).toBeTruthy()

    disposePluginSubscriptions(ctx.subscriptions)
  })

  it('syncs schema extension config into ctx.api.getConfig() before activate() and enforces external ESM permissions', async () => {
    const extPlugin = makeEsmPluginView({
      id: 'ext-guarded',
      source: 'external',
      permissions: ['ui:slots', 'workspace:files.read'],
      granted_permissions: ['ui:slots'],
      settings: {
        mode: 'schema',
        schema: [{ key: 'pageSize', label: 'Page Size', type: 'number', default_value: 50 }],
      },
    })
    useExtensionStore.setState({ items: [extPlugin], hydrated: true })

    vi.spyOn(extensionsApi, 'getExtensionConfig').mockResolvedValue({
      plugin_id: 'ext-guarded',
      values: { pageSize: 200 },
    })

    let observedPageSize: unknown
    const loader = createEsmPluginLoader({
      syncExtensionStore: false,
      importer: async () => ({
        activate(ctx) {
          observedPageSize = ctx.api.getConfig<{ pageSize?: number }>().pageSize
        },
      }),
    })

    await loader.syncLoadedPlugins([extPlugin])
    expect(observedPageSize).toBe(200)

    const ctx = createScopedPluginContext(extPlugin)
    expect(() => ctx.api.readWorkspaceFile('README.md')).toThrow(/workspace:files\.read/)
    expect(() => ctx.api.writeWorkspaceFile('README.md', 'x')).toThrow(/workspace:files\.write/)
    expect(() => ctx.api.fetch('/api/v1/extensions')).toThrow(/受保护接口/)
    expect(() => ctx.api.fetch('/api/v1/auth/session')).toThrow(/受保护接口/)

    disposePluginSubscriptions(ctx.subscriptions)
    await loader.reset()
    useExtensionStore.getState().reset()
  })
})
