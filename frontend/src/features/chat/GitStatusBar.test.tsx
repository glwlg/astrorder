import { MantineProvider } from '@mantine/core'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Session } from '../../domain/types'
import { GitStatusBar } from './GitStatusBar'

const session: Session = {
  id: 's1', agent_id: 'codex', title: 'Test', workspace: 'P:/workspace/project',
  status: 'idle', updated_at: '2026-09-28T00:00:00Z',
}

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

it('shows the file count from git status instead of saying the workspace is clean', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
    branch: 'master', branches: ['master'], changed_files: 3, insertions: 0, deletions: 0,
  }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
  render(<MantineProvider><GitStatusBar session={session} /></MantineProvider>)

  expect(await screen.findByText('个文件变更')).toBeInTheDocument()
  expect(screen.queryByText('工作区无修改')).not.toBeInTheDocument()
  expect(screen.queryByText('+0')).not.toBeInTheDocument()
  expect(screen.queryByText('-0')).not.toBeInTheDocument()
})

it('shows an unavailable workspace instead of a false clean status when Git rejects the path', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 422 })))
  render(<MantineProvider><GitStatusBar session={session} /></MantineProvider>)
  expect(await screen.findByText('工作区状态不可用')).toBeInTheDocument()
  expect(screen.queryByText('工作区无修改')).not.toBeInTheDocument()
})
