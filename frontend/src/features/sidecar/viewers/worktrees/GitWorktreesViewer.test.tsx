import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../../../api/client'
import type { Session } from '../../../../domain/types'
import { GitWorktreesViewer } from './GitWorktreesViewer'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const sampleSession: Session = {
  id: 'sess-1',
  agent_id: 'agy',
  title: '主线任务',
  workspace: '/repos/demo',
  status: 'idle',
  updated_at: '2026-11-25T00:00:00Z',
}

function renderViewer() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MemoryRouter>
      <MantineProvider>
        <QueryClientProvider client={client}>
          <GitWorktreesViewer
            session={sampleSession}
            artifact={{
              id: 'gitworktrees:sess-1',
              name: 'Worktree 工作区',
              kind: 'workspace_file',
              mediaType: 'application/x-git-worktrees',
              readUrl: '',
              writable: false,
              path: '/repos/demo',
              sessionId: 'sess-1',
              agentId: 'agy',
            }}
          />
        </QueryClientProvider>
      </MantineProvider>
    </MemoryRouter>,
  )
}

describe('GitWorktreesViewer', () => {
  it('renders worktree statuses, guards dirty removal, and handles safe merge conflicts', async () => {
    vi.spyOn(api, 'listGitWorktrees').mockResolvedValue({
      is_git_repo: true,
      workspace: '/repos/demo',
      main_workspace: '/repos/demo',
      is_worktree: false,
      current_branch: 'main',
      default_branch: 'main',
      default_sibling_base: '/repos/demo-worktrees',
      default_inside_base: '/repos/demo/.worktrees',
      worktrees: [
        {
          path: '/repos/demo',
          head: '1111111',
          branch: 'main',
          detached: false,
          bare: false,
          is_main: true,
          is_current: true,
          locked: false,
          prunable: false,
          status_loaded: true,
          dirty: false,
          changed_files: 0,
          ahead: 0,
          behind: 0,
        },
        {
          path: '/repos/demo-worktrees/feat/payments',
          head: '2222222',
          branch: 'feat/payments',
          detached: false,
          bare: false,
          is_main: false,
          is_current: false,
          locked: false,
          prunable: false,
          status_loaded: true,
          dirty: true,
          changed_files: 3,
          ahead: 2,
          behind: 1,
        },
      ],
      branches: [
        { name: 'main', is_current: true, occupied_by_path: '/repos/demo' },
        { name: 'feat/payments', is_current: false, occupied_by_path: '/repos/demo-worktrees/feat/payments' },
      ],
      copyable_files: [{ path: '.env', size: 64, source: 'default' }],
    })

    const mergeSpy = vi.spyOn(api, 'mergeGitWorktree').mockResolvedValue({
      merged: false,
      conflicted: true,
      aborted: true,
      source_branch: 'feat/payments',
      target_branch: 'main',
      target_path: '/repos/demo',
      strategy: 'merge',
      conflict_files: ['src/billing.ts'],
      detail: 'CONFLICT (content): Merge conflict in src/billing.ts',
    })

    const quotedEvents: CustomEvent[] = []
    const listener = (evt: Event) => { quotedEvents.push(evt as CustomEvent) }
    window.addEventListener('astrorder:quote-to-composer', listener)

    try {
      renderViewer()

      expect(await screen.findByText('feat/payments')).toBeInTheDocument()
      expect(screen.getByText('3 处修改')).toBeInTheDocument()
      expect(screen.getByText('↑2 ↓1')).toBeInTheDocument()

      // Open safe merge dialog and trigger conflict
      fireEvent.click(screen.getByRole('button', { name: '合并' }))
      fireEvent.click(await screen.findByRole('button', { name: '执行合并' }))

      await waitFor(() => expect(mergeSpy).toHaveBeenCalledWith(expect.objectContaining({
        source_branch: 'feat/payments',
        target_branch: 'main',
        strategy: 'merge',
      })))
      expect(await screen.findByText(/检测到合并冲突/)).toBeInTheDocument()
      expect(screen.getByText('• src/billing.ts')).toBeInTheDocument()

      // Send conflict resolution prompt to composer
      fireEvent.click(screen.getByRole('button', { name: /将冲突说明填入当前会话/ }))
      expect(quotedEvents).toHaveLength(1)
      expect(quotedEvents[0].detail.text).toContain('feat/payments')
      expect(quotedEvents[0].detail.text).toContain('src/billing.ts')
    } finally {
      window.removeEventListener('astrorder:quote-to-composer', listener)
    }
  })
})
