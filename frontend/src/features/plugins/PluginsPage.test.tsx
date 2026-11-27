import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { PluginsPage } from './PluginsPage'
import {
  CODE_CHECKPOINTS_PLUGIN_ID,
  COMMAND_COMPRESSION_PLUGIN_ID,
  SIDEBAR_INTEGRATIONS_PLUGIN_ID,
  VOICE_INPUT_PLUGIN_ID,
} from './builtinPlugins'
import { usePluginSettingsStore } from './pluginSettingsStore'
import { api, type LocalVoicePluginStatus } from '../../api/client'
import { extensionsApi, type PluginView } from '../../api/extensions'
import { extensionRegistry } from '../extensions/extensionRegistry'
import { esmPluginLoader } from '../extensions/esmPluginLoader'
import { useExtensionStore } from '../extensions/extensionStore'
import { PENDING_PLUGIN_CONFIG_SESSION_KEY } from '../extensions/types'
import { clearStoredAppProfileForTest, setAppProfile } from '../office/appProfile'
import { artifactViewerRegistry } from '../sidecar/registry'
import type { ArtifactViewer } from '../sidecar/types'

const officialThreePlugin: PluginView = {
  id: 'three-viewer',
  name: '3D 模型/CAD 预览',
  version: '1.0.0',
  category: 'media',
  description: '官方按需热加载 3D / CAD 模型交互预览插件（支持 .stl/.obj/.gltf/.glb）',
  source: 'official',
  enabled: false,
  permissions: [],
  granted_permissions: [],
  pending_permissions: [],
  permissions_granted: true,
  runtime_state: 'stopped',
  backend_type: 'none',
  default_enabled: false,
  signature_verified: true,
  content_digest: 'three-digest-01',
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
}

const officialExcalidrawPlugin: PluginView = {
  id: 'excalidraw-viewer',
  name: '手绘白板',
  version: '1.0.0',
  category: 'diagram',
  description: '轻量自由手绘白板，适合头脑风暴、逻辑设计与自由拓扑标注',
  source: 'official',
  enabled: true,
  permissions: [],
  granted_permissions: [],
  pending_permissions: [],
  permissions_granted: true,
  runtime_state: 'ready',
  backend_type: 'none',
  default_enabled: true,
  signature_verified: true,
  content_digest: 'excalidraw-digest-01',
  esm_base_url: '/api/v1/ext/excalidraw-viewer/_esm',
  frontend: {
    mode: 'esm',
    entry: 'ui/entry.js',
    styles: ['ui/entry.css'],
    sdk_version: '^1.0',
  },
  contributes: {
    viewers: [
      {
        id: 'excalidraw-viewer',
        title: '手绘白板',
        extensions: ['.excalidraw', '.excalidraw.json', '.excalidraw.svg'],
        hide_in_office_mode: false,
      },
    ],
  },
}

const officialDrawioPlugin: PluginView = {
  id: 'drawio-viewer',
  name: '矢量流程图',
  version: '1.0.0',
  category: 'diagram',
  description: '支持交互式查看与全功能编辑矢量架构图、泳道图及流程图',
  source: 'official',
  enabled: true,
  permissions: [],
  granted_permissions: [],
  pending_permissions: [],
  permissions_granted: true,
  runtime_state: 'ready',
  backend_type: 'none',
  default_enabled: true,
  signature_verified: true,
  content_digest: 'drawio-digest-01',
  esm_base_url: '/api/v1/ext/drawio-viewer/_esm',
  frontend: {
    mode: 'esm',
    entry: 'ui/entry.js',
    styles: ['ui/entry.css'],
    sdk_version: '^1.0',
  },
  contributes: {
    viewers: [
      {
        id: 'drawio-viewer',
        title: '矢量流程图',
        extensions: ['.drawio', '.drawio.xml', '.drawio.svg'],
        hide_in_office_mode: false,
      },
    ],
  },
}

