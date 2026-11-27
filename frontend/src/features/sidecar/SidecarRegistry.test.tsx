import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../domain/artifact'
import { registerBuiltinWorkspacePages } from '../extensions/builtinWorkspacePages'
import { extensionRegistry } from '../extensions/extensionRegistry'
import { useExtensionStore } from '../extensions/extensionStore'
import { PluginErrorBoundary } from '../extensions/PluginErrorBoundary'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { databaseToolRegistry, useHasDatabaseTool } from './databaseTool'
import { artifactViewerRegistry, useViewerRegistryVersion } from './registry'
import type { ArtifactViewer } from './types'

const REACT_LAZY_SYMBOL = Symbol.for('react.lazy')

function sampleArtifact(name: string, mediaType = 'application/octet-stream'): ArtifactRef {
  return {
    id: `local:sess:${name}`,
    name,
    kind: 'workspace_file',
    mediaType,
    readUrl: `/api/v1/files/raw?path=${encodeURIComponent(name)}`,
    writable: false,
    sessionId: 'sess-1',
    agentId: 'codex-1',
  }
}

describe('ArtifactViewerRegistry lazy loading, disabled filtering & hot-plug', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    usePluginSettingsStore.getState().reset()
    useExtensionStore.getState().reset()
    artifactViewerRegistry.unregister('test-cad-viewer')
    artifactViewerRegistry.unregister('test-hot-viewer')
    databaseToolRegistry.unregister('test-db-tool')
    extensionRegistry.clear()
    registerBuiltinWorkspacePages()
  })

  it('registers all heavy built-in viewers lazily and excludes migrated official ESM viewers', () => {
    expect(artifactViewerRegistry.getViewerById('three-viewer')).toBeNull()
    expect(artifactViewerRegistry.getViewerById('excalidraw-viewer')).toBeNull()
    expect(artifactViewerRegistry.getViewerById('drawio-viewer')).toBeNull()
    expect(artifactViewerRegistry.getViewerById('dbx-viewer')).toBeNull()
    expect(artifactViewerRegistry.getViewerById('gitlab-ci-viewer')).toBeNull()
    expect(databaseToolRegistry.getActiveProvider()).toBeNull()

    const lazyViewerIds = [
      'browser-mirror-viewer',
      'pdf-viewer',
      'docx-viewer',
      'sheet-viewer',
      'pptx-viewer',
      'mermaid-viewer',
      'diff-viewer',
      'html-viewer',
      'image-viewer',
      'video-viewer',
      'audio-viewer',
      'xterm-viewer',
      'monaco-viewer',
      'filetree-viewer',
      'git-diff-viewer',
      'git-worktrees-viewer',
      'sidecar-chat-viewer',
      'agent-graph-viewer',
    ]

    for (const id of lazyViewerIds) {
      const viewer = artifactViewerRegistry.getViewerById(id)
      expect(viewer, `expected viewer "${id}" to be registered`).not.toBeNull()
      expect((viewer!.component as { $$typeof?: symbol }).$$typeof, `expected "${id}" to be React.lazy`).toBe(
        REACT_LAZY_SYMBOL,
      )
    }
  })

  it('reactively updates useHasDatabaseTool on register, dispose, and unregister', () => {
    const listener = vi.fn()
    const unsubscribe = databaseToolRegistry.subscribe(listener)

    function DbToolProbe() {
      const hasDb = useHasDatabaseTool()
      return <output data-testid="has-db">{String(hasDb)}</output>
    }

    render(<DbToolProbe />)
    expect(screen.getByTestId('has-db')).toHaveTextContent('false')

    let disposable!: { dispose(): void }
    act(() => {
      disposable = databaseToolRegistry.register({
        id: 'test-db-tool',
        implementationName: 'DBX',
        mediaType: 'application/x-astrorder-dbx',
        openWorkbench: vi.fn(),
        openSqlFile: vi.fn(async () => {}),
        openSqlSnippet: vi.fn(),
      })
    })
    expect(screen.getByTestId('has-db')).toHaveTextContent('true')
    expect(listener).toHaveBeenCalled()

    act(() => {
      disposable.dispose()
    })
    expect(screen.getByTestId('has-db')).toHaveTextContent('false')

    act(() => {
      databaseToolRegistry.register({
        id: 'test-db-tool',
        implementationName: 'DBX',
        mediaType: 'application/x-astrorder-dbx',
        openWorkbench: vi.fn(),
        openSqlFile: vi.fn(async () => {}),
        openSqlSnippet: vi.fn(),
      })
    })
    expect(screen.getByTestId('has-db')).toHaveTextContent('true')

    act(() => {
      expect(databaseToolRegistry.unregister('test-db-tool')).toBe(true)
      expect(databaseToolRegistry.unregister('test-db-tool')).toBe(false)
    })
    expect(screen.getByTestId('has-db')).toHaveTextContent('false')
    unsubscribe()
  })

  it('filters disabled viewer extensions and refreshes regex cache when settings toggle', () => {
    expect(artifactViewerRegistry.getAllRegisteredExtensions().has('.pdf')).toBe(true)
    expect(artifactViewerRegistry.isSupportedExtension('docs/report.pdf')).toBe(true)
    const beforePattern = artifactViewerRegistry.getExtensionPattern()
    expect('见「docs/report.pdf」'.match(beforePattern)).toEqual(['docs/report.pdf'])

    usePluginSettingsStore.setState({
      enabledPlugins: { 'pdf-viewer': false },
      hydrated: true,
    })

    expect(artifactViewerRegistry.getAllRegisteredExtensions().has('.pdf')).toBe(false)
    expect(artifactViewerRegistry.isSupportedExtension('docs/report.pdf')).toBe(false)
    const disabledPattern = artifactViewerRegistry.getExtensionPattern()
    expect(disabledPattern).not.toBe(beforePattern)
    expect('见「docs/report.pdf」'.match(disabledPattern)).toBeNull()

    // Re-enabling restores recognition immediately
    usePluginSettingsStore.setState({
      enabledPlugins: { 'pdf-viewer': true },
      hydrated: true,
    })

    expect(artifactViewerRegistry.getAllRegisteredExtensions().has('.pdf')).toBe(true)
    expect(artifactViewerRegistry.isSupportedExtension('docs/report.pdf')).toBe(true)
    const restoredPattern = artifactViewerRegistry.getExtensionPattern()
    expect('见「docs/report.pdf」'.match(restoredPattern)).toEqual(['docs/report.pdf'])
  })

  it('supports hot register, same-id replacement, unregister, and reactive version subscription', () => {
    const listener = vi.fn()
    const unsubscribe = artifactViewerRegistry.subscribe(listener)
    const v0 = artifactViewerRegistry.getSnapshotVersion()

    function VersionProbe() {
      const version = useViewerRegistryVersion()
      const hasHot = Boolean(artifactViewerRegistry.getViewerById('test-hot-viewer'))
      return <output data-testid="ver">{`${version}:${hasHot}`}</output>
    }

    render(<VersionProbe />)
    expect(screen.getByTestId('ver')).toHaveTextContent(`${v0}:false`)

    const v1Viewer: ArtifactViewer = {
      id: 'test-hot-viewer',
      pluginId: 'test-hot-plugin',
      source: 'esm',
      title: 'Hot Viewer v1',
      icon: () => null,
      extensions: ['.cadx'],
      component: () => <div>v1</div>,
      capabilities: { canEdit: false },
    }
    const v2Viewer: ArtifactViewer = {
      ...v1Viewer,
      title: 'Hot Viewer v2',
      extensions: ['.cadx', '.stepx'],
    }

    let handle1!: { dispose(): void }
    let handle2!: { dispose(): void }
    act(() => {
      handle1 = artifactViewerRegistry.register(v1Viewer)
    })
    expect(artifactViewerRegistry.getSnapshotVersion()).toBeGreaterThan(v0)
    expect(listener).toHaveBeenCalled()
    expect(artifactViewerRegistry.isSupportedExtension('part.cadx')).toBe(true)
    expect(artifactViewerRegistry.findViewer(sampleArtifact('part.cadx'))?.title).toBe('Hot Viewer v1')
    expect(screen.getByTestId('ver').textContent).toMatch(/:true$/)

    // Hot replace with same id; disposing old handle1 must NOT evict v2Viewer
    act(() => {
      handle2 = artifactViewerRegistry.register(v2Viewer)
      handle1.dispose()
    })
    expect(artifactViewerRegistry.getViewerById('test-hot-viewer')?.title).toBe('Hot Viewer v2')
    expect(artifactViewerRegistry.isSupportedExtension('part.stepx')).toBe(true)

    // Disposing active handle2 removes it cleanly
    act(() => {
      handle2.dispose()
    })
    expect(artifactViewerRegistry.getViewerById('test-hot-viewer')).toBeNull()
    expect(artifactViewerRegistry.isSupportedExtension('part.cadx')).toBe(false)
    expect(screen.getByTestId('ver').textContent).toMatch(/:false$/)

    // Unregister returns true when present and false when absent
    act(() => {
      artifactViewerRegistry.register(v1Viewer)
    })
    expect(artifactViewerRegistry.unregister('test-hot-viewer')).toBe(true)
    expect(artifactViewerRegistry.unregister('test-hot-viewer')).toBe(false)
    unsubscribe()
  })
})

