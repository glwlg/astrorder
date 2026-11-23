import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Agent, Message, Session, SessionSchedule, SessionSwarm } from '../../domain/types'
import { WorkspaceChangesPanel } from './WorkspaceChangesPanel'
import { useSidecarStore } from '../sidecar/sidecarStore'

vi.mock('liquid-gl', () => ({ default: () => ({ destroy: vi.fn() }) }))

const session: Session = {
  id: 'coord-1',
  agent_id: 'local-antigravity',
  connection_id: null,
  title: 'Swarm 协调会话',
  workspace: '/home/user/project',
  status: 'idle',
  updated_at: '2026-11-22T10:00:00Z',
}

const agyAgent: Agent = {
  id: 'local-antigravity',
  kind: 'antigravity',
  name: 'Antigravity',
  status: 'ready',
  capabilities: ['chat', 'stop', 'history', 'events'],
  limitation: null,
}

const codexAgent: Agent = {
  id: 'local-codex',
  kind: 'codex',
  name: 'Codex',
  status: 'ready',
  capabilities: ['chat', 'stop', 'history', 'events'],
  limitation: null,
}

const sampleSwarm: SessionSwarm = {
  supported: true,
  available: true,
  active: true,
  kind: 'swarm',
  title: 'Auth Overhaul Team',
  coordinator_session_id: 'coord-1',
  current_session_id: 'coord-1',
  is_coordinator: true,
  summary: { total: 2, working: 1, waiting: 1, stopped: 0, killed: 0 },
  members: [
    {
      name: 'coordinator',
      role: '协调者',
      is_coordinator: true,
      conversation_id: 'coord-1',
      agent_id: 'local-antigravity',
      in_catalog: true,
      model: 'claude-sonnet-4-6',
      workspace_mode: 'inherit',
      state: 'waiting',
      last_message: 'Waiting for backend worker report.',
    },
    {
      name: 'backend',
      role: 'worker',
      is_coordinator: false,
      conversation_id: 'worker-1',
      parent_conversation_id: 'coord-1',
      agent_id: 'local-antigravity',
      in_catalog: true,
      model: 'gemini-3-pro',
      workspace_mode: 'share',
      state: 'working',
      brief: 'Update auth endpoints and tests.',
      last_message: 'Running unit tests now.',
    },
  ],
  board: [
    {
      id: 'b-1',
      kind: 'brief',
      author: 'coordinator',
      targets: ['backend'],
      text: 'Update auth endpoints and tests.',
      created_at: '2026-11-22T10:00:01Z',
    },
  ],
}

const clients: QueryClient[] = []
function show(opts: {
  messages?: Message[]
  session?: Session
  agent?: Agent
  swarm?: SessionSwarm
  schedules?: SessionSchedule[]
  onRefreshSwarm?: () => void
  onOpenSession?: (aid: string, sid: string) => void
  onClose?: () => void
} = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  clients.push(client)
  return render(
    <MantineProvider>
      <QueryClientProvider client={client}>
        <div className="chat-workbench">
          <WorkspaceChangesPanel
            session={opts.session ?? session}
            agent={opts.agent}
            messages={opts.messages ?? []}
            swarm={opts.swarm}
            schedules={opts.schedules}
            onRefreshSwarm={opts.onRefreshSwarm}
            onOpenSession={opts.onOpenSession}
            onClose={opts.onClose ?? vi.fn()}
          />
        </div>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('shows Antigravity Swarm roster, board, inline read-only member preview, and enter-session jump', async () => {
  const onOpenSession = vi.fn()
  const previewSpy = vi.spyOn(api, 'getSessionSwarm').mockResolvedValue({
    ...sampleSwarm,
    member_conversation_id: 'worker-1',
    member_messages: [
      {
        id: 'm-1',
        role: 'assistant',
        kind: 'message',
        text: 'Token refresh handler passed all unit tests.',
        created_at: '2026-11-22T10:02:00Z',
      },
    ],
  })

  show({ agent: agyAgent, swarm: sampleSwarm, onOpenSession })

  expect(screen.getByText('Auth Overhaul Team')).toBeInTheDocument()
  expect(screen.getByText('backend')).toBeInTheDocument()
  expect(screen.getByText('独立工作副本')).toBeInTheDocument()
  expect(screen.queryByText('工作区变更')).not.toBeInTheDocument()

  // Switch to shared board
  fireEvent.click(screen.getByRole('tab', { name: /共享消息板 1/ }))
  expect(screen.getByText('Update auth endpoints and tests.')).toBeInTheDocument()

  // Switch back to roster and click member for inline read-only preview
  fireEvent.click(screen.getByRole('tab', { name: /成员名册 2/ }))
  fireEvent.click(screen.getByText('backend'))

  expect(await screen.findByText('Token refresh handler passed all unit tests.')).toBeInTheDocument()
  expect(previewSpy).toHaveBeenCalledWith('coord-1', 'local-antigravity', {
    memberConversationId: 'worker-1',
    memberLimit: 24,
  })

  fireEvent.click(screen.getByRole('button', { name: /进入会话/ }))
  await waitFor(() => expect(onOpenSession).toHaveBeenCalledWith('local-antigravity', 'worker-1'))
})

it('for non-Antigravity sessions shows only explicit Agent outputs without empty Swarm or git tabs and closes on Escape', async () => {
  const onClose = vi.fn()
  const open = vi.spyOn(useSidecarStore.getState(), 'openArtifact')
  const message: Message = {
    id: 'm1',
    session_id: 'codex-1',
    agent_id: 'local-codex',
    role: 'assistant',
    kind: 'message',
    text: 'See /home/user/skills/SKILL.md',
    attachments: [{ id: 'report', name: 'report.png', media_type: 'image/png', url: '/report.png' }],
    command_id: null,
    tool: null,
    created_at: session.updated_at,
  }

  show({
    session: { ...session, id: 'codex-1', agent_id: 'local-codex' },
    agent: codexAgent,
    messages: [
      message,
      {
        ...message,
        id: 'user-msg',
        role: 'user',
        attachments: [{ id: 'ref', name: 'reference.png', media_type: 'image/png', url: '/ref.png' }],
      },
    ],
    onClose,
  })

  expect(screen.getByRole('heading', { name: '产物' })).toBeInTheDocument()
  expect(screen.queryByRole('radio', { name: /Swarm/ })).not.toBeInTheDocument()
  expect(screen.getByText('report.png')).toBeInTheDocument()
  expect(screen.queryByText('reference.png')).not.toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: /report\.png/ }))
  expect(open).toHaveBeenCalledOnce()
  expect(onClose).toHaveBeenCalledOnce()

  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(2))
})

