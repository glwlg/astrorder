import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Session } from '../../domain/types'
import { ApprovalModeControl } from './ApprovalModeControl'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const testSession: Session = {
  id: 'session-mode-1',
  agent_id: 'codex',
  title: 'Test Session',
  workspace: null,
  status: 'idle',
  updated_at: '2026-01-01T00:00:00Z',
}

function renderControl(session = testSession) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <ApprovalModeControl session={session} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('ApprovalModeControl', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('renders default auto approval mode capsule button', async () => {
    vi.spyOn(api, 'getSessionApprovalMode').mockResolvedValue({ mode: 'auto' })
    renderControl()

    const trigger = screen.getByRole('button', { name: /当前审批模式：帮我批准/ })
    expect(trigger).toBeInTheDocument()
    expect(trigger).toHaveTextContent('帮我批准')
    expect(trigger).not.toHaveClass('is-full-access')
  })

  it('opens popover on click and shows the three approval mode options', async () => {
    vi.spyOn(api, 'getSessionApprovalMode').mockResolvedValue({ mode: 'auto' })
    renderControl()

    const trigger = screen.getByRole('button', { name: /当前审批模式：帮我批准/ })
    fireEvent.click(trigger)

    expect(await screen.findByText('应如何批准操作？')).toBeInTheDocument()
    expect(screen.getByText('控制执行系统命令与编辑文件时的权限等级')).toBeInTheDocument()

    // 选项 1: 请求批准
    expect(screen.getByText('请求批准')).toBeInTheDocument()
    expect(screen.getByText('编辑外部文件和使用互联网时始终询问')).toBeInTheDocument()

    // 选项 2: 帮我批准
    expect(screen.getAllByText('帮我批准')).toHaveLength(2)
    expect(screen.getByText('仅对检测到的风险操作请求批准')).toBeInTheDocument()

    // 选项 3: 完全访问权限
    expect(screen.getByText('完全访问权限')).toBeInTheDocument()
    expect(screen.getByText('可不受限制地访问互联网和你电脑上的任何文件')).toBeInTheDocument()
  })

  it('switches to full_access mode and shows warning styling', async () => {
    vi.spyOn(api, 'getSessionApprovalMode').mockResolvedValue({ mode: 'auto' })
    const setSpy = vi.spyOn(api, 'setSessionApprovalMode').mockResolvedValue({ mode: 'full_access' })

    renderControl()
    const trigger = screen.getByRole('button', { name: /当前审批模式：帮我批准/ })
    fireEvent.click(trigger)

    const fullOption = await screen.findByText('完全访问权限')
    fireEvent.click(fullOption)

    await waitFor(() => {
      expect(setSpy).toHaveBeenCalledWith(testSession.id, testSession.agent_id, 'full_access')
    })

    await waitFor(() => {
      expect(screen.getByText('完全访问')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /当前审批模式：完全访问权限/ })).toHaveClass('is-full-access')
    })
  })

  it('switches to manual approval mode', async () => {
    vi.spyOn(api, 'getSessionApprovalMode').mockResolvedValue({ mode: 'auto' })
    const setSpy = vi.spyOn(api, 'setSessionApprovalMode').mockResolvedValue({ mode: 'manual' })

    renderControl()
    const trigger = screen.getByRole('button', { name: /当前审批模式：帮我批准/ })
    fireEvent.click(trigger)

    const manualOption = await screen.findByText('请求批准')
    fireEvent.click(manualOption)

    await waitFor(() => {
      expect(setSpy).toHaveBeenCalledWith(testSession.id, testSession.agent_id, 'manual')
    })

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /当前审批模式：请求批准/ })).toBeInTheDocument()
    })
  })
})
