import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { describe, expect, it, vi } from 'vitest'
import { CreateBotGroupDialog } from './CreateBotGroupDialog'
import { api } from '../api/client'
import type { Agent, BotGroup } from '../domain/types'

const agents: Record<string, Agent> = {
  'local-codex': {
    id: 'local-codex',
    kind: 'codex',
    name: 'Codex',
    status: 'ready',
    capabilities: ['chat'],
    limitation: null,
  },
  'remote-hermes': {
    id: 'remote-hermes',
    kind: 'hermes',
    name: 'Hermes',
    status: 'ready',
    capabilities: ['chat'],
    limitation: null,
    connection_id: 'ssh-debian',
  },
}

describe('CreateBotGroupDialog', () => {
  it('submits selected multi-machine agents with name and custom max_hops', async () => {
    const createdGroup: BotGroup = {
      id: 'group-1',
      name: '发布专家组',
      description: '协同工作',
      members: [
        { machine_id: 'local', agent_id: 'local-codex', name: 'Codex' },
        { machine_id: 'ssh-debian', agent_id: 'remote-hermes', name: 'Hermes' },
      ],
      max_hops: 4,
      created_at: '2026-09-17T00:00:00Z',
      updated_at: '2026-09-17T00:00:00Z',
    }
    const createSpy = vi.spyOn(api, 'createBotGroup').mockResolvedValue({ group: createdGroup, ok: true })
    const onClose = vi.fn()
    const onCreated = vi.fn()

    render(
      <MantineProvider>
        <CreateBotGroupDialog
          agents={agents}
          opened={true}
          onClose={onClose}
          onCreated={onCreated}
        />
      </MantineProvider>,
    )

    // Fill name
    fireEvent.change(screen.getByLabelText(/群聊名称/), { target: { value: '发布专家组' } })

    // Select agents
    const checkboxes = screen.getAllByRole('checkbox')
    expect(checkboxes.length).toBe(2)
    fireEvent.click(checkboxes[0])
    fireEvent.click(checkboxes[1])

    // Submit
    const submitBtn = screen.getByRole('button', { name: '创建群聊' })
    expect(submitBtn).not.toBeDisabled()
    fireEvent.click(submitBtn)

    await waitFor(() => {
      expect(createSpy).toHaveBeenCalledWith(expect.objectContaining({
        name: '发布专家组',
        max_hops: 3,
        members: expect.arrayContaining([
          expect.objectContaining({ agent_id: 'local-codex' }),
          expect.objectContaining({ agent_id: 'remote-hermes' }),
        ]),
      }))
    })

    expect(onCreated).toHaveBeenCalledWith(createdGroup)
    expect(onClose).toHaveBeenCalled()
    createSpy.mockRestore()
  })
})