describe('Unified ExtensionRegistry slots (pages, viewers, commands) & Disposable', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    usePluginSettingsStore.getState().reset()
    extensionRegistry.clear()
    registerBuiltinWorkspacePages()
  })

  it('registers, queries, filters by office mode & enabled state, and disposes pages and commands', () => {
    const devPage = extensionRegistry.registerPage({
      pluginId: 'ext-demo',
      id: 'board',
      title: '研发看板',
      order: 150,
      hideInOfficeMode: true,
      component: () => <div>board</div>,
      source: 'esm',
    })
    const officePage = extensionRegistry.registerPage({
      pluginId: 'ext-office',
      id: 'notes',
      title: '共享笔记',
      order: 140,
      hideInOfficeMode: false,
      component: () => <div>notes</div>,
      source: 'esm',
    })
    const cmdRun = vi.fn()
    const devCmd = extensionRegistry.registerCommand({
      pluginId: 'ext-demo',
      id: 'sync-board',
      title: '同步研发看板',
      hideInOfficeMode: true,
      order: 10,
      run: cmdRun,
      source: 'esm',
    })
    const officeCmd = extensionRegistry.registerCommand({
      pluginId: 'ext-office',
      id: 'new-note',
      title: '新建共享笔记',
      hideInOfficeMode: false,
      order: 20,
      run: vi.fn(),
      source: 'esm',
    })

    expect(extensionRegistry.getPageById('ext-demo', 'board')?.title).toBe('研发看板')

    const devPages = extensionRegistry.getActivePages({ profile: 'developer' })
    expect(devPages.map((p) => `${p.pluginId}:${p.id}`)).toContain('ext-demo:board')
    expect(devPages.map((p) => `${p.pluginId}:${p.id}`)).toContain('ext-office:notes')
    expect(devPages.findIndex((p) => p.id === 'notes')).toBeLessThan(devPages.findIndex((p) => p.id === 'board'))

    const officePages = extensionRegistry.getActivePages({ profile: 'office' })
    expect(officePages.map((p) => `${p.pluginId}:${p.id}`)).not.toContain('ext-demo:board')
    expect(officePages.map((p) => `${p.pluginId}:${p.id}`)).toContain('ext-office:notes')

    const disabledFiltered = extensionRegistry.getActivePages({
      profile: 'developer',
      enabledPlugins: { 'ext-office': false },
    })
    expect(disabledFiltered.map((p) => `${p.pluginId}:${p.id}`)).not.toContain('ext-office:notes')

    expect(extensionRegistry.getActiveCommands({ profile: 'developer' }).map((c) => c.id)).toEqual([
      'sync-board',
      'new-note',
    ])
    expect(extensionRegistry.getActiveCommands({ profile: 'office' }).map((c) => c.id)).toEqual([
      'new-note',
    ])

    devPage.dispose()
    officePage.dispose()
    devCmd.dispose()
    officeCmd.dispose()

    expect(extensionRegistry.getPageById('ext-demo', 'board')).toBeUndefined()
    expect(extensionRegistry.getActiveCommands({ profile: 'developer' })).toEqual([])
  })

  it('bridges registerViewer to artifactViewerRegistry and clears only dynamic viewers on clear()', () => {
    const viewerDisposable = extensionRegistry.registerViewer({
      id: 'test-cad-viewer',
      pluginId: 'test-cad-plugin',
      source: 'esm',
      title: 'CAD 查看器',
      icon: () => null,
      extensions: ['.mycad'],
      component: () => <div>cad</div>,
      capabilities: { canEdit: false },
    })

    expect(artifactViewerRegistry.getViewerById('test-cad-viewer')?.title).toBe('CAD 查看器')
    expect(artifactViewerRegistry.isSupportedExtension('model.mycad')).toBe(true)

    viewerDisposable.dispose()
    expect(artifactViewerRegistry.getViewerById('test-cad-viewer')).toBeNull()
    expect(artifactViewerRegistry.isSupportedExtension('model.mycad')).toBe(false)

    // Register again and verify clear() removes dynamic viewer while keeping built-in viewers
    extensionRegistry.registerViewer({
      id: 'test-cad-viewer',
      pluginId: 'test-cad-plugin',
      source: 'esm',
      title: 'CAD 查看器',
      icon: () => null,
      extensions: ['.mycad'],
      component: () => <div>cad</div>,
      capabilities: { canEdit: false },
    })
    extensionRegistry.clear()
    expect(artifactViewerRegistry.getViewerById('test-cad-viewer')).toBeNull()
    expect(artifactViewerRegistry.getViewerById('monaco-viewer')).not.toBeNull()
  })
})

