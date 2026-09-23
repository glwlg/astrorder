import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../../../domain/artifact'
import { FileTreeViewer } from './FileTreeViewer'

const artifact: ArtifactRef = {
  id: 'filetree:session-tree',
  name: '工作区文件',
  kind: 'workspace_file',
  mediaType: 'application/x-directory',
  readUrl: '',
  writable: false,
  path: 'P:/workspace/example',
  sessionId: 'session-tree',
  agentId: 'agent-tree',
}

function renderTree(target = artifact) {
  return render(
    <MantineProvider>
      <FileTreeViewer artifact={target} />
    </MantineProvider>,
  )
}

describe('FileTreeViewer expansion persistence', () => {
  const values = new Map<string, string>()

  afterEach(() => {
    cleanup()
    values.clear()
    vi.unstubAllGlobals()
  })

  it('starts collapsed and permanently restores expanded folders', async () => {
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
      clear: () => values.clear(),
    })
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      root: artifact.path,
      items: [{
        name: 'src',
        path: 'P:/workspace/example/src',
        is_dir: true,
        children: [{ name: 'index.ts', path: 'P:/workspace/example/src/index.ts', is_dir: false }],
      }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))

    const first = renderTree()
    const folder = await screen.findByText('src')
    await waitFor(() => expect(screen.getByText('index.ts')).not.toBeVisible())

    fireEvent.doubleClick(folder.closest('.file-tree-row') as HTMLElement)
    await waitFor(() => expect(screen.getByText('index.ts')).toBeVisible())
    first.unmount()

    renderTree()
    await screen.findByText('src')
    await waitFor(() => expect(screen.getByText('index.ts')).toBeVisible())
  })

  it('expands and selects a requested file', async () => {
    const revealPath = 'P:/workspace/example/src/deep/index.ts'
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      root: artifact.path,
      items: [{
        name: 'src', path: 'P:/workspace/example/src', is_dir: true, children: [{
          name: 'deep', path: 'P:/workspace/example/src/deep', is_dir: true, children: [
            { name: 'index.ts', path: revealPath, is_dir: false },
          ],
        }],
      }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    Element.prototype.scrollIntoView = vi.fn()
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })

    const view = renderTree({ ...artifact, metadata: { revealPath, revealAt: 1 } })

    const file = await screen.findByText('index.ts')
    await waitFor(() => expect(file).toBeVisible())
    expect(file.closest('.file-tree-row')).toHaveStyle({ outline: '1px solid var(--astr-blue, #3b82f6)' })
    expect(fetch).toHaveBeenCalledWith(expect.stringContaining(`reveal_path=${encodeURIComponent(revealPath)}`))
    view.unmount()
  })

  it('opens context menu on right click with full and relative path copy options', async () => {
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      root: artifact.path,
      items: [{
        name: 'src',
        path: 'P:/workspace/example/src',
        is_dir: false,
      }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))

    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', {
      clipboard: { writeText },
    })

    const view = renderTree()
    const item = await screen.findByText('src')
    const row = item.closest('.file-tree-row') as HTMLElement

    fireEvent.contextMenu(row, { clientX: 120, clientY: 240 })

    expect(await screen.findByText('在资源管理器中打开')).toBeInTheDocument()
    expect(screen.getByText('复制全路径')).toBeInTheDocument()
    expect(screen.getByText('复制相对路径')).toBeInTheDocument()

    fireEvent.click(screen.getByText('复制相对路径'))
    expect(writeText).toHaveBeenCalledWith('src')

    view.unmount()
  })
})
