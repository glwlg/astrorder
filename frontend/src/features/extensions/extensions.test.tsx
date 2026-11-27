import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MantineProvider, Menu } from '@mantine/core'
import {
  extensionsApi,
  type DeclarativePillPayload,
  type PluginView,
} from '../../api/extensions'
import type { ArtifactRef } from '../../domain/artifact'
import type { Session } from '../../domain/types'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { clearStoredAppProfileForTest, setAppProfile } from '../office/appProfile'
import { extensionRegistry } from './extensionRegistry'
import { EXTENSION_STORE_SYNC_KEY, useExtensionStore } from './extensionStore'
import { InstallPluginModal } from './InstallPluginModal'
import { PermissionGrantModal } from './PermissionGrantModal'
import { PluginConfigDrawer } from './PluginConfigDrawer'
import {
  resolveSafeExtensionFetchPath,
  SandboxedPluginFrame,
} from './SandboxedPluginFrame'
import {
  ComposerStatusBarSlots,
  GitMenuExtensionItems,
  PluginPanelViewer,
  useWelcomeMenuExtensions,
} from './SlotRenderer'

const mockSession: Session = {
  id: 'sess-1',
  agent_id: 'agent-1',
  title: 'Test Session',
  workspace: '/repo/demo',
  status: 'idle',
  updated_at: '2026-01-01T00:00:00Z',
  connection_id: 'conn-1',
}

const sampleExternalPlugin: PluginView = {
  id: 'acme.deploy-helper',
  name: 'Deploy Helper',
  version: '0.5.0',
  category: 'development',
  description: 'Third-party deployment helper',
  author: 'Acme Labs',
  source: 'external',
  enabled: true,
  permissions: ['network:outbound', 'workspace:git.read', 'storage:secrets', 'ui:slots'],
  granted_permissions: ['network:outbound', 'workspace:git.read', 'storage:secrets', 'ui:slots'],
  pending_permissions: [],
  permissions_granted: true,
  runtime_state: 'ready',
  backend_type: 'process',
  ui_base_url: '/api/v1/ext/acme.deploy-helper/_ui',
  api_base_url: '/api/v1/ext/acme.deploy-helper',
  settings: {
    mode: 'schema',
    test_endpoint: '/test-connection',
    schema: [
      { key: 'endpoint', label: '服务地址', type: 'string', required: true, placeholder: 'https://deploy.local' },
      { key: 'api_token', label: 'API Token', type: 'secret', required: true },
      { key: 'auto_refresh', label: '自动刷新', type: 'boolean', default_value: true },
      { key: 'interval_sec', label: '轮询间隔', type: 'number', default_value: 15, min: 5, max: 300 },
      {
        key: 'env',
        label: '目标环境',
        type: 'select',
        default_value: 'staging',
        options: [
          { label: 'Staging', value: 'staging' },
          { label: 'Production', value: 'prod' },
        ],
      },
    ],
  },
  contributes: {
    ui_slots: [
      {
        slot: 'chat.composer.status_bar',
        id: 'deploy-pill',
        renderer: 'declarative_pill',
        label: 'Deploy',
        poll_endpoint: '/status-pill',
        poll_interval_ms: 15000,
        requires_workspace: true,
        on_click: { open_panel: 'dashboard' },
      },
      {
        slot: 'chat.git_menu.items',
        id: 'deploy-menu',
        label: '打开 Deploy 控制台',
        badge: '发布助手',
        requires_workspace: true,
        on_click: { open_panel: 'dashboard' },
      },
      {
        slot: 'sidecar.welcome_menu',
        id: 'deploy-welcome',
        label: 'Deploy 控制台',
        badge: '扩展工具',
        requires_workspace: true,
        on_click: { open_panel: 'dashboard' },
      },
    ],
    sidecar_panels: [
      {
        id: 'dashboard',
        title: 'Deploy Dashboard',
        badge_label: '[Deploy]',
        media_type: 'application/x-astrorder-ext.acme.deploy-helper.dashboard',
        ui_entry: 'panel.html',
      },
    ],
  },
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
  sessionStorage.clear()
  clearStoredAppProfileForTest()
  usePluginSettingsStore.getState().reset()
  useExtensionStore.getState().reset()
  extensionRegistry.clear()
})