it('shows daemon upgrade notice when small kernel lacks Swarm query support', () => {
  show({
    agent: agyAgent,
    swarm: {
      ...sampleSwarm,
      available: false,
      active: false,
      needs_daemon_upgrade: true,
      members: [],
      board: [],
    },
  })
  fireEvent.click(screen.getByRole('radio', { name: 'Swarm 0' }))
  expect(screen.getByText('需要更新小内核以读取 Swarm')).toBeInTheDocument()
})

it('manages session schedules inside the 定时 tab with live preview and immediate run', async () => {
  const previewSpy = vi.spyOn(api, 'previewSessionSchedule').mockResolvedValue({
    valid: true,
    timezone: 'Asia/Shanghai',
    next_runs: ['2026-11-23T01:00:00Z', '2026-11-24T01:00:00Z'],
  })
  const createSpy = vi.spyOn(api, 'createSessionSchedule').mockResolvedValue({
    id: 'sched-1',
    agent_id: 'local-antigravity',
    session_id: 'coord-1',
    name: '晨报巡检',
    prompt: '请总结今天构建日志',
    enabled: true,
    schedule_kind: 'daily',
    cron_expr: '',
    interval_minutes: 0,
    daily_time: '09:00',
    weekday_mask: 0,
    timezone: 'Asia/Shanghai',
    when_busy: 'skip',
    misfire_policy: 'grace_once',
    misfire_grace_sec: 900,
    next_run_at: '2026-11-23T01:00:00Z',
    last_status: '',
    last_error: '',
    last_command_id: '',
    consecutive_failures: 0,
    pause_reason: '',
    created_at: '2026-11-22T10:00:00Z',
    updated_at: '2026-11-22T10:00:00Z',
  })

  show({ agent: agyAgent, swarm: sampleSwarm, schedules: [] })

  fireEvent.click(screen.getByRole('radio', { name: '定时 0' }))
  expect(screen.getByText('当前会话还没有定时任务')).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: /新建第一个定时任务/ }))
  fireEvent.change(screen.getByPlaceholderText(/每日晨报巡检/), { target: { value: '晨报巡检' } })
  fireEvent.change(screen.getByPlaceholderText(/到时间后将作为一条消息/), { target: { value: '请总结今天构建日志' } })

  await waitFor(() => expect(previewSpy).toHaveBeenCalled())
  fireEvent.click(screen.getByRole('button', { name: '保存定时' }))
  await waitFor(() => expect(createSpy).toHaveBeenCalledWith('coord-1', expect.objectContaining({
    agent_id: 'local-antigravity',
    name: '晨报巡检',
    prompt: '请总结今天构建日志',
    schedule_kind: 'daily',
    daily_time: '09:00',
    when_busy: 'skip',
  })))
})
