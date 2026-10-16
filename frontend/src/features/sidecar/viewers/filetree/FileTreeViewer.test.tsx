import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
  return render(
    <MantineProvider>
      <FileTreeViewer artifact={target} />
    </MantineProvider>,
  )
}

describe('FileTreeViewer file actions', () => {
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop })

  it.each(['local', 'ssh-fixture'])('copies %s files through the native clipboard after staging', async connectionId => {
    const setClipboardFiles = vi.fn().mockResolvedValue(true)
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { setClipboardFiles }
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (url.includes('/stage')) {
        const body = JSON.parse(String(init?.body))
        if (body.copy_to_clipboard !== false) return new Response(JSON.stringify({ detail: 'File clipboard is unavailable' }), { status: 503 })
        return new Response(JSON.stringify({ local_paths: ['C:/staged/file.txt'], clipboard_set: false }))
      }
      return new Response(JSON.stringify({ root: artifact.path, items: [{ name: 'file.txt', path: `${artifact.path}/file.txt`, is_dir: false }] }))
    })
    vi.stubGlobal('fetch', fetchMock)
    renderTree({ ...artifact, connectionId })
    fireEvent.contextMenu(await screen.findByText('file.txt'))
    fireEvent.click(await screen.findByText('复制文件 (Ctrl+C)'))
    await waitFor(() => expect(setClipboardFiles).toHaveBeenCalledWith(['C:/staged/file.txt']))
  })

  it('downloads authenticated bytes without opening a popup', async () => {
    const saveDownload = vi.fn().mockResolvedValue(true)
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { saveDownload }
    const popup = vi.spyOn(window, 'open').mockReturnValue(null)
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ root: artifact.path, items: [{ name: 'file.txt', path: `${artifact.path}/file.txt`, is_dir: false }] }))))
    renderTree()
    fireEvent.contextMenu(await screen.findByText('file.txt'))
    fireEvent.click(await screen.findByText('下载 / 另存为'))
    await waitFor(() => expect(saveDownload).toHaveBeenCalledWith(expect.stringContaining('/api/v1/files/raw?'), 'file.txt'))
    expect(popup).not.toHaveBeenCalled()
  })
})

describe('FileTreeViewer expansion persistence', () => {
  const values = new Map<string, string>()

  beforeEach(() => {
    // Expansion state is persisted; a previous run must not pre-expand folders.
    localStorage.clear()
  })

  afterEach(() => {
    cleanup()
    values.clear()
    vi.unstubAllGlobals()
  })

  it('authenticates mobile tree reads with the stored browser credential', async () => {
    localStorage.setItem('astrorder:token', 'fixture-mobile-tree')
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ root: artifact.path, items: [] }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    renderTree()
    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(init?.credentials).toBe('include')
    expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer fixture-mobile-tree')
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
    const row = file.closest('.file-tree-row') as HTMLElement
    expect(row).toHaveStyle({ outline: '1px solid var(--astr-blue, #3b82f6)' })
    expect(row).toHaveClass('is-selected')
    expect(row).toHaveClass('is-revealed')
    expect(fetch).toHaveBeenCalledWith(expect.stringContaining(`reveal_path=${encodeURIComponent(revealPath)}`), expect.objectContaining({ credentials: 'include' }))
    view.unmount()
  })

  it('selects and reveals requested file even when paths differ in slashes (Windows vs POSIX)', async () => {
    // revealPath 为 POSIX 正斜杠，后端返回的节点为 Windows 反斜杠
    const revealPath = 'P:/workspace/example/local-prod/ELK/logstash-pipelines/logstash-zhiyuan-portal.conf'
    const windowsNodePath = 'P:\\workspace\\example\\local-prod\\ELK\\logstash-pipelines\\logstash-zhiyuan-portal.conf'
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      root: artifact.path,
      items: [{
        name: 'local-prod', path: 'P:\\workspace\\example\\local-prod', is_dir: true, children: [{
          name: 'ELK', path: 'P:\\workspace\\example\\local-prod\\ELK', is_dir: true, children: [{
            name: 'logstash-pipelines', path: 'P:\\workspace\\example\\local-prod\\ELK\\logstash-pipelines', is_dir: true, children: [
              { name: 'logstash-zhiyuan-portal.conf', path: windowsNodePath, is_dir: false },
            ],
          }],
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

    const file = await screen.findByText('logstash-zhiyuan-portal.conf')
    await waitFor(() => expect(file).toBeVisible())
    const row = file.closest('.file-tree-row') as HTMLElement
    expect(row).toHaveClass('is-selected')
    expect(row).toHaveClass('is-revealed')
    expect(row).toHaveStyle({ outline: '1px solid var(--astr-blue, #3b82f6)' })
    view.unmount()
  })

  it('shows the server reason next to the status when the tree cannot be loaded', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ detail: 'Failed to inspect directory entry' }), { status: 500, headers: { 'Content-Type': 'application/json' } })))
    const view = renderTree()
    expect(await screen.findByText('获取文件树失败 (500)：Failed to inspect directory entry')).toBeInTheDocument()
    view.unmount()
  })

  it('still reports the status when the error body is not JSON', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('bad gateway', { status: 502 })))
    const view = renderTree()
    expect(await screen.findByText('获取文件树失败 (502)')).toBeInTheDocument()
    view.unmount()
  })

  it('loads a directory the server left unexpanded when it is opened', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      calls.push(url)
      const body = url.includes('depth=2')
        ? { root: 'P:/workspace/example/big', items: [{ name: 'late.ts', path: 'P:/workspace/example/big/late.ts', is_dir: false }] }
        : { root: artifact.path, truncated: true, items: [{ name: 'big', path: 'P:/workspace/example/big', is_dir: true }] }
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }))
    const view = renderTree()
    const folder = await screen.findByText('big')
    expect(calls).toHaveLength(1)

    fireEvent.doubleClick(folder.closest('.file-tree-row') as HTMLElement)
    expect(await screen.findByText('late.ts')).toBeInTheDocument()
    expect(calls[1]).toContain(`path=${encodeURIComponent('P:/workspace/example/big')}`)
    expect(calls).toHaveLength(2)
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