describe('Extension API & Store', () => {
  it('handles 409 permission grant response cleanly in setExtensionEnabled without throwing', async () => {
    localStorage.setItem('astrorder:token', 'secret-browser-token')
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          detail: 'permission grant required',
          pending_permissions: ['network:outbound', 'storage:secrets'],
        }),
        { status: 409, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const result = await extensionsApi.setExtensionEnabled('acme.demo', true)
    expect(result.requires_permission_grant).toBe(true)
    expect(result.pending_permissions).toEqual(['network:outbound', 'storage:secrets'])
    const [url, init] = fetchSpy.mock.calls[0]
    expect(url).toBe('/api/v1/extensions/acme.demo/enable')
    const headers = new Headers(init?.headers)
    expect(headers.get('Authorization')).toBe('Bearer secret-browser-token')
  })

  it('gracefully tolerates hydrate failure and syncs pluginSettingsStore on enable', async () => {
    vi.spyOn(extensionsApi, 'listExtensions').mockRejectedValue(new Error('404 Not Found'))
    await expect(useExtensionStore.getState().hydrate()).resolves.toBeUndefined()
    expect(useExtensionStore.getState().items).toEqual([])
    expect(useExtensionStore.getState().error).toContain('404')

    vi.spyOn(extensionsApi, 'setExtensionEnabled').mockResolvedValue({
      item: sampleExternalPlugin,
    })
    await useExtensionStore.getState().setEnabled(sampleExternalPlugin.id, true, ['ui:slots'])
    expect(usePluginSettingsStore.getState().isPluginEnabled(sampleExternalPlugin.id)).toBe(true)
    expect(useExtensionStore.getState().getById(sampleExternalPlugin.id)?.enabled).toBe(true)
    expect(localStorage.getItem(EXTENSION_STORE_SYNC_KEY)).toContain(sampleExternalPlugin.id)
  })

  it('synchronizes extension toggle state across windows via storage events', async () => {
    useExtensionStore.setState({ items: [{ ...sampleExternalPlugin, enabled: true }], hydrated: true })
    usePluginSettingsStore.setState({ enabledPlugins: { [sampleExternalPlugin.id]: true } })

    const updatedPlugin: PluginView = {
      ...sampleExternalPlugin,
      enabled: false,
      runtime_state: 'stopped',
    }
    const listSpy = vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({
      items: [updatedPlugin],
    })

    // Simulate another window toggling the extension off via localStorage sync event
    window.dispatchEvent(
      new StorageEvent('storage', {
        key: EXTENSION_STORE_SYNC_KEY,
        newValue: JSON.stringify({
          ts: Date.now(),
          action: 'setEnabled',
          id: sampleExternalPlugin.id,
          enabled: false,
        }),
      }),
    )

    // Optimistic update applies synchronously and triggers background rehydration
    expect(usePluginSettingsStore.getState().isPluginEnabled(sampleExternalPlugin.id)).toBe(false)
    expect(useExtensionStore.getState().getById(sampleExternalPlugin.id)?.enabled).toBe(false)
    await waitFor(() => {
      expect(listSpy).toHaveBeenCalledTimes(1)
      expect(useExtensionStore.getState().getById(sampleExternalPlugin.id)?.runtime_state).toBe('stopped')
    })
  })
})

