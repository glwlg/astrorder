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
import { useExtensionStore } from '../extensions/extensionStore'
import { PENDING_PLUGIN_CONFIG_SESSION_KEY } from '../extensions/types'

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
  usePluginSettingsStore.getState().reset()
  useExtensionStore.getState().reset()
  extensionRegistry.clear()
})

describe('PluginsPage Component', () => {
  it('renders plugin management header, system module plugins, and list of viewers', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    render(
      <MantineProvider>
        <PluginsPage />
      </MantineProvider>,
    )

    expect(screen.getByText('插件管理')).toBeInTheDocument()
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
    expect(screen.getByText('3D 模型/CAD 预览')).toBeInTheDocument()
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

  it('organizes plugins by category tabs and supports switching provider implementations', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
    render(<MantineProvider><PluginsPage /></MantineProvider>)

    // Category tablist exists with all 5 categories + All
    expect(screen.getByRole('tablist', { name: '插件分类' })).toBeInTheDocument()
    const devTab = screen.getByRole('tab', { name: /开发与数据/ })
    const diagramTab = screen.getByRole('tab', { name: /图表与白板/ })
    expect(devTab).toBeInTheDocument()
    expect(diagramTab).toBeInTheDocument()

    // Switch to "开发与数据" — shows generic "数据库管理工具" with selectable DBX provider option
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

    // Switch to "图表与白板" — shows generic flowchart viewer with Draw.io provider option
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
    fireEvent.click(await screen.findByText('System One'))
    await waitFor(() => expect(updateEngine).toHaveBeenCalledWith('jev_ultrafast'))
    expect(await screen.findByText('当前实现: System One')).toBeInTheDocument()
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
    vi.spyOn(extensionsApi, 'listExtensions').mockResolvedValue({ items: [] })

    extensionRegistry.registerBuiltin({
      pluginId: 'gitlab-ci-viewer',
      settingsTitle: 'GitLab CI 实例配置',
      settingsButtonLabel: '配置实例',
      settingsComponent: () => <div>GitLab 自定义抽屉内容</div>,
    })

    render(<MantineProvider><PluginsPage /></MantineProvider>)

    fireEvent.change(screen.getByRole('textbox', { name: '搜索插件' }), { target: { value: 'gitlab' } })
    const cfgBtn = await screen.findByRole('button', { name: '配置实例' })
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
})
