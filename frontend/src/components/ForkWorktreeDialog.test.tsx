import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { vi, describe, it, expect, beforeEach, afterEach } from 'vitest'
import { ForkWorktreeDialog } from './ForkWorktreeDialog'
import { api } from '../api/client'
import type { Session } from '../domain/types'

const mockSession: Session = {
  id: 'session-123',
  agent_id: 'local-codex',
  title: '主线开发任务',
  status: 'idle',
  workspace: 'C:/repos/astrorder',
  updated_at: '2026-09-17T00:00:00Z',
}

describe('ForkWorktreeDialog', () => {
  beforeEach(() => {
    cleanup()
  })
  afterEach(() => {
    cleanup()
  })

  it('renders with pre-populated branch and linked worktree directory', () => {
    render(
      <MantineProvider>
        <ForkWorktreeDialog
          session={mockSession}
          onClose={vi.fn()}
          onCreated={vi.fn()}
        />
      </MantineProvider>,
    )

    expect(screen.getByRole('dialog', { name: '在新工作树中创建聊天分支' })).toBeInTheDocument()
    expect(screen.getByText(/源工作区：C:\/repos\/astrorder/)).toBeInTheDocument()

    const branchInput = screen.getByLabelText(/分支名称/) as HTMLInputElement
    const pathInput = screen.getByLabelText(/工作树目录/) as HTMLInputElement

    expect(branchInput.value).toMatch(/^branch-/)
    expect(pathInput.value).toContain('astrorder-worktrees')

    // Test dynamic update of path when branch changes
    fireEvent.change(branchInput, { target: { value: 'feature/dark-mode' } })
    expect(pathInput.value).toBe('C:/repos/astrorder-worktrees/feature/dark-mode')
  })

  it('submits fork request with worktree params and calls callbacks', async () => {
    const createdSession: Session = {
      id: 'session-forked',
      agent_id: 'local-codex',
      title: '主线开发任务 (feature/dark-mode)',
      status: 'idle',
      workspace: 'C:/repos/astrorder-worktrees/feature/dark-mode',
      updated_at: '2026-09-17T00:00:00Z',
    }

    const forkSpy = vi.spyOn(api, 'forkSession').mockResolvedValue(createdSession)
    const onClose = vi.fn()
    const onCreated = vi.fn()

    render(
      <MantineProvider>
        <ForkWorktreeDialog
          session={mockSession}
          onClose={onClose}
          onCreated={onCreated}
        />
      </MantineProvider>,
    )

    const branchInput = screen.getByLabelText(/分支名称/)
    fireEvent.change(branchInput, { target: { value: 'feature/dark-mode' } })

    const submitBtn = screen.getByRole('button', { name: '创建分支' })
    fireEvent.click(submitBtn)

    await waitFor(() => {
      expect(forkSpy).toHaveBeenCalledWith('session-123', {
        agent_id: 'local-codex',
        worktree: true,
        branch_name: 'feature/dark-mode',
        worktree_path: 'C:/repos/astrorder-worktrees/feature/dark-mode',
      })
    })

    expect(onCreated).toHaveBeenCalledWith(createdSession)
    expect(onClose).toHaveBeenCalled()

    forkSpy.mockRestore()
  })

  it('shows error notification when fork API fails', async () => {
    const forkSpy = vi.spyOn(api, 'forkSession').mockRejectedValue(new Error('当前工作区不是 Git 仓库'))
    const notifySpy = vi.spyOn(notifications, 'show')
    const onClose = vi.fn()

    render(
      <MantineProvider>
        <ForkWorktreeDialog
          session={mockSession}
          onClose={onClose}
          onCreated={vi.fn()}
        />
      </MantineProvider>,
    )

    const submitBtn = screen.getByRole('button', { name: '创建分支' })
    fireEvent.click(submitBtn)

    await waitFor(() => {
      expect(notifySpy).toHaveBeenCalledWith(
        expect.objectContaining({
          color: 'red',
          message: '当前工作区不是 Git 仓库',
        }),
      )
    })

    expect(onClose).not.toHaveBeenCalled()

    forkSpy.mockRestore()
    notifySpy.mockRestore()
  })
})
