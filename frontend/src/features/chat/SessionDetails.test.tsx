import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Agent, Command, Session } from '../../domain/types'
import { SessionDetails } from './SessionDetails'

vi.mock('../../components/SessionRuntimeFacts', () => ({
  SessionRuntimeFacts: () => <div>运行信息</div>,
}))
vi.mock('../../components/NativeObservationPanel', () => ({
  NativeObservationPanel: () => <div>原生观察</div>,
}))

const session: Session = {
  id: 'thread-1',
  agent_id: 'codex',
  title: 'Codex 会话',
  workspace: '/workspace/demo',
  status: 'error',
  updated_at: '2026-09-10T00:00:00Z',
}
const agent: Agent = {
  id: 'codex',
  kind: 'codex',
  name: 'Codex',
  status: 'ready',
  capabilities: ['chat', 'approvals', 'stop'],
  limitation: '这是一段不应该出现在会话详情里的静态限制文案',
}
const failedCommand: Command = {
  id: 'command-1',
  agent_id: 'codex',
  session_id: 'thread-1',
  action: 'send',
  state: 'failed',
  text: 'hello',
  attachments: [],
  created_at: '2026-09-10T00:00:00Z',
  error: 'Missing environment variable: OPENCODEX_API_AUTH_TOKEN.',
  target_id: null,
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('SessionDetails', () => {
  it('surfaces failed command error directly and omits capability wall, limitation text, and noisy activity list', () => {
    render(
      <MantineProvider>
        <SessionDetails
          session={session}
          agent={agent}
          commands={[failedCommand]}
          messages={[]}
          approvals={[]}
          onApproval={vi.fn()}
        />
      </MantineProvider>,
    )

    expect(screen.getByText('Missing environment variable: OPENCODEX_API_AUTH_TOKEN.')).toBeInTheDocument()
    expect(screen.getByText('Codex 会话')).toBeInTheDocument()
    expect(screen.getByText('/workspace/demo')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '终端' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '文件树' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '复制路径' })).toBeInTheDocument()
    expect(screen.queryByText('工具与命令活动')).not.toBeInTheDocument()
    expect(screen.queryByText('能力')).not.toBeInTheDocument()
    expect(screen.queryByText(/静态限制文案/)).not.toBeInTheDocument()
  })

  it('renders unified inspector header actions in drawer and pinned modes', () => {
    const onPin = vi.fn()
    const onUnpin = vi.fn()
    const onClose = vi.fn()

    const { rerender } = render(
      <MantineProvider>
        <SessionDetails
          session={session}
          agent={agent}
          commands={[]}
          messages={[]}
          approvals={[]}
          onApproval={vi.fn()}
          mode="drawer"
          onPin={onPin}
          onClose={onClose}
        />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '固定侧栏' }))
    expect(onPin).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: '关闭会话详情' }))
    expect(onClose).toHaveBeenCalledTimes(1)

    rerender(
      <MantineProvider>
        <SessionDetails
          session={session}
          agent={agent}
          commands={[]}
          messages={[]}
          approvals={[]}
          onApproval={vi.fn()}
          mode="pinned"
          onUnpin={onUnpin}
          onClose={onClose}
        />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '取消固定' }))
    expect(onUnpin).toHaveBeenCalledTimes(1)
  })
})