describe('SandboxedPluginFrame Bridge & Security', () => {
  it('validates extension fetch paths strictly against traversal and cross-plugin access', () => {
    expect(resolveSafeExtensionFetchPath('acme.demo', '/status-pill?ref=main')).toBe(
      '/api/v1/ext/acme.demo/status-pill?ref=main',
    )
    expect(resolveSafeExtensionFetchPath('acme.demo', '/api/v1/ext/acme.demo/items')).toBe(
      '/api/v1/ext/acme.demo/items',
    )
    expect(resolveSafeExtensionFetchPath('acme.demo', '../other-plugin/secret')).toBeNull()
    expect(resolveSafeExtensionFetchPath('acme.demo', '/api/v1/ext/other-plugin/secret')).toBeNull()
    expect(resolveSafeExtensionFetchPath('acme.demo', '/api/v1/settings/plugins')).toBeNull()
    expect(resolveSafeExtensionFetchPath('acme.demo', 'https://evil.example/steal')).toBeNull()
    expect(resolveSafeExtensionFetchPath('acme.demo', '%2e%2e/other/data')).toBeNull()
  })

  it('renders sandboxed iframe and handles postMessage bridge securely', async () => {
    localStorage.setItem('astrorder:token', 'host-token-123')
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true, env: 'staging' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const openPanelEvents: CustomEvent[] = []
    const insertEvents: CustomEvent[] = []
    const onOpenPanelEvent = (e: Event) => openPanelEvents.push(e as CustomEvent)
    const onInsertEvent = (e: Event) => insertEvents.push(e as CustomEvent)
    window.addEventListener('astrorder:open-plugin-panel', onOpenPanelEvent)
    window.addEventListener('astrorder:insert-composer', onInsertEvent)

    render(
      <MantineProvider>
        <SandboxedPluginFrame
          pluginId="acme.deploy-helper"
          src="/api/v1/ext/acme.deploy-helper/_ui/panel.html"
          title="Deploy Dashboard"
          session={mockSession}
          params={{ runId: 42 }}
          autoResize
        />
      </MantineProvider>,
    )

    const iframe = screen.getByTitle('Deploy Dashboard') as HTMLIFrameElement
    expect(iframe).toHaveAttribute('sandbox', 'allow-scripts allow-forms')

    const mockContentWindow = { postMessage: vi.fn() }
    Object.defineProperty(iframe, 'contentWindow', {
      configurable: true,
      value: mockContentWindow,
    })

    // 1. Messages from other sources MUST be ignored
    window.dispatchEvent(
      new MessageEvent('message', {
        source: window,
        data: { type: 'astrorder:get-context', id: 'spoofed' },
      }),
    )
    expect(mockContentWindow.postMessage).not.toHaveBeenCalled()

    // 2. Valid get-context from iframe contentWindow
    window.dispatchEvent(
      new MessageEvent('message', {
        source: mockContentWindow as unknown as MessageEventSource,
        data: { type: 'astrorder:get-context', requestId: 'req-1' },
      }),
    )
    expect(mockContentWindow.postMessage).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'astrorder:response',
        requestId: 'req-1',
        ok: true,
        data: expect.objectContaining({
          pluginId: 'acme.deploy-helper',
          sessionId: 'sess-1',
          workspace: '/repo/demo',
          params: { runId: 42 },
        }),
      }),
      '*',
    )

    // 3. Cross-plugin fetch is rejected with 403 and NEVER hits network
    window.dispatchEvent(
      new MessageEvent('message', {
        source: mockContentWindow as unknown as MessageEventSource,
        data: { type: 'astrorder:fetch', requestId: 'req-bad', path: '/api/v1/ext/other/steal' },
      }),
    )
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(mockContentWindow.postMessage).toHaveBeenCalledWith(
      expect.objectContaining({
        requestId: 'req-bad',
        ok: false,
        data: expect.objectContaining({ status: 403 }),
      }),
      '*',
    )

    // 4. Valid same-plugin fetch attaches host Authorization header
    window.dispatchEvent(
      new MessageEvent('message', {
        source: mockContentWindow as unknown as MessageEventSource,
        data: { type: 'astrorder:fetch', requestId: 'req-ok', path: '/status' },
      }),
    )
    await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(1))
    const [calledUrl, calledInit] = fetchSpy.mock.calls[0]
    expect(calledUrl).toBe('/api/v1/ext/acme.deploy-helper/status')
    expect(new Headers(calledInit?.headers).get('Authorization')).toBe('Bearer host-token-123')
    await waitFor(() =>
      expect(mockContentWindow.postMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          requestId: 'req-ok',
          ok: true,
          data: expect.objectContaining({ ok: true, status: 200, json: { ok: true, env: 'staging' } }),
        }),
        '*',
      ),
    )

    // 5. open-panel, insert-composer, and resize
    window.dispatchEvent(
      new MessageEvent('message', {
        source: mockContentWindow as unknown as MessageEventSource,
        data: { type: 'astrorder:open-panel', panelId: 'dashboard', params: { tab: 'logs' } },
      }),
    )
    expect(openPanelEvents).toHaveLength(1)
    expect(openPanelEvents[0].detail).toMatchObject({
      pluginId: 'acme.deploy-helper',
      panelId: 'dashboard',
      params: { tab: 'logs' },
    })

    window.dispatchEvent(
      new MessageEvent('message', {
        source: mockContentWindow as unknown as MessageEventSource,
        data: { type: 'astrorder:insert-composer', text: '请帮我排查部署日志' },
      }),
    )
    expect(insertEvents).toHaveLength(1)
    expect(insertEvents[0].detail).toMatchObject({ text: '请帮我排查部署日志' })

    window.dispatchEvent(
      new MessageEvent('message', {
        source: mockContentWindow as unknown as MessageEventSource,
        data: { type: 'astrorder:resize', height: 540 },
      }),
    )
    await waitFor(() => expect(iframe.style.height).toBe('540px'))

    window.removeEventListener('astrorder:open-plugin-panel', onOpenPanelEvent)
    window.removeEventListener('astrorder:insert-composer', onInsertEvent)
  })
})

