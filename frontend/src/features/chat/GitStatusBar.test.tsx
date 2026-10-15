import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Session } from '../../domain/types'
import userEvent from '@testing-library/user-event'
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
  render(<MantineProvider env="test"><GitStatusBar session={session} /></MantineProvider>)

  expect(await screen.findByText('个文件变更')).toBeInTheDocument()
  expect(screen.queryByText('工作区无修改')).not.toBeInTheDocument()
  expect(screen.queryByText('+0')).not.toBeInTheDocument()
  expect(screen.queryByText('-0')).not.toBeInTheDocument()
})

it('keeps branch metadata unknown until Git returns it', async () => {
  let respond!: (value: Response) => void
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(resolve => { respond = resolve })))
  render(<MantineProvider env="test"><GitStatusBar session={session} /></MantineProvider>)
  expect(screen.queryByText('master')).not.toBeInTheDocument()
  respond(new Response(JSON.stringify({ branch: 'feature/mobile', branches: ['feature/mobile'], changed_files: 0 }), { status: 200 }))
  expect(await screen.findByText('feature/mobile')).toBeInTheDocument()
})

it('switches and creates branches for the exact session and verifies Git status', async () => {
  let branch = 'feature/mobile'
  const fetcher = vi.fn(async (_url: unknown, init?: RequestInit) => {
    if (init?.method === 'POST') branch = JSON.parse(String(init.body)).branch
    return new Response(JSON.stringify({ branch, branches: ['feature/mobile', 'review', branch], changed_files: 0 }), { status: 200 })
  })
  vi.stubGlobal('fetch', fetcher)
  const user = userEvent.setup()
  render(<MantineProvider env="test"><GitStatusBar session={{ ...session, connection_id: 'remote-test' }} /></MantineProvider>)
  await waitFor(() => expect(screen.getByRole('button', { name: '切换或创建 Git 分支' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: '切换或创建 Git 分支' }))
  await user.click(await screen.findByRole('menuitem', { name: 'review' }))
  await waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/git/branch', expect.objectContaining({ method: 'POST', body: JSON.stringify({ branch: 'review', create: false, workspace: session.workspace, session_id: session.id, connection_id: 'remote-test' }) })))
  await waitFor(() => expect(screen.getByRole('button', { name: '切换或创建 Git 分支' })).toHaveTextContent('review'))
  await waitFor(() => expect(document.querySelector('.mantine-Menu-dropdown')).toBeNull())
  await waitFor(() => expect(screen.getByRole('button', { name: '切换或创建 Git 分支' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: '切换或创建 Git 分支' }))
  await user.click(await screen.findByRole('menuitem', { name: /基于当前创建新分支/ }))
  const dialog = await screen.findByRole('dialog', { name: '创建并切换到新分支' })
  fireEvent.change(within(dialog).getByLabelText('新分支名称'), { target: { value: 'feature/new' } })
  fireEvent.click(within(dialog).getByRole('button', { name: '创建并切换' }))
  await waitFor(() => expect(fetcher).toHaveBeenCalledWith('/api/v1/git/branch', expect.objectContaining({ method: 'POST', body: JSON.stringify({ branch: 'feature/new', create: true, workspace: session.workspace, session_id: session.id, connection_id: 'remote-test' }) })))
  await waitFor(() => expect(screen.getByRole('button', { name: '切换或创建 Git 分支' })).toHaveTextContent('feature/new'))
})

it('shows an unavailable workspace instead of a false clean status when Git rejects the path', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 422 })))
  render(<MantineProvider env="test"><GitStatusBar session={session} /></MantineProvider>)
  expect(await screen.findByText('工作区状态不可用')).toBeInTheDocument()
  expect(screen.queryByText('工作区无修改')).not.toBeInTheDocument()
})
