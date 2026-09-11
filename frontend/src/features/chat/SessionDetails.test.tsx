import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
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
  workspace: '/workspace',
  status: 'error',
  updated_at: '2026-09-10T00:00:00Z',
}
const agent: Agent = {
  id: 'codex',
  kind: 'codex',
  name: 'Codex',
  status: 'ready',
  capabilities: ['chat'],
  limitation: null,
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

describe('SessionDetails', () => {
  it('shows the native failure reason beside a failed browser command', () => {
    render(
      <MantineProvider>
        <SessionDetails session={session} agent={agent} commands={[failedCommand]} messages={[]} approvals={[]} onApproval={vi.fn()} />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByText('工具与命令活动'))
    expect(screen.getByText('Missing environment variable: OPENCODEX_API_AUTH_TOKEN.')).toBeInTheDocument()
  })
})
