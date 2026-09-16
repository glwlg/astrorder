import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
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

function renderTree() {
  return render(
    <MantineProvider>
      <FileTreeViewer artifact={artifact} />
    </MantineProvider>,
  )
}

describe('FileTreeViewer expansion persistence', () => {
  const values = new Map<string, string>()

  afterEach(() => {
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

    fireEvent.click(folder.closest('.file-tree-row') as HTMLElement)
    await waitFor(() => expect(screen.getByText('index.ts')).toBeVisible())
    first.unmount()

    renderTree()
    await screen.findByText('src')
    await waitFor(() => expect(screen.getByText('index.ts')).toBeVisible())
  })
})
