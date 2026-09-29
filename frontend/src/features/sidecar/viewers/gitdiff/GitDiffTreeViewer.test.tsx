import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../../../domain/artifact'
import { GitDiffTreeViewer } from './GitDiffTreeViewer'

const artifact: ArtifactRef = {
  id: 'gitdifftree:session:working-tree',
  sessionId: 'session', agentId: 'agent', name: '审查', kind: 'workspace_file',
  mediaType: 'application/x-git-diff-tree', readUrl: '', writable: false,
  path: 'P:/workspace/example',
}

describe('GitDiffTreeViewer', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
  })

  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it('renders compact status marks without painting status words over file icons in both views', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      files: [{ path: 'src/example.py', status: 'modified' }],
      insertions: 4, deletions: 2,
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    render(<MantineProvider><GitDiffTreeViewer artifact={artifact} /></MantineProvider>)

    const file = await screen.findByText('example.py')
    const treeRow = file.closest('button') as HTMLElement
    expect(treeRow.querySelector('.git-diff-status')).toHaveTextContent('M')
    expect(treeRow.querySelector('.git-diff-status')).toHaveAttribute('title', '已修改')
    expect(treeRow.querySelector('svg')).toBeInTheDocument()
    expect(screen.getByText('+4')).toBeInTheDocument()
    expect(screen.getByText('-2')).toBeInTheDocument()

    fireEvent.click(screen.getByText('列表'))
    const listRow = screen.getByText('example.py').closest('button') as HTMLElement
    expect(listRow.querySelector('.git-diff-status')).toHaveTextContent('M')
    expect(listRow.querySelector('.git-diff-status')).toHaveAttribute('title', '已修改')
    expect(listRow.querySelector('svg')).toBeInTheDocument()
  })

  it('shows untracked files without claiming zero changed lines', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      files: [{ path: 'new.txt', status: 'untracked' }],
      insertions: 0, deletions: 0,
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    render(<MantineProvider><GitDiffTreeViewer artifact={artifact} /></MantineProvider>)

    expect(await screen.findByText('new.txt')).toBeInTheDocument()
    expect(screen.getByText('工作区变更 (1)')).toBeInTheDocument()
    expect(screen.getByText('未跟踪 1')).toBeInTheDocument()
    expect(screen.queryByText('+0')).not.toBeInTheDocument()
    expect(screen.queryByText('-0')).not.toBeInTheDocument()
  })
})
