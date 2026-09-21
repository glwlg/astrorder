import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../../../domain/artifact'
import { MonacoViewer } from './MonacoViewer'

vi.mock('@monaco-editor/react', () => ({ default: () => <div data-testid="monaco-editor" /> }))

describe('MonacoViewer markdown mode', () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it('opens markdown in reading mode and can switch to editing', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('# 阅读标题', { status: 200 })))
    const artifact: ArtifactRef = {
      id: 'markdown-file', sessionId: 'session-1', agentId: 'agent-1', name: 'README.md',
      kind: 'workspace_file', path: '/repo/README.md', readUrl: '/files/readme',
      mediaType: 'text/markdown', writable: true,
    }

    render(<MantineProvider><MonacoViewer artifact={artifact} /></MantineProvider>)

    expect(await screen.findByRole('heading', { name: '阅读标题' })).toBeInTheDocument()
    expect(screen.queryByTestId('monaco-editor')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '编辑 Markdown' }))
    expect(screen.getByTestId('monaco-editor')).toBeInTheDocument()
  })
})
