import { MantineProvider } from '@mantine/core'
import { StrictMode } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session } from '../../../../domain/types'
import { useAstrorderStore } from '../../../../state/store'
import type { ViewerContext } from '../../types'
import { SideChatViewer } from './SideChatViewer'

const { createSession } = vi.hoisted(() => ({ createSession: vi.fn() }))

vi.mock('../../../chat/ChatComposer', () => ({
  ChatComposer: ({ session }: { session: Session }) => (
    <div data-testid="shared-chat-composer">{session.id}</div>
  ),
}))

vi.mock('../../../../api/client', () => ({
  api: { createSession },
}))

const parent: Session = {
  id: 'parent-session',
  agent_id: 'agent-1',
  title: '主会话',
  workspace: 'C:/workspace/project',
  status: 'idle',
  updated_at: '2026-09-11T00:00:00Z',
  connection_id: 'local',
}

const sideSession: Session = {
  ...parent,
  id: 'ephemeral-side-session',
  title: '[侧边聊天]',
  ephemeral: true,
}

const artifact: ViewerContext['artifact'] = {
  id: 'sidechat:parent-session',
  name: '侧边聊天',
  kind: 'workspace_file',
  mediaType: 'application/x-astrorder-side-chat',
  readUrl: '',
  writable: false,
  sessionId: parent.id,
  agentId: parent.agent_id,
  connectionId: parent.connection_id || undefined,
}

beforeEach(() => {
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().hydrateBootstrap({
    protocol_version: 1,
    agents: [],
    sessions: [parent],
    cursor: 1,
  })
  createSession.mockReset()
  createSession.mockResolvedValue(sideSession)
})

describe('SideChatViewer', () => {
  it('creates only one child during StrictMode effect replay', async () => {
    render(<StrictMode><MantineProvider><SideChatViewer artifact={artifact} /></MantineProvider></StrictMode>)
    await screen.findByTestId('shared-chat-composer')
    expect(createSession).toHaveBeenCalledTimes(1)
  })

  it('uses the shared main composer while keeping the fork transcript empty', async () => {
    render(
      <MantineProvider>
        <SideChatViewer artifact={artifact} />
      </MantineProvider>,
    )

    await waitFor(() => expect(screen.getByTestId('shared-chat-composer')).toHaveTextContent(sideSession.id))
    expect(screen.queryByPlaceholderText('发送消息…')).not.toBeInTheDocument()
    expect(createSession).toHaveBeenCalledWith(expect.objectContaining({
      agent_id: parent.agent_id,
      workspace: parent.workspace,
      parent_session_id: parent.id,
      ephemeral: true,
    }))
  })
})