const officialDbxPlugin: PluginView = {
  id: 'dbx-viewer',
  name: '数据库管理工具',
  version: '1.0.0',
  category: 'development',
  description: '在右侧工作台嵌入数据库管理工具，支持点击 .sql 文件与独立 SQL 代码块一键载入查询',
  source: 'official',
  enabled: true,
  permissions: [],
  granted_permissions: [],
  pending_permissions: [],
  permissions_granted: true,
  runtime_state: 'ready',
  backend_type: 'none',
  default_enabled: true,
  signature_verified: true,
  content_digest: 'dbx-digest-01',
  esm_base_url: '/api/v1/ext/dbx-viewer/_esm',
  frontend: {
    mode: 'esm',
    entry: 'ui/entry.js',
    styles: ['ui/entry.css'],
    sdk_version: '^1.0',
  },
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
}

const hybridGitLabPlugin: PluginView = {
  id: 'gitlab-ci-viewer',
  name: 'GitLab CI/CD 流水线',
  version: '1.0.0',
  category: 'development',
  description: '直连自建或云端 GitLab，查看当前仓库提交后的 CI 流水线、阶段作业与实时日志',
  source: 'builtin',
  enabled: true,
  permissions: [],
  granted_permissions: [],
  pending_permissions: [],
  permissions_granted: true,
  runtime_state: 'builtin',
  backend_type: 'builtin',
  default_enabled: true,
  signature_verified: true,
  content_digest: 'gitlab-digest-01',
  esm_base_url: '/api/v1/ext/gitlab-ci-viewer/_esm',
  frontend: {
    mode: 'esm',
    entry: 'ui/entry.js',
    styles: ['ui/entry.css'],
    sdk_version: '^1.0',
  },
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
}

function registerLoadedOfficialTestViewers() {
  const dbxViewer: ArtifactViewer = {
    id: 'dbx-viewer',
    pluginId: 'dbx-viewer',
    source: 'esm',
    title: '数据库管理工具',
    category: 'development',
    capabilitySlot: 'database-tool',
    capabilityLabel: '数据库工具',
    providerConfigKey: 'provider',
    icon: () => null,
    component: () => null,
    capabilities: { canEdit: false },
    configOptions: [
      {
        key: 'provider',
        label: '工具实现',
        type: 'select',
        options: [
          { label: 'DBX', value: 'dbx' },
          { label: '自定义 Web 工具', value: 'custom-web' },
        ],
        defaultValue: 'dbx',
      },
      {
        key: 'upstreamUrl',
        label: '服务地址',
        type: 'string',
        defaultValue: 'http://127.0.0.1:14224',
        visibleWhen: { key: 'provider', value: 'dbx' },
      },
      {
        key: 'customUrl',
        label: 'Web 工具地址',
        type: 'string',
        defaultValue: '',
        visibleWhen: { key: 'provider', value: 'custom-web' },
      },
    ],
  }
  const drawioViewer: ArtifactViewer = {
    id: 'drawio-viewer',
    pluginId: 'drawio-viewer',
    source: 'esm',
    title: '矢量流程图',
    category: 'diagram',
    capabilitySlot: 'diagram-flowchart',
    capabilityLabel: '流程图',
    providerConfigKey: 'provider',
    icon: () => null,
    component: () => null,
    capabilities: { canEdit: true },
    configOptions: [
      {
        key: 'provider',
        label: '工具实现',
        type: 'select',
        options: [
          { label: 'Draw.io (官方)', value: 'diagrams-net' },
          { label: 'Draw.io (自托管)', value: 'self-hosted' },
        ],
        defaultValue: 'diagrams-net',
      },
      {
        key: 'customOrigin',
        label: '自托管服务地址',
        type: 'string',
        defaultValue: '',
        visibleWhen: { key: 'provider', value: 'self-hosted' },
      },
      {
        key: 'autosave',
        label: '实时自动暂存修改',
        type: 'boolean',
        defaultValue: true,
      },
      {
        key: 'theme',
        label: '图表界面主题',
        type: 'select',
        options: [
          { label: '自动适应', value: 'auto' },
          { label: '浅色极简', value: 'min' },
          { label: '深色模式', value: 'dark' },
        ],
        defaultValue: 'min',
      },
    ],
  }
  const gitlabViewer: ArtifactViewer = {
    id: 'gitlab-ci-viewer',
    pluginId: 'gitlab-ci-viewer',
    source: 'esm',
    title: 'GitLab CI 流水线',
    category: 'development',
    icon: () => null,
    component: () => null,
    capabilities: { canEdit: false },
  }
  artifactViewerRegistry.register(dbxViewer)
  artifactViewerRegistry.register(drawioViewer)
  artifactViewerRegistry.register(gitlabViewer)
}

