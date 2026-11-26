import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Agent, Project, Session } from '../domain/types'
import { useAstrorderStore } from '../state/store'
import { SessionRail } from './SessionRail'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAstrorderStore.setState({ sessions: {} })
})

describe('SessionRail move session to another project', () => {
  it.each([
    { kind: 'antigravity' as const, agentId: 'agy-local', name: 'Antigravity', sid: '11111111-1111-4111-8111-111111111111' },
    { kind: 'codex' as const, agentId: 'codex-local', name: 'Codex', sid: '22222222-2222-4222-8222-222222222222' },
    { kind: 'hermes' as const, agentId: 'hermes-local', name: 'Hermes', sid: '33333333-3333-4333-8333-333333333333' },
    { kind: 'grok' as const, agentId: 'grok-local', name: 'Grok', sid: '44444444-4444-4444-8444-444444444444' },
  ])('moves an idle $kind session to another project via PATCH /sessions/:id', async ({ kind, agentId, name, sid }) => {
    const agent: Agent = {
      id: agentId,
      kind,
      name,
      status: 'ready',
      capabilities: ['chat', 'events', 'history', 'delete'],
      limitation: null,
    }
    const session: Session = {
      id: sid,
      agent_id: agentId,
      title: `${name} 任务`,
      workspace: '/workspace/project-a',
      project_id: 'proj-a',
      project_name: 'ProjectA',
      status: 'idle',
      updated_at: new Date().toISOString(),
    }
    const projects: Project[] = [
      { id: 'project:local\u0000proj-a', source_id: 'local', project_id: 'proj-a', project_name: 'ProjectA', workspace: '/workspace/project-a', session_count: 1, updated_at: new Date().toISOString() },
      { id: 'project:local\u0000proj-b', source_id: 'local', project_id: 'proj-b', project_name: 'ProjectB', workspace: '/workspace/project-b', session_count: 0, updated_at: new Date().toISOString() },
    ]

    const updateSpy = vi.spyOn(api, 'updateSession').mockResolvedValue({
      ...session,
      workspace: '/workspace/project-b',
      project_id: 'proj-b',
      project_name: 'ProjectB',
      control_state: 'unbound',
    })

    render(
      <MantineProvider>
        <SessionRail
          sessions={[session]}
          agents={{ [agentId]: agent }}
          projects={projects}
          onSelect={() => {}}
        />
      </MantineProvider>,
    )

    const moreButtons = screen.getAllByRole('button', { name: '更多操作' })
    fireEvent.click(moreButtons[0])

    const moveItem = await screen.findByText('移动到其他项目')
    fireEvent.click(moveItem)

    expect(await screen.findByText('移动会话到其他项目')).toBeInTheDocument()

    const selectInput = screen.getByPlaceholderText('选择要移入的目标项目')
    fireEvent.click(selectInput)
    const option = await screen.findByText('ProjectB (/workspace/project-b)')
    fireEvent.click(option)

    const confirmBtn = screen.getByRole('button', { name: '确认移动' })
    fireEvent.click(confirmBtn)

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith(sid, {
        agent_id: agentId,
        workspace: '/workspace/project-b',
      })
    })
  })
})
