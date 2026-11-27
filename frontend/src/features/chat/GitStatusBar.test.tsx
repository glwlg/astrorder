import { useEffect, useState } from 'react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Session } from '../../domain/types'
import userEvent from '@testing-library/user-event'
import { bootstrapBuiltinExtensions } from '../extensions/bootstrapBuiltins'
import { extensionRegistry } from '../extensions/extensionRegistry'
import type { StatusBarSlotContext } from '../extensions/types'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { GitStatusBar } from './GitStatusBar'

function registerTestGitLabSlot() {
  return extensionRegistry.registerBuiltin({
    pluginId: 'gitlab-ci-viewer',
    statusBarComponent: function TestGitLabStatusPill({
      session: s,
      statusUnavailable,
      statusLoading,
      branch,
    }: StatusBarSlotContext) {
      const [pipeline, setPipeline] = useState<{ id: number; status: string } | null>(null)
      useEffect(() => {
        if (statusUnavailable || statusLoading || !branch || !s.workspace) {
          setPipeline(null)
          return
        }
        let active = true
        const q = new URLSearchParams({ workspace: s.workspace, session_id: s.id })
        void fetch(`/api/v1/gitlab/pipelines?${q.toString()}`)
          .then((r) => r.json())
          .then((data: { matched?: boolean; latest_pipeline?: { id: number; status: string } }) => {
            if (!active) return
            setPipeline(data.matched && data.latest_pipeline ? data.latest_pipeline : null)
          })
          .catch(() => {
            if (active) setPipeline(null)
          })
        return () => {
          active = false
        }
      }, [s.workspace, s.id, statusUnavailable, statusLoading, branch])

      if (!pipeline) return null
      return (
        <button
          type="button"
          aria-label="查看 GitLab CI 流水线"
          onClick={() =>
            useSidecarStore.getState().openPluginPanel('gitlab-ci-viewer', 'ci-panel', {
              title: 'GitLab CI',
              mediaType: 'application/x-gitlab-ci',
              viewerId: 'gitlab-ci-viewer',
              sessionId: s.id,
              agentId: s.agent_id,
              workspacePath: s.workspace || undefined,
              connectionId: s.connection_id || undefined,
              params: { pipeline_id: pipeline.id },
            })
          }
        >
          <span>#{pipeline.id}</span>
          <span>{pipeline.status === 'running' ? '运行中' : '通过'}</span>
        </button>
      )
    },
    gitMenuItems: [
      {
        id: 'open-gitlab-ci',
        groupLabel: 'CI / CD',
        label: '查看 GitLab CI 流水线...',
        requiresWorkspace: true,
        onClick: (s) =>
          useSidecarStore.getState().openPluginPanel('gitlab-ci-viewer', 'ci-panel', {
            title: 'GitLab CI',
            mediaType: 'application/x-gitlab-ci',
            viewerId: 'gitlab-ci-viewer',
            sessionId: s.id,
            agentId: s.agent_id,
            workspacePath: s.workspace || undefined,
            connectionId: s.connection_id || undefined,
          }),
      },
    ],
  })
}

const session: Session = {
  id: 's1', agent_id: 'codex', title: 'Test', workspace: 'P:/workspace/project',
  status: 'idle', updated_at: '2026-09-28T00:00:00Z',
}

function renderStatus(value: Session = session) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<MantineProvider env="test"><QueryClientProvider client={client}><GitStatusBar session={value} /></QueryClientProvider></MantineProvider>)
}

beforeEach(() => {
  bootstrapBuiltinExtensions()
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
})

afterEach(() => {
  cleanup()
  extensionRegistry.clear()
  vi.unstubAllGlobals()
})

it('shows the file count from git status instead of saying the workspace is clean', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
    branch: 'master', branches: ['master'], changed_files: 3, insertions: 0, deletions: 0,
  }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
  renderStatus()

  expect(await screen.findByText('3 个文件变更')).toBeInTheDocument()
  expect(screen.queryByText('工作区无修改')).not.toBeInTheDocument()
  expect(screen.queryByText('+0')).not.toBeInTheDocument()
  expect(screen.queryByText('-0')).not.toBeInTheDocument()
})