describe('SlotRenderer (StatusBar, GitMenu, WelcomeMenu, PluginPanelViewer)', () => {
  it('renders built-in status slot and external declarative pill with click -> open-plugin-panel', async () => {
    extensionRegistry.registerBuiltin({
      pluginId: 'builtin-demo',
      statusBarComponent: ({ branch }) => <span data-testid="builtin-pill">Builtin:{branch}</span>,
    })
    useExtensionStore.setState({ items: [sampleExternalPlugin], hydrated: true })

    const pillPayload: DeclarativePillPayload = {
      visible: true,
      label: '部署正常',
      sublabel: 'v1.4',
      tone: 'success',
      dot_color: '#12b886',
      tooltip: '点击查看 Deploy Dashboard',
      panel_params: { release: 'v1.4' },
    }
    vi.spyOn(extensionsApi, 'fetchExtensionEndpoint').mockResolvedValue(pillPayload)

    const panelEvents: CustomEvent[] = []
    const onPanel = (e: Event) => panelEvents.push(e as CustomEvent)
    window.addEventListener('astrorder:open-plugin-panel', onPanel)

    render(
      <MantineProvider>
        <ComposerStatusBarSlots
          session={mockSession}
          statusUnavailable={false}
          statusLoading={false}
          branch="main"
        />
      </MantineProvider>,
    )

    expect(screen.getByTestId('builtin-pill')).toHaveTextContent('Builtin:main')
    const extBtn = await screen.findByRole('button', { name: '点击查看 Deploy Dashboard' })
    expect(extBtn).toHaveTextContent('v1.4')
    expect(extBtn).toHaveTextContent('部署正常')

    fireEvent.click(extBtn)
    expect(panelEvents).toHaveLength(1)
    expect(panelEvents[0].detail).toMatchObject({
      pluginId: 'acme.deploy-helper',
      panelId: 'dashboard',
      params: { release: 'v1.4' },
    })

    window.removeEventListener('astrorder:open-plugin-panel', onPanel)
  })

  it('renders Git menu items and welcome menu items from both builtin and external extensions', () => {
    const builtinClick = vi.fn()
    extensionRegistry.registerBuiltin({
      pluginId: 'builtin-demo',
      gitMenuItems: [
        {
          id: 'b-git',
          groupLabel: 'Builtin Group',
          label: '内置 Git 项',
          requiresWorkspace: true,
          onClick: builtinClick,
        },
      ],
      welcomeMenuItems: [
        {
          id: 'b-welcome',
          label: '内置欢迎入口',
          shortcut: 'Built-in',
          requiresWorkspace: true,
          hideInOfficeMode: true,
          onClick: builtinClick,
        },
      ],
    })
    useExtensionStore.setState({ items: [sampleExternalPlugin], hydrated: true })

    function WelcomeProbe({ office }: { office: boolean }) {
      const items = useWelcomeMenuExtensions(mockSession, office)
      return (
        <div>
          {items.map((item) => (
            <button key={item.id} onClick={item.onClick}>
              {item.label}
            </button>
          ))}
        </div>
      )
    }

    render(
      <MantineProvider>
        <Menu opened>
          <Menu.Target>
            <button type="button">target</button>
          </Menu.Target>
          <Menu.Dropdown>
            <GitMenuExtensionItems session={mockSession} />
          </Menu.Dropdown>
        </Menu>
        <WelcomeProbe office={false} />
      </MantineProvider>,
    )

    expect(screen.getByText('内置 Git 项')).toBeInTheDocument()
    expect(screen.getByText('打开 Deploy 控制台')).toBeInTheDocument()
    expect(screen.getByText('内置欢迎入口')).toBeInTheDocument()
    expect(screen.getByText('Deploy 控制台')).toBeInTheDocument()

    fireEvent.click(screen.getByText('内置 Git 项'))
    expect(builtinClick).toHaveBeenCalledWith(mockSession)
  })

  it('renders builtin panel component or sandboxed iframe in PluginPanelViewer', () => {
    extensionRegistry.registerBuiltin({
      pluginId: 'builtin-panel-ext',
      panels: {
        main: {
          title: 'Builtin Panel',
          component: ({ pluginId, panelId }) => (
            <div data-testid="builtin-panel-view">
              {pluginId}/{panelId}
            </div>
          ),
        },
      },
    })
    useExtensionStore.setState({ items: [sampleExternalPlugin], hydrated: true })

    const builtinArtifact: ArtifactRef = {
      id: 'ext:builtin-panel-ext:main:sess-1',
      sessionId: 'sess-1',
      agentId: 'agent-1',
      name: 'Builtin Panel',
      kind: 'remote_url',
      readUrl: '',
      mediaType: 'application/x-astrorder-ext.builtin-panel-ext.main',
      writable: false,
    }
    const { rerender } = render(
      <MantineProvider>
        <PluginPanelViewer artifact={builtinArtifact} session={mockSession} />
      </MantineProvider>,
    )
    expect(screen.getByTestId('builtin-panel-view')).toHaveTextContent('builtin-panel-ext/main')

    const externalArtifact: ArtifactRef = {
      id: 'ext:acme.deploy-helper:dashboard:sess-1',
      sessionId: 'sess-1',
      agentId: 'agent-1',
      name: 'Deploy Dashboard',
      kind: 'remote_url',
      readUrl: '',
      mediaType: 'application/x-astrorder-ext.acme.deploy-helper.dashboard',
      writable: false,
    }
    rerender(
      <MantineProvider>
        <PluginPanelViewer artifact={externalArtifact} session={mockSession} />
      </MantineProvider>,
    )
    const frame = screen.getByTitle('Deploy Dashboard')
    expect(frame).toHaveAttribute('src', '/api/v1/ext/acme.deploy-helper/_ui/panel.html')
    expect(frame).toHaveAttribute('sandbox', 'allow-scripts allow-forms')
  })
})

