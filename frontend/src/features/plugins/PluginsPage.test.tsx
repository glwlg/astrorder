import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { PluginsPage } from './PluginsPage'
import { SIDEBAR_INTEGRATIONS_PLUGIN_ID, usePluginSettingsStore } from './pluginSettingsStore'
import { api, type LocalVoicePluginStatus } from '../../api/client'

const disabledStatus: LocalVoicePluginStatus = {
  id: 'r2t2-local-voice', name: 'R2T2 本地流式听写', enabled: false, state: 'disabled',
  available: true, progress: null, error: null, pid: null, device: 'Vulkan0',
  model: 'Confucius4-R2T2-Q8_0.gguf',
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
  usePluginSettingsStore.getState().reset()
})

describe('PluginsPage Component', () => {
  it('renders plugin management header, sidebar integrations plugin, and list of viewers', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    render(
      <MantineProvider>
        <PluginsPage />
      </MantineProvider>,
    )

    expect(screen.getByText('插件管理')).toBeInTheDocument()
    expect(screen.getByText('R2T2 本地流式听写')).toBeInTheDocument()
    expect(screen.getByText('侧边栏外链集成')).toBeInTheDocument()
    expect(screen.getByText('Draw.io 架构与流程图')).toBeInTheDocument()
    expect(screen.getByText('Mermaid 架构图')).toBeInTheDocument()
    expect(screen.getByText('Excalidraw 白板')).toBeInTheDocument()
    expect(screen.getByText('代码变更比对 (Diff)')).toBeInTheDocument()
    expect(screen.getByText('3D 模型/CAD 预览')).toBeInTheDocument()
    expect(screen.getByText('HTML 页面预览')).toBeInTheDocument()
    expect(screen.getByText('Agent 决策状态机')).toBeInTheDocument()
  })

  it('persists plugin toggles to the backend database instead of localStorage', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
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

  it('starts the local voice runtime from its plugin switch', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
    const setPlugin = vi.spyOn(api, 'setLocalVoicePlugin').mockResolvedValue({
      ...disabledStatus, enabled: true, state: 'running', pid: 1234,
    })
    render(<MantineProvider><PluginsPage /></MantineProvider>)
    const toggle = await screen.findByRole('switch', { name: '启用 R2T2 本地流式听写' })
    await waitFor(() => expect(toggle).not.toBeDisabled())
    fireEvent.click(toggle)
    await waitFor(() => expect(setPlugin).toHaveBeenCalledWith(true))
    expect(await screen.findByText('流式听写已启用')).toBeInTheDocument()
  })

  it('organizes plugins by category tabs and marks replaceable database tool implementations', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({ enabled: {}, configs: {} })
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
    expect(screen.getByText('当前实现: DBX')).toBeInTheDocument()
    expect(screen.queryByText('Draw.io 架构与流程图')).toBeNull()

    // Expanding config shows "工具实现" selector with DBX as an option alongside custom web tool
    fireEvent.click(screen.getAllByRole('button', { name: /参数配置/ })[0])
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

    // Switch to "图表与白板" — shows Draw.io, hides Database Tool
    fireEvent.click(diagramTab)
    expect(screen.getByText('Draw.io 架构与流程图')).toBeInTheDocument()
    expect(screen.queryByText('数据库管理工具')).toBeNull()

    // Search filters plugins across categories
    fireEvent.click(screen.getByRole('tab', { name: /全部/ }))
    fireEvent.change(screen.getByRole('textbox', { name: '搜索插件' }), { target: { value: '视频' } })
    expect(screen.getByText('视频播放器')).toBeInTheDocument()
    expect(screen.queryByText('Draw.io 架构与流程图')).toBeNull()
  })
})