const disabledStatus: LocalVoicePluginStatus = {
  id: 'r2t2-local-voice', name: 'R2T2 本地流式听写', enabled: false, state: 'disabled',
  available: true, progress: null, error: null, pid: null, device: 'Vulkan0',
  model: 'Confucius4-R2T2-Q8_0.gguf',
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
  artifactViewerRegistry.unregister('dbx-viewer')
  artifactViewerRegistry.unregister('drawio-viewer')
  artifactViewerRegistry.unregister('excalidraw-viewer')
  artifactViewerRegistry.unregister('gitlab-ci-viewer')
})

describe('PluginsPage Component', () => {
  it('renders plugin management header, system module plugins, and list of viewers without duplicating ESM cards', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({
      items: [
        officialThreePlugin,
        officialExcalidrawPlugin,
        officialDrawioPlugin,
        officialDbxPlugin,
        hybridGitLabPlugin,
      ],
    })
    registerLoadedOfficialTestViewers()
    render(
      <MantineProvider>
        <PluginsPage />
      </MantineProvider>,
    )

    expect(screen.getByText('插件管理')).toBeInTheDocument()
    // Wait for extensionsApi.listExtensions() to hydrate so ESM cards replace any pre-registered viewer fallback
    expect(await screen.findByText('3D 模型/CAD 预览')).toBeInTheDocument()
    expect(screen.getByText('语音输入与听写')).toBeInTheDocument()
    expect(screen.getByText('命令输出压缩')).toBeInTheDocument()
    expect(screen.getByText('代码检查点')).toBeInTheDocument()
    expect(screen.getByText('Git Worktree 分叉工作区')).toBeInTheDocument()
    expect(screen.getByText('侧边栏外链集成')).toBeInTheDocument()
    expect(screen.getByText('定时备份与快照')).toBeInTheDocument()
    expect(screen.getByText('Agent 多账号与配额池')).toBeInTheDocument()
    expect(screen.getByText('LLM 网关同步与用量统计')).toBeInTheDocument()
    expect(screen.getByText('多 Agent 群聊协作')).toBeInTheDocument()
    expect(screen.getByText('多会话监控矩阵')).toBeInTheDocument()
    expect(screen.getByText('浏览器自动化')).toBeInTheDocument()
    expect(screen.getByText('矢量流程图')).toBeInTheDocument()
    expect(screen.getByText('Mermaid 架构图')).toBeInTheDocument()
    expect(screen.getByText('手绘白板')).toBeInTheDocument()
    expect(screen.getByText('代码变更比对 (Diff)')).toBeInTheDocument()
    // Hybrid builtin+ESM plugin rendered once (no duplicate cards for viewer + extension)
    expect(screen.getAllByText('GitLab CI/CD 流水线')).toHaveLength(1)
    expect(screen.getAllByText('矢量流程图')).toHaveLength(1)
    expect(screen.getAllByText('数据库管理工具')).toHaveLength(1)
    expect(screen.getAllByText('官方热加载').length).toBeGreaterThanOrEqual(5)
    expect(screen.getAllByText('未加载（零占用）').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('HTML 页面预览')).toBeInTheDocument()
    expect(screen.getByText('决策与轨迹')).toBeInTheDocument()
  })

  it('persists plugin toggles to the backend database instead of localStorage', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    const updateSpy = vi.spyOn(api, 'updatePluginSettings').mockResolvedValue({
      enabled: { [SIDEBAR_INTEGRATIONS_PLUGIN_ID]: false },
      configs: {},
    })
    render(<MantineProvider><PluginsPage /></MantineProvider>)

    const toggle = await screen.findByRole('switch', { name: '启用 侧边栏外链集成' })
    fireEvent.click(toggle)
    await waitFor(() => expect(updateSpy).toHaveBeenCalledWith({
      enabled: { [SIDEBAR_INTEGRATIONS_PLUGIN_ID]: false },
    }))
    expect(localStorage.getItem('astrorder:plugins:enabled')).toBeNull()
  })

  it('migrates legacy localStorage plugin settings to the database on first hydration', async () => {
    localStorage.setItem('astrorder:plugins:enabled', JSON.stringify({ drawio: false }))
    localStorage.setItem('astrorder:plugins:config', JSON.stringify({ drawio: { theme: 'dark' } }))
    const updateSpy = vi.spyOn(api, 'updatePluginSettings').mockResolvedValue({
      enabled: { drawio: false },
      configs: { drawio: { theme: 'dark' } },
    })

    await usePluginSettingsStore.getState().hydrate(true)
    expect(updateSpy).toHaveBeenCalledWith({
      enabled: { drawio: false },
      configs: { drawio: { theme: 'dark' } },
      import_only: true,
    })
    expect(localStorage.getItem('astrorder:plugins:enabled')).toBeNull()
    expect(localStorage.getItem('astrorder:plugins:config')).toBeNull()
    expect(usePluginSettingsStore.getState().isPluginEnabled('drawio')).toBe(false)
  })

  it('starts the local voice runtime and allows switching voice provider to cloud Whisper', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    const setPlugin = vi.spyOn(api, 'setLocalVoicePlugin').mockResolvedValue({
      ...disabledStatus, enabled: true, state: 'running', pid: 1234,
    })
    const updateSpy = vi.spyOn(api, 'updatePluginSettings').mockResolvedValue({
      enabled: {},
      configs: { [VOICE_INPUT_PLUGIN_ID]: { provider: 'cloud-whisper' } },
    })

    render(<MantineProvider><PluginsPage /></MantineProvider>)
    fireEvent.click(screen.getByRole('tab', { name: /本地运行时/ }))
    expect(screen.getByText('命令输出压缩')).toBeInTheDocument()
    expect(screen.getByText('当前实现: RTK')).toBeInTheDocument()

    // Voice input & dictation lives under the "办公与文档" category
    fireEvent.click(screen.getByRole('tab', { name: /办公与文档/ }))
    expect(screen.getByText('当前实现: R2T2 本地流式')).toBeInTheDocument()

    const toggle = await screen.findByRole('switch', { name: '启用 R2T2 本地流式听写' })
    await waitFor(() => expect(toggle).not.toBeDisabled())
    fireEvent.click(toggle)
    await waitFor(() => expect(setPlugin).toHaveBeenCalledWith(true))
    expect(await screen.findByText(/流式听写已启用/)).toBeInTheDocument()

    // Switch voice provider to cloud Whisper
    const configButtons = screen.getAllByRole('button', { name: /参数配置/ })
    fireEvent.click(configButtons[configButtons.length - 1])
    fireEvent.click(screen.getByDisplayValue('R2T2 本地流式模型'))
    fireEvent.click(await screen.findByText('云端 Whisper 转写'))
    expect(screen.getByText('当前实现: 云端 Whisper')).toBeInTheDocument()
    await waitFor(() => expect(updateSpy).toHaveBeenCalledWith({
      configs: { [VOICE_INPUT_PLUGIN_ID]: { provider: 'cloud-whisper' } },
    }))
  })

  it('organizes plugins by category tabs and supports switching provider implementations on ESM cards', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({
      items: [officialDbxPlugin, officialDrawioPlugin],
    })
    registerLoadedOfficialTestViewers()
    render(<MantineProvider><PluginsPage /></MantineProvider>)
    await waitFor(() => expect(useExtensionStore.getState().hydrated).toBe(true))

    // Category tablist exists with all 5 categories + All
    expect(screen.getByRole('tablist', { name: '插件分类' })).toBeInTheDocument()
    const devTab = screen.getByRole('tab', { name: /开发与数据/ })
    const diagramTab = screen.getByRole('tab', { name: /图表与白板/ })
    expect(devTab).toBeInTheDocument()
    expect(diagramTab).toBeInTheDocument()

    // Switch to "开发与数据" — shows official ESM "数据库管理工具" with selectable DBX provider option
    fireEvent.click(devTab)
    expect(devTab).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText('数据库管理工具')).toBeInTheDocument()
    expect(screen.getByText('Git Worktree 分叉工作区')).toBeInTheDocument()
    expect(screen.getByText('当前实现: DBX')).toBeInTheDocument()
    expect(screen.queryByText('矢量流程图')).toBeNull()

    // Expanding config on the database plugin card shows "工具实现" selector with DBX as an option alongside custom web tool
    const dbCard = screen.getByText('数据库管理工具').closest('.mantine-Card-root') as HTMLElement
    expect(dbCard).toBeTruthy()
    fireEvent.click(dbCard.querySelector('button[class*="mantine-Button-root"]')!)
    expect(screen.getByText('工具实现')).toBeInTheDocument()
    expect(screen.getByDisplayValue('DBX')).toBeInTheDocument()
    expect(screen.getByDisplayValue('http://127.0.0.1:14224')).toBeInTheDocument()
    expect(screen.queryByText('Web 工具地址')).toBeNull()

    // Switching "工具实现" to "自定义 Web 工具" swaps the active provider badge and config fields
    const updateSpy = vi.spyOn(api, 'updatePluginSettings').mockResolvedValue({
      enabled: {},
      configs: { 'dbx-viewer': { provider: 'custom-web' } },
    })
    fireEvent.click(screen.getByDisplayValue('DBX'))
    fireEvent.click(await screen.findByText('自定义 Web 工具'))
    expect(screen.getByText('当前实现: 自定义 Web 工具')).toBeInTheDocument()
    expect(screen.getByText('Web 工具地址')).toBeInTheDocument()
    expect(screen.queryByText('服务地址')).toBeNull()
    await waitFor(() => expect(updateSpy).toHaveBeenCalledWith({
      configs: { 'dbx-viewer': { provider: 'custom-web' } },
    }))

    // Switch to "图表与白板" — shows official ESM flowchart viewer with Draw.io provider option
    fireEvent.click(diagramTab)
    expect(screen.getByText('矢量流程图')).toBeInTheDocument()
    expect(screen.getByText('当前实现: Draw.io (官方)')).toBeInTheDocument()
    expect(screen.queryByText('数据库管理工具')).toBeNull()

    // Search filters plugins across categories (including implementation names like rtk / openviking)
    fireEvent.click(screen.getByRole('tab', { name: /全部/ }))
    fireEvent.change(screen.getByRole('textbox', { name: '搜索插件' }), { target: { value: 'rtk' } })
    expect(screen.getByText('命令输出压缩')).toBeInTheDocument()
    expect(screen.queryByText('矢量流程图')).toBeNull()

    // Toggling command-compression persists cleanly
    updateSpy.mockResolvedValueOnce({
      enabled: { [COMMAND_COMPRESSION_PLUGIN_ID]: false },
      configs: {},
    })
    fireEvent.click(screen.getByRole('switch', { name: '启用 命令输出压缩' }))
    await waitFor(() => expect(updateSpy).toHaveBeenCalledWith({
      enabled: { [COMMAND_COMPRESSION_PLUGIN_ID]: false },
    }))
  })

  it('switches browser automation engine directly from plugin settings', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    const updateEngine = vi.spyOn(api, 'updateBrowserEngine').mockResolvedValue({ engine: 'jev_ultrafast' })

    render(<MantineProvider><PluginsPage /></MantineProvider>)
    fireEvent.change(screen.getByRole('textbox', { name: '搜索插件' }), { target: { value: '浏览器自动化' } })
    expect(screen.getByText('浏览器自动化')).toBeInTheDocument()
    expect(await screen.findByText('当前实现: Browser Use')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /参数配置/ }))
    fireEvent.click(screen.getByDisplayValue('Browser Use'))
    fireEvent.click(await screen.findByText('决策模型极速引擎'))
    await waitFor(() => expect(updateEngine).toHaveBeenCalledWith('jev_ultrafast'))
    expect(await screen.findByText('当前实现: 决策模型极速引擎')).toBeInTheDocument()
  })

  it('allows configuring code checkpoints maxAutoSnapshots count and toggling autoCapture', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    const updateSpy = vi.spyOn(api, 'updatePluginSettings').mockResolvedValue({
      enabled: {},
      configs: { [CODE_CHECKPOINTS_PLUGIN_ID]: { maxAutoSnapshots: 15 } },
    })

    render(<MantineProvider><PluginsPage /></MantineProvider>)
    fireEvent.change(screen.getByRole('textbox', { name: '搜索插件' }), { target: { value: '代码检查点' } })
    expect(screen.getByText('代码检查点')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /参数配置/ }))
    const limitInput = screen.getByLabelText('自动保留快照数')
    expect(limitInput).toHaveValue('20')

    fireEvent.change(limitInput, { target: { value: '15' } })
    fireEvent.blur(limitInput)
    await waitFor(() => expect(updateSpy).toHaveBeenCalledWith({
      configs: { [CODE_CHECKPOINTS_PLUGIN_ID]: { maxAutoSnapshots: 15 } },
    }))
  })

  it('opens PluginConfigDrawer directly from card button and astrorder:open-plugin-config event', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({ items: [hybridGitLabPlugin] })

    extensionRegistry.registerBuiltin({
      pluginId: 'gitlab-ci-viewer',
      settingsTitle: 'GitLab CI 实例配置',
      settingsButtonLabel: '配置实例',
      settingsComponent: () => <div>GitLab 自定义抽屉内容</div>,
    })

    render(<MantineProvider><PluginsPage /></MantineProvider>)

    fireEvent.change(screen.getByRole('textbox', { name: '搜索插件' }), { target: { value: 'gitlab' } })
    const cfgBtn = await screen.findByRole('button', { name: '配置实例' })
    expect(screen.queryByRole('button', { name: '卸载' })).toBeNull()
    fireEvent.click(cfgBtn)
    expect(await screen.findByText('GitLab 自定义抽屉内容')).toBeInTheDocument()

    // Also verifies sessionStorage handoff & window event opening
    cleanup()
    sessionStorage.setItem(PENDING_PLUGIN_CONFIG_SESSION_KEY, 'gitlab-ci-viewer')
    render(<MantineProvider><PluginsPage /></MantineProvider>)
    expect(await screen.findByText('GitLab 自定义抽屉内容')).toBeInTheDocument()
    expect(sessionStorage.getItem(PENDING_PLUGIN_CONFIG_SESSION_KEY)).toBeNull()
  })

  it('renders external plugin card, enforces permission grant on enable, and supports install & uninstall', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })

    const extPlugin: PluginView = {
      id: 'acme.hello-plugin',
      name: 'Acme Hello 扩展',
      version: '1.2.0',
      category: 'development',
      description: '示例第三方进程插件',
      author: 'Acme',
      source: 'external',
      enabled: false,
      permissions: ['workspace:git.read', 'storage:secrets'],
      granted_permissions: [],
      pending_permissions: ['workspace:git.read', 'storage:secrets'],
      permissions_granted: false,
      runtime_state: 'stopped',
      backend_type: 'process',
      settings: { mode: 'schema', schema: [] },
      contributes: {},
    }

    vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({ items: [extPlugin] })
    const enableSpy = vi
      .spyOn(extensionsApi, 'setExtensionEnabled')
      .mockResolvedValueOnce({
        requires_permission_grant: true,
        pending_permissions: ['workspace:git.read', 'storage:secrets'],
        item: extPlugin,
      })
      .mockResolvedValueOnce({
        item: {
          ...extPlugin,
          enabled: true,
          granted_permissions: ['workspace:git.read', 'storage:secrets'],
          pending_permissions: [],
          permissions_granted: true,
          runtime_state: 'ready',
        },
      })
    const uninstallSpy = vi.spyOn(extensionsApi, 'uninstallExtension').mockResolvedValue({
      ok: true,
      id: extPlugin.id,
    })

    render(<MantineProvider><PluginsPage /></MantineProvider>)

    expect(await screen.findByText('Acme Hello 扩展')).toBeInTheDocument()
    expect(screen.getByText('第三方')).toBeInTheDocument()

    // Toggle ON -> hits 409 requires_permission_grant -> opens PermissionGrantModal
    const toggle = screen.getByRole('switch', { name: '启用或禁用 Acme Hello 扩展' })
    fireEvent.click(toggle)

    expect(await screen.findByText('扩展权限授权确认')).toBeInTheDocument()
    expect(screen.getByText('读取工作区 Git 信息')).toBeInTheDocument()
    expect(screen.getByText('加密凭据存储')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '授权并启用' }))
    await waitFor(() =>
      expect(enableSpy).toHaveBeenNthCalledWith(2, 'acme.hello-plugin', true, [
        'workspace:git.read',
        'storage:secrets',
      ]),
    )
    expect(await screen.findByText('运行中')).toBeInTheDocument()

    // Uninstall flow with confirmation dialog
    fireEvent.click(screen.getByRole('button', { name: '卸载' }))
    expect(await screen.findByText('确认卸载外部插件')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '确认卸载' }))
    await waitFor(() => expect(uninstallSpy).toHaveBeenCalledWith('acme.hello-plugin'))
    await waitFor(() => expect(screen.queryByText('Acme Hello 扩展')).toBeNull())
  })

  it('hides external process plugins, install entry, and dev-only plugins in global office profile even when searching', async () => {
    setAppProfile('office', { syncPluginStore: false })
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    const extPlugin: PluginView = {
      id: 'acme.proc-plugin',
      name: 'Acme 本地进程扩展',
      version: '1.0.0',
      category: 'office',
      description: '外部本地进程插件',
      source: 'external',
      enabled: false,
      permissions: [],
      granted_permissions: [],
      pending_permissions: [],
      permissions_granted: true,
      runtime_state: 'stopped',
      backend_type: 'process',
      settings: { mode: 'schema', schema: [] },
      contributes: {},
    }
    vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({
      items: [extPlugin, officialDrawioPlugin, officialDbxPlugin, hybridGitLabPlugin],
    })

    render(<MantineProvider><PluginsPage /></MantineProvider>)

    expect(screen.getByText('内置功能开关')).toBeInTheDocument()
    expect(screen.getByText('会议纪要与行动项')).toBeInTheDocument()
    expect(await screen.findByText('矢量流程图')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /安装外部插件/ })).toBeNull()
    expect(screen.queryByText('Acme 本地进程扩展')).toBeNull()
    expect(screen.queryByText('数据库管理工具')).toBeNull()
    expect(screen.queryByText('GitLab CI/CD 流水线')).toBeNull()
    expect(screen.queryByText('Git Worktree 分叉工作区')).toBeNull()
    expect(screen.queryByText('成长雷达（Bot 驱动主动学习）')).toBeNull()

    // Even searching must NOT expose external or dev plugins in office mode
    fireEvent.change(screen.getByRole('textbox', { name: '搜索插件' }), { target: { value: 'acme' } })
    expect(screen.queryByText('Acme 本地进程扩展')).toBeNull()
    fireEvent.change(screen.getByRole('textbox', { name: '搜索插件' }), { target: { value: 'worktree' } })
    expect(screen.queryByText('Git Worktree 分叉工作区')).toBeNull()
  })

  it('displays official ESM plugins in both office and developer modes with hot-load status and reload button', async () => {
    setAppProfile('office', { syncPluginStore: false })
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    const enabledOfficial: PluginView = {
      ...officialThreePlugin,
      enabled: true,
      runtime_state: 'ready',
    }
    vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({ items: [enabledOfficial] })
    const reloadSpy = vi.spyOn(esmPluginLoader, 'reloadPlugin').mockResolvedValue({
      ok: true,
      pluginId: enabledOfficial.id,
      digest: enabledOfficial.content_digest,
    })

    useExtensionStore.setState({
      esmStates: {
        [enabledOfficial.id]: {
          status: 'loaded',
          digest: enabledOfficial.content_digest,
          error: null,
          updatedAt: Date.now(),
        },
      },
    })

    render(<MantineProvider><PluginsPage /></MantineProvider>)

    expect(await screen.findByText('3D 模型/CAD 预览')).toBeInTheDocument()
    expect(screen.getByText('官方热加载')).toBeInTheDocument()
    expect(screen.getByText('已热加载')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '卸载' })).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: '重载' }))
    await waitFor(() => expect(reloadSpy).toHaveBeenCalledWith(enabledOfficial))

    // Error state displays error alert and red status badge
    useExtensionStore.getState().setEsmRuntimeState(enabledOfficial.id, {
      status: 'error',
      digest: enabledOfficial.content_digest,
      error: '加载 entry.js 失败',
      updatedAt: Date.now(),
    })
    expect(await screen.findByText('加载失败')).toBeInTheDocument()
    expect(screen.getByText('加载 entry.js 失败')).toBeInTheDocument()
  })

  it('displays backend-only builtin plugins (e.g. desktop-control) with schema config and permissions', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })

    const desktopPlugin: PluginView = {
      id: 'desktop-control',
      name: '电脑控制',
      version: '1.0.0',
      category: 'workspace',
      description: 'Windows 端内置包装 CursorTouch/Windows-MCP',
      author: 'Astrorder',
      icon: 'device-desktop',
      source: 'builtin',
      enabled: false,
      default_enabled: false,
      permissions: ['agent:capabilities', 'storage:kv', 'desktop:control'],
      granted_permissions: ['agent:capabilities', 'storage:kv', 'desktop:control'],
      pending_permissions: [],
      permissions_granted: true,
      runtime_state: 'builtin',
      backend_type: 'builtin',
      settings: {
        mode: 'schema',
        test_endpoint: '/test-connection',
        schema: [
          {
            key: 'mode',
            label: '控制模式',
            type: 'select',
            default_value: 'interactive',
            options: [{ label: '完整交互', value: 'interactive' }],
          },
        ],
      },
      contributes: {},
    }
    vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({ items: [desktopPlugin] })
    vi.spyOn(extensionsApi, 'getExtensionConfig').mockResolvedValue({
      plugin_id: 'desktop-control',
      values: { mode: 'interactive' },
      secrets_set: {},
      masked_secrets: {},
    })

    render(<MantineProvider><PluginsPage /></MantineProvider>)

    fireEvent.change(screen.getByRole('textbox', { name: '搜索插件' }), { target: { value: '电脑' } })
    expect(await screen.findByText('电脑控制')).toBeInTheDocument()
    expect(screen.getByText('官方内置')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: '启用或禁用 电脑控制' })).not.toBeChecked()

    fireEvent.click(screen.getByRole('button', { name: '配置' }))
    expect(await screen.findByRole('button', { name: '测试连接' })).toBeInTheDocument()
  })
})