describe('PluginConfigDrawer, PermissionGrantModal & InstallPluginModal', () => {
  it('renders schema config drawer with masked secrets, saves changes, and tests connection', async () => {
    useExtensionStore.setState({ items: [sampleExternalPlugin], hydrated: true })
    vi.spyOn(extensionsApi, 'getExtensionConfig').mockResolvedValue({
      plugin_id: sampleExternalPlugin.id,
      values: { endpoint: 'https://deploy.internal', auto_refresh: true, interval_sec: 20, env: 'staging' },
      secrets_set: { api_token: true },
      masked_secrets: { api_token: 'tok_***89' },
    })
    const updateSpy = vi.spyOn(extensionsApi, 'updateExtensionConfig').mockResolvedValue({
      plugin_id: sampleExternalPlugin.id,
      values: { endpoint: 'https://deploy-v2.internal', auto_refresh: true, interval_sec: 20, env: 'staging' },
      secrets_set: { api_token: true },
      masked_secrets: { api_token: 'tok_***89' },
    })
    const testSpy = vi.spyOn(extensionsApi, 'postExtensionEndpoint').mockResolvedValue({
      ok: true,
      message: '连接成功 (v2.1)',
    })

    render(
      <MantineProvider>
        <PluginConfigDrawer opened pluginId={sampleExternalPlugin.id} onClose={vi.fn()} />
      </MantineProvider>,
    )

    const endpointInput = await screen.findByLabelText(/服务地址/)
    expect(endpointInput).toHaveValue('https://deploy.internal')
    expect(screen.getByText(/已配置 \(tok_\*\*\*89\)/)).toBeInTheDocument()

    fireEvent.change(endpointInput, { target: { value: 'https://deploy-v2.internal' } })
    fireEvent.click(screen.getByRole('button', { name: '保存配置' }))

    await waitFor(() =>
      expect(updateSpy).toHaveBeenCalledWith(sampleExternalPlugin.id, {
        values: {
          endpoint: 'https://deploy-v2.internal',
          auto_refresh: true,
          interval_sec: 20,
          env: 'staging',
        },
      }),
    )

    fireEvent.click(screen.getByRole('button', { name: '测试连接' }))
    await waitFor(() =>
      expect(testSpy).toHaveBeenCalledWith(
        sampleExternalPlugin.id,
        '/test-connection',
        expect.objectContaining({
          values: expect.objectContaining({ endpoint: 'https://deploy-v2.internal' }),
        }),
      ),
    )
    expect(await screen.findByText('连接成功 (v2.1)')).toBeInTheDocument()
  })

  it('renders external custom_ui settings inside SandboxedPluginFrame', () => {
    const customUiPlugin: PluginView = {
      ...sampleExternalPlugin,
      id: 'acme.custom-ui',
      name: 'Custom UI Ext',
      ui_base_url: '/api/v1/ext/acme.custom-ui/_ui',
      settings: { mode: 'custom_ui', ui_entry: 'settings.html' },
    }
    useExtensionStore.setState({ items: [customUiPlugin], hydrated: true })

    render(
      <MantineProvider>
        <PluginConfigDrawer opened pluginId={customUiPlugin.id} onClose={vi.fn()} />
      </MantineProvider>,
    )

    const iframe = screen.getByTitle('Custom UI Ext 设置')
    expect(iframe).toHaveAttribute('src', '/api/v1/ext/acme.custom-ui/_ui/settings.html')
    expect(iframe).toHaveAttribute('sandbox', 'allow-scripts allow-forms')
  })

  it('renders PermissionGrantModal and confirms selected permissions', () => {
    const onConfirm = vi.fn()
    render(
      <MantineProvider>
        <PermissionGrantModal
          opened
          plugin={sampleExternalPlugin}
          pendingPermissions={['network:outbound', 'storage:secrets']}
          onConfirm={onConfirm}
          onClose={vi.fn()}
        />
      </MantineProvider>,
    )

    expect(screen.getByText('外部网络访问')).toBeInTheDocument()
    expect(screen.getByText('加密凭据存储')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '授权并启用' }))
    expect(onConfirm).toHaveBeenCalledWith(
      expect.arrayContaining(['network:outbound', 'storage:secrets']),
    )
  })

  it('highlights full-page risk and requires explicit acknowledgment for unsigned external ESM plugins', () => {
    const unsignedEsmPlugin: PluginView = {
      ...sampleExternalPlugin,
      id: 'acme.unsigned-esm',
      name: 'Unsigned ESM Tool',
      signature_verified: false,
      frontend: { mode: 'esm', entry: 'ui/entry.js', sdk_version: '^1.0' },
    }
    const onConfirm = vi.fn()
    const { rerender } = render(
      <MantineProvider>
        <PermissionGrantModal
          opened
          plugin={unsignedEsmPlugin}
          pendingPermissions={['ui:slots']}
          onConfirm={onConfirm}
          onClose={vi.fn()}
        />
      </MantineProvider>,
    )

    expect(screen.getByText(/未签名同页脚本插件拥有完整页面访问权限/)).toBeInTheDocument()
    const confirmBtn = screen.getByRole('button', { name: '授权并启用' })
    expect(confirmBtn).toBeDisabled()

    fireEvent.click(screen.getByRole('checkbox', { name: /允许在研发模式下加载未签名同页脚本插件/ }))
    expect(confirmBtn).not.toBeDisabled()
    fireEvent.click(confirmBtn)
    expect(onConfirm).toHaveBeenCalled()

    // Office mode must strictly block enabling unsigned external ESM plugins
    setAppProfile('office', { syncPluginStore: false })
    rerender(
      <MantineProvider>
        <PermissionGrantModal
          opened
          plugin={unsignedEsmPlugin}
          pendingPermissions={['ui:slots']}
          onConfirm={onConfirm}
          onClose={vi.fn()}
        />
      </MantineProvider>,
    )
    expect(screen.getByText(/办公模式下已严格禁止启用未签名的同页 ESM 脚本插件/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '授权并启用' })).toBeDisabled()
  })

  it('installs external plugin from local path in InstallPluginModal and triggers enable handoff', async () => {
    const installed: PluginView = {
      ...sampleExternalPlugin,
      enabled: false,
      permissions_granted: false,
      runtime_state: 'stopped',
    }
    const installSpy = vi.spyOn(extensionsApi, 'installExtensionFromPath').mockResolvedValue({
      item: installed,
    })
    const onInstalled = vi.fn()

    render(
      <MantineProvider>
        <InstallPluginModal opened onClose={vi.fn()} onInstalled={onInstalled} />
      </MantineProvider>,
    )

    fireEvent.change(screen.getByLabelText(/插件目录路径/), {
      target: { value: '/tmp/hello-plugin' },
    })
    fireEvent.click(screen.getByRole('button', { name: '从目录安装' }))

    await waitFor(() => expect(installSpy).toHaveBeenCalledWith('/tmp/hello-plugin'))
    expect(await screen.findByText(/安装成功/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '查看权限并启用' }))
    expect(onInstalled).toHaveBeenCalledWith(installed, true)
  })

  it('blocks external process plugin installation inside InstallPluginModal in office profile', () => {
    setAppProfile('office', { syncPluginStore: false })
    render(
      <MantineProvider>
        <InstallPluginModal opened onClose={vi.fn()} />
      </MantineProvider>,
    )

    expect(screen.getByText('办公模式下已禁用外部本地进程插件安装')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '从目录安装' })).toBeNull()
    expect(screen.queryByLabelText(/插件目录路径/)).toBeNull()
  })
})
