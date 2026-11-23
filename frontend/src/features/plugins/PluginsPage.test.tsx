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
})
