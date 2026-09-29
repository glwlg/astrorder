import { describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { afterEach } from 'vitest'
import { MarkdownContent } from './MarkdownContent'
import type { Session } from '../domain/types'
import { useSidecarStore } from '../features/sidecar/sidecarStore'
import { api } from '../api/client'

const mockSession: Session = {
  id: 'session-links',
  agent_id: 'local-hermes',
  title: '测试会话',
  workspace: 'C:/workspace/test',
  status: 'idle',
  updated_at: '2026-09-10T12:00:00Z',
}

describe('MarkdownContent Links Handling', () => {
  afterEach(() => {
    cleanup()
    useSidecarStore.setState({ isOpen: false, activeTabId: '', tabs: [], activeSessionKey: null, sessionMemories: {} })
  })
  it('intercepts http artifact url and does not render raw refreshable link', () => {
    const text = '访问 [order-flow.excalidraw](https://ao.651971564.xyz/abs/path/artifacts/order-flow.excalidraw)'
    render(
      <MantineProvider>
        <MarkdownContent value={text} session={mockSession} />
      </MantineProvider>,
    )

    const btn = screen.getByRole('button', { name: /order-flow\.excalidraw/ })
    expect(btn).toBeInTheDocument()
    expect(btn).toHaveClass('markdown-file-link')
  })

  it('automatically detects plain text path like artifacts/order-flow.mmd', async () => {
    vi.spyOn(api, 'checkWorkspaceFiles').mockResolvedValue({ existing: ['artifacts/order-flow.mmd'] })
    const text = '文件在 artifacts/order-flow.mmd 中'
    render(
      <MantineProvider>
        <MarkdownContent value={text} session={mockSession} />
      </MantineProvider>,
    )

    const btn = await screen.findByRole('button', { name: /artifacts\/order-flow\.mmd/ })
    expect(btn).toBeInTheDocument()
    vi.restoreAllMocks()
  })

  it('links the longest existing relative path after prose, including spaces in filenames', async () => {
    vi.spyOn(api, 'checkWorkspaceFiles').mockResolvedValue({
      existing: ['output/周报_20260929.html', 'output/季度 周报.html'],
    })
    render(
      <MantineProvider>
        <MarkdownContent
          value="将 output/周报_20260929.html 处理好；再看 output/季度 周报.html"
          session={mockSession}
        />
      </MantineProvider>,
    )

    const first = await screen.findByRole('button', { name: /output\/周报_20260929\.html/ })
    expect(first.textContent).toContain('output/周报_20260929.html')
    expect(first.textContent).not.toContain('将')
    expect(screen.getByRole('button', { name: /output\/季度 周报\.html/ }).textContent)
      .toContain('output/季度 周报.html')
    expect(screen.getByText(/将/)).toBeInTheDocument()
    vi.restoreAllMocks()
  })

  it('locates executable links in the file tree instead of opening an editor', () => {
    render(
      <MantineProvider>
        <MarkdownContent value="[安装程序](dist/setup.exe)" session={mockSession} />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: /安装程序/ }))
    const state = useSidecarStore.getState()
    expect(state.activeTabId).toBe(`filetree:${mockSession.id}`)
    expect(state.tabs[0].artifact?.metadata?.revealPath).toBe('C:/workspace/test/dist/setup.exe')
  })
})