it('keeps branch metadata unknown until Git returns it', async () => {
  let respond!: (value: Response) => void
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(resolve => { respond = resolve })))
  renderStatus()
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
  renderStatus({ ...session, connection_id: 'remote-test' })
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
  renderStatus()
  expect(await screen.findByText('工作区状态不可用')).toBeInTheDocument()
  expect(screen.queryByText('工作区无修改')).not.toBeInTheDocument()
})

it('does not carry a previous session branch into a different workspace', async () => {
  let finishSecond!: (value: Response) => void
  vi.stubGlobal('fetch', vi.fn((url: unknown) => {
    if (String(url).includes('session_id=s2')) return new Promise<Response>(resolve => { finishSecond = resolve })
    return Promise.resolve(new Response(JSON.stringify({ branch: 'master', branches: ['master'], changed_files: 0 }), { status: 200 }))
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(<MantineProvider env="test"><QueryClientProvider client={client}><GitStatusBar session={session} /></QueryClientProvider></MantineProvider>)
  expect(await screen.findByText('master')).toBeInTheDocument()

  view.rerender(<MantineProvider env="test"><QueryClientProvider client={client}><GitStatusBar session={{ ...session, id: 's2', workspace: '/other' }} /></QueryClientProvider></MantineProvider>)
  expect(screen.queryByText('master')).not.toBeInTheDocument()
  expect(screen.getByText('读取分支…')).toBeInTheDocument()
  finishSecond(new Response(JSON.stringify({ branch: 'other', branches: ['other'], changed_files: 0 }), { status: 200 }))
  expect(await screen.findByText('other')).toBeInTheDocument()
})

it('shows live GitLab CI pill for matched repos and opens the GitLab CI viewer on click', async () => {
  registerTestGitLabSlot()
  useSidecarStore.getState().resetSessionSidecar()
  vi.stubGlobal('fetch', vi.fn(async (url: unknown) => {
    const raw = String(url)
    if (raw.includes('/api/v1/gitlab/pipelines')) {
      return new Response(JSON.stringify({
        configured: true,
        matched: true,
        repo: { host: 'gitlab.internal', project_path: 'platform/opscore', branch: 'main', head_sha: 'abcdef123456' },
        latest_pipeline: {
          id: 402,
          status: 'running',
          ref: 'main',
          sha: 'abcdef123456',
          short_sha: 'abcdef12',
          web_url: 'https://gitlab.internal/platform/opscore/-/pipelines/402',
          matches_head: true,
        },
        pipelines: [],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }
    return new Response(JSON.stringify({ branch: 'main', branches: ['main'], changed_files: 0 }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }))

  renderStatus()
  const ciButton = await screen.findByRole('button', { name: '查看 GitLab CI 流水线' })
  expect(ciButton).toHaveTextContent('#402')
  expect(ciButton).toHaveTextContent('运行中')

  fireEvent.click(ciButton)
  const state = useSidecarStore.getState()
  expect(state.isOpen).toBe(true)
  expect(state.tabs.some((tab) => tab.viewerId === 'gitlab-ci-viewer' && tab.artifact?.readUrl === '?pipeline_id=402')).toBe(true)
})

it('renders extension-registered Git menu items and opens plugin panel via Sidecar', async () => {
  registerTestGitLabSlot()
  useSidecarStore.getState().resetSessionSidecar()
  vi.stubGlobal('fetch', vi.fn(async (url: unknown) => {
    const raw = String(url)
    if (raw.includes('/api/v1/gitlab/pipelines')) {
      return new Response(JSON.stringify({ configured: false, matched: false, pipelines: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    return new Response(JSON.stringify({ branch: 'main', branches: ['main'], changed_files: 0 }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }))

  const user = userEvent.setup()
  renderStatus()
  await waitFor(() => expect(screen.getByRole('button', { name: '切换或创建 Git 分支' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: '切换或创建 Git 分支' }))

  const menuItem = await screen.findByRole('menuitem', { name: /查看 GitLab CI 流水线/ })
  await user.click(menuItem)

  const state = useSidecarStore.getState()
  expect(state.isOpen).toBe(true)
  expect(state.tabs.some((tab) => tab.id === 'ext:gitlab-ci-viewer:ci-panel:s1' && tab.viewerId === 'gitlab-ci-viewer')).toBe(true)
})

it('switches branch, WT badge, diff stats, GitLab CI pill, and diff target when the same session switches to a worktree view', async () => {
  registerTestGitLabSlot()
  useSidecarStore.getState().resetSessionSidecar()
  const mainPath = '/home/luwei/workspace/OpsCore'
  const wtPath = '/home/luwei/workspace/OpsCore/.worktrees/security-drill-guide-v4'

  vi.stubGlobal('fetch', vi.fn(async (url: unknown) => {
    const raw = String(url)
    const isWt = raw.includes(encodeURIComponent(wtPath)) || raw.includes(wtPath)
    if (raw.includes('/api/v1/gitlab/pipelines')) {
      return new Response(JSON.stringify({
        configured: true,
        matched: true,
        repo: {
          host: 'gitlab.internal',
          project_path: 'platform/opscore',
          branch: isWt ? 'feat/security-drill-guide-v4' : 'master',
          head_sha: isWt ? '222222222222' : '111111111111',
        },
        latest_pipeline: {
          id: isWt ? 3201 : 3192,
          status: isWt ? 'success' : 'running',
          ref: isWt ? 'feat/security-drill-guide-v4' : 'master',
          sha: isWt ? '222222222222' : '111111111111',
          short_sha: isWt ? '22222222' : '11111111',
          web_url: `https://gitlab.internal/platform/opscore/-/pipelines/${isWt ? 3201 : 3192}`,
          matches_head: true,
        },
        pipelines: [],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }
    return new Response(JSON.stringify(
      isWt
        ? { branch: 'feat/security-drill-guide-v4', branches: ['feat/security-drill-guide-v4', 'master'], changed_files: 6, insertions: 240, deletions: 12 }
        : { branch: 'master', branches: ['master'], changed_files: 117, insertions: 8583, deletions: 1629 },
    ), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))

  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const mainSession: Session = { ...session, workspace: mainPath }
  const view = render(
    <MantineProvider env="test">
      <QueryClientProvider client={client}>
        <GitStatusBar session={mainSession} />
      </QueryClientProvider>
    </MantineProvider>,
  )

  expect(await screen.findByText('master')).toBeInTheDocument()
  expect(await screen.findByText('117 个文件变更')).toBeInTheDocument()
  expect(await screen.findByText('#3192')).toBeInTheDocument()
  expect(screen.queryByText('WT')).not.toBeInTheDocument()

  // Open diff tab while on main workspace so we verify switching updates the existing tab path too
  fireEvent.click(screen.getByRole('button', { name: '查看工作区 Git Diff' }))
  expect(useSidecarStore.getState().tabs.find((t) => t.viewerId === 'git-diff-viewer')?.artifact?.path).toBe(mainPath)

  // Switch the SAME session to the linked worktree view (as ChatPage passes activeWorktreeState.effectiveSession)
  const wtSession: Session = {
    ...mainSession,
    workspace: wtPath,
    worktree_root_workspace: mainPath,
    worktree_branch: 'feat/security-drill-guide-v4',
  }
  view.rerender(
    <MantineProvider env="test">
      <QueryClientProvider client={client}>
        <GitStatusBar session={wtSession} />
      </QueryClientProvider>
    </MantineProvider>,
  )

  expect(await screen.findByText('feat/security-drill-guide-v4')).toBeInTheDocument()
  expect(screen.getByText('WT')).toBeInTheDocument()
  expect(await screen.findByText('6 个文件变更')).toBeInTheDocument()
  expect(await screen.findByText('+240')).toBeInTheDocument()
  expect(await screen.findByText('-12')).toBeInTheDocument()
  expect(await screen.findByText('#3201')).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: '查看工作区 Git Diff' }))
  expect(useSidecarStore.getState().tabs.find((t) => t.viewerId === 'git-diff-viewer')?.artifact?.path).toBe(wtPath)
})
