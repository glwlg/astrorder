import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { PluginsPage } from './PluginsPage'
import { api, type LocalVoicePluginStatus } from '../../api/client'

const disabledStatus: LocalVoicePluginStatus = {
  id: 'r2t2-local-voice', name: 'R2T2 本地流式听写', enabled: false, state: 'disabled',
  available: true, progress: null, error: null, pid: null, device: 'Vulkan0',
  model: 'Confucius4-R2T2-Q8_0.gguf',
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('PluginsPage Component', () => {
  it('renders plugin management header and list of viewers', () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
    render(
      <MantineProvider>
        <PluginsPage />
      </MantineProvider>,
    )

    expect(screen.getByText('插件管理')).toBeInTheDocument()
    expect(screen.getByText('R2T2 本地流式听写')).toBeInTheDocument()
    expect(screen.getByText('Draw.io 架构与流程图')).toBeInTheDocument()
    expect(screen.getByText('Mermaid 架构图')).toBeInTheDocument()
    expect(screen.getByText('Excalidraw 白板')).toBeInTheDocument()
    expect(screen.getByText('代码变更比对 (Diff)')).toBeInTheDocument()
    expect(screen.getByText('3D 模型/CAD 预览')).toBeInTheDocument()
    expect(screen.getByText('HTML 页面预览')).toBeInTheDocument()
    expect(screen.getByText('Agent 决策状态机')).toBeInTheDocument()
  })

  it('starts the local voice runtime from its plugin switch', async () => {
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue(disabledStatus)
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
