import { describe, expect, it } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { afterEach } from 'vitest'
import { MarkdownContent } from './MarkdownContent'
import type { Session } from '../domain/types'
import { useSidecarStore } from '../features/sidecar/sidecarStore'

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

  it('automatically detects plain text path like artifacts/order-flow.mmd', () => {
    const text = '文件在 artifacts/order-flow.mmd 中'
    render(
      <MantineProvider>
        <MarkdownContent value={text} session={mockSession} />
      </MantineProvider>,
    )

    const btn = screen.getByRole('button', { name: /artifacts\/order-flow\.mmd/ })
    expect(btn).toBeInTheDocument()
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
