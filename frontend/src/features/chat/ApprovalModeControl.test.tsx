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

  it('offers Antigravity native rules and full access without unsupported auto review', async () => {
    vi.spyOn(api, 'getSessionApprovalMode').mockResolvedValue({ mode: 'manual' })
    const setSpy = vi.spyOn(api, 'setSessionApprovalMode').mockResolvedValue({ mode: 'full_access' })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<MantineProvider><QueryClientProvider client={queryClient}><ApprovalModeControl session={testSession} agentKind="antigravity" /></QueryClientProvider></MantineProvider>)
    fireEvent.click(await screen.findByRole('button', { name: /当前审批模式：按原生规则/ }))
    expect(await screen.findByText(/需要批准的操作会被拒绝/)).toBeInTheDocument()
    expect(screen.queryByText('帮我批准')).not.toBeInTheDocument()
    fireEvent.click(screen.getByText('完全访问权限'))
    await waitFor(() => expect(setSpy).toHaveBeenCalledWith(testSession.id, testSession.agent_id, 'full_access'))
    expect(await screen.findByRole('button', { name: /当前审批模式：完全访问权限/ })).toBeInTheDocument()
  })

  it('does not query or offer unsupported approval modes', () => {
    const getSpy = vi.spyOn(api, 'getSessionApprovalMode')
    const queryClient = new QueryClient()
    render(<MantineProvider><QueryClientProvider client={queryClient}><ApprovalModeControl session={testSession} supported={false} /></QueryClientProvider></MantineProvider>)
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(getSpy).not.toHaveBeenCalled()
  })

  it('does not present a cached mode as confirmed when the native read fails', async () => {
    localStorage.setItem(`astrorder_approval_mode:${testSession.agent_id}:${testSession.id}`, 'full_access')
    vi.spyOn(api, 'getSessionApprovalMode').mockRejectedValue(new Error('native unavailable'))
    renderControl()
    expect(await screen.findByRole('button', { name: '审批模式读取失败' })).toBeDisabled()
    expect(screen.queryByText('完全访问')).not.toBeInTheDocument()
  })

  it('keeps the confirmed mode when a change fails', async () => {
    vi.spyOn(api, 'getSessionApprovalMode').mockResolvedValue({ mode: 'manual' })
    vi.spyOn(api, 'setSessionApprovalMode').mockRejectedValue(new Error('native rejected'))
    renderControl()
    fireEvent.click(await screen.findByRole('button', { name: /当前审批模式：请求批准/ }))
    fireEvent.click(await screen.findByText('完全访问权限'))
    await waitFor(() => expect(api.setSessionApprovalMode).toHaveBeenCalled())
    expect(screen.getByRole('button', { name: /当前审批模式：请求批准/ })).not.toHaveClass('is-full-access')
    expect(localStorage.getItem(`astrorder_approval_mode:${testSession.agent_id}:${testSession.id}`)).not.toBe('full_access')
  })

  it('renders default auto approval mode capsule button', async () => {
    vi.spyOn(api, 'getSessionApprovalMode').mockResolvedValue({ mode: 'auto' })
    renderControl()

    const trigger = await screen.findByRole('button', { name: /当前审批模式：帮我批准/ })
    expect(trigger).toBeInTheDocument()
    expect(trigger).toHaveTextContent('帮我批准')
    expect(trigger).not.toHaveClass('is-full-access')
  })

  it('opens popover on click and shows the three approval mode options', async () => {
    vi.spyOn(api, 'getSessionApprovalMode').mockResolvedValue({ mode: 'auto' })
    renderControl()

    const trigger = await screen.findByRole('button', { name: /当前审批模式：帮我批准/ })
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
    const trigger = await screen.findByRole('button', { name: /当前审批模式：帮我批准/ })
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
    const trigger = await screen.findByRole('button', { name: /当前审批模式：帮我批准/ })
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
