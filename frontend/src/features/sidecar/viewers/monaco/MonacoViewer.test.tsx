import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../../../domain/artifact'
import { MonacoViewer } from './MonacoViewer'

vi.mock('@monaco-editor/react', () => ({ default: ({ value, onChange }: { value: string; onChange: (value: string) => void }) => <textarea aria-label="测试代码编辑器" data-testid="monaco-editor" value={value} onChange={event => onChange(event.currentTarget.value)} /> }))

describe('MonacoViewer markdown mode', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it('saves only the active editor and retains a hidden draft', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('original', { status: 200 })))
    const artifact: ArtifactRef = { id: 'draft-file', sessionId: 'session-1', agentId: 'agent-1', name: 'draft.ts', kind: 'workspace_file', path: '/repo/draft.ts', readUrl: '/files/draft', mediaType: 'text/typescript', writable: true }
    const save = vi.fn(async () => true)
    const editor = (isActive: boolean) => <MantineProvider><MonacoViewer artifact={artifact} isActive={isActive} onSave={save} /></MantineProvider>
    const view = render(editor(true))
    await waitFor(() => expect(screen.getByLabelText('测试代码编辑器')).toHaveValue('original'))
    fireEvent.change(screen.getByLabelText('测试代码编辑器'), { target: { value: 'unsaved draft' } })
    view.rerender(editor(false))
    fireEvent.keyDown(window, { key: 's', ctrlKey: true })
    expect(save).not.toHaveBeenCalled()
    expect(screen.getByLabelText('测试代码编辑器')).toHaveValue('unsaved draft')
    view.rerender(editor(true))
    fireEvent.keyDown(window, { key: 's', ctrlKey: true })
    await waitFor(() => expect(save).toHaveBeenCalledExactlyOnceWith('unsaved draft'))
    expect(screen.queryByText('● 未保存')).not.toBeInTheDocument()
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

  it('rejects binary bytes even when the filename has no extension', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new Uint8Array([127, 69, 76, 70, 0, 1, 2, 3]))))
    const artifact: ArtifactRef = { id: 'binary', sessionId: 's', agentId: 'a', name: 'program', kind: 'workspace_file', path: '/repo/program', readUrl: '/files/program', mediaType: 'text/plain', writable: true }
    render(<MantineProvider><MonacoViewer artifact={artifact} /></MantineProvider>)
    expect(await screen.findByText('此文件包含二进制内容，无法作为文本打开')).toBeInTheDocument()
    expect(screen.queryByTestId('monaco-editor')).not.toBeInTheDocument()
  })

  it('reads UTF-16 text with a BOM without treating zero bytes as binary', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new Uint8Array([255, 254, 65, 0, 66, 0]))))
    const artifact: ArtifactRef = { id: 'utf16', sessionId: 's', agentId: 'a', name: 'notes.custom', kind: 'workspace_file', readUrl: '/files/notes', mediaType: 'text/plain', writable: true }
    render(<MantineProvider><MonacoViewer artifact={artifact} /></MantineProvider>)
    await waitFor(() => expect(screen.getByLabelText('测试代码编辑器')).toHaveValue('AB'))
  })
})