describe('PluginErrorBoundary error isolation', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    usePluginSettingsStore.getState().reset()
    useExtensionStore.getState().reset()
  })

  it('isolates crashes, recovers on retry, and disables the crashing plugin', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    let shouldCrash = true
    function FlakyPlugin() {
      if (shouldCrash) throw new Error('WebGL context lost')
      return <div data-testid="plugin-recovered">Recovered OK</div>
    }

    const onRetry = vi.fn()
    const toggleSpy = vi.spyOn(usePluginSettingsStore.getState(), 'togglePlugin').mockImplementation(() => {})

    render(
      <MantineProvider>
        <div data-testid="host-shell">Host Shell Alive</div>
        <PluginErrorBoundary pluginId="three-viewer" pluginName="3D 模型预览" onRetry={onRetry}>
          <FlakyPlugin />
        </PluginErrorBoundary>
      </MantineProvider>,
    )

    expect(screen.getByTestId('host-shell')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('3D 模型预览 渲染异常')).toBeInTheDocument()
    expect(screen.getByText('WebGL context lost')).toBeInTheDocument()

    // Retry recovers when fault clears
    shouldCrash = false
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(onRetry).toHaveBeenCalledOnce()
    expect(screen.getByTestId('plugin-recovered')).toBeInTheDocument()

    // Crash again and disable via button
    cleanup()
    shouldCrash = true
    render(
      <MantineProvider>
        <PluginErrorBoundary pluginId="three-viewer" pluginName="3D 模型预览">
          <FlakyPlugin />
        </PluginErrorBoundary>
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '停用该插件' }))
    await waitFor(() => expect(toggleSpy).toHaveBeenCalledWith('three-viewer', false))
  })

  it('disables via useExtensionStore when the plugin is managed as an extension', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    function AlwaysCrash(): never {
      throw new Error('ESM crash')
    }

    const setEnabledSpy = vi.fn().mockResolvedValue({ item: undefined })
    useExtensionStore.setState({
      items: [
        {
          id: 'esm-ext-demo',
          name: 'ESM Demo',
          version: '1.0.0',
          category: 'workspace',
          source: 'external',
          enabled: true,
          runtime_state: 'ready',
          backend_type: 'none',
        },
      ],
      hydrated: true,
      setEnabled: setEnabledSpy,
    })

    render(
      <MantineProvider>
        <PluginErrorBoundary pluginId="esm-ext-demo" pluginName="ESM Demo">
          <AlwaysCrash />
        </PluginErrorBoundary>
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '停用该插件' }))
    await waitFor(() => expect(setEnabledSpy).toHaveBeenCalledWith('esm-ext-demo', false))
  })
})
