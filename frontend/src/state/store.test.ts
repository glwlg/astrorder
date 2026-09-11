import { beforeEach, describe, expect, it } from 'vitest'
import type { Command, Message, Project } from '../domain/types'
import { selectOutbox, selectProjects, useAstrorderStore } from './store'

const session = {
  id: 'session-1',
  agent_id: 'agent-1',
  title: '工作台',
  workspace: 'P:/workspace/demo',
  status: 'running' as const,
  updated_at: '2026-01-01T00:00:00Z',
}

function command(id: string, text = '继续', action: Command['action'] = 'send'): Command {
  return {
    id,
    session_id: session.id,
    agent_id: session.agent_id,
    action,
    state: 'received',
    text,
    attachments: [],
    created_at: `2026-01-01T00:00:0${id.endsWith('2') ? '2' : '1'}Z`,
    error: null,
  }
}

function message(id: string, text: string, agentId = session.agent_id): Message {
  return {
    id,
    session_id: session.id,
    agent_id: agentId,
    role: 'user',
    kind: 'message',
    text,
    attachments: [],
    created_at: '2026-01-01T00:00:00Z',
    command_id: null,
    tool: null,
  }
}

beforeEach(() => {
  useAstrorderStore.getState().resetRuntime()
})

describe('shared runtime store', () => {
  it('keeps the native project catalog, including zero-session projects, after bootstrap hydration', () => {
    const project: Project = {
      id: 'project-row-empty',
      source_id: 'source-remote',
      connection_id: 'ssh-wsl',
      project_id: 'empty-project-id',
      project_name: '空项目',
      workspace: '/empty',
      session_count: 0,
      updated_at: '2026-09-07T09:00:00Z',
    }

    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      agents: [],
      projects: [project],
      sessions: [],
      cursor: 1,
    })

    expect(selectProjects(useAstrorderStore.getState())).toEqual([project])
  })

  it('keeps both same-text commands after an old bootstrap snapshot', () => {
    const store = useAstrorderStore.getState()
    store.addOutbox(command('command-1'))
    store.addOutbox(command('command-2'))
    store.hydrateBootstrap({ protocol_version: 1, agents: [], sessions: [], cursor: 1 })

    expect(selectOutbox(useAstrorderStore.getState(), session.agent_id, session.id).map((entry) => entry.command.id)).toEqual([
      'command-1',
      'command-2',
    ])
  })

  it('hydrates durable command receipts after a reconnect without a local optimistic entry', () => {
    const durable = { ...command('durable-command', '已持久化'), state: 'unknown' as const, error: '连接中断' }
    useAstrorderStore.getState().mergeCommands([durable])

    expect(selectOutbox(useAstrorderStore.getState(), session.agent_id, session.id)).toEqual([
      expect.objectContaining({ command: durable, status: 'unknown', error: '连接中断' }),
    ])
  })

  it('keeps route-scoped transcripts separate when ids are reused', () => {
    const store = useAstrorderStore.getState()
    store.mergeMessages('agent-1', session.id, [message('message-1', 'Hermes')])
    store.mergeMessages('agent-2', session.id, [message('message-1', 'Codex', 'agent-2')])

    const current = useAstrorderStore.getState()
    expect(current.messages['agent-1::session-1']?.['message-1'].text).toBe('Hermes')
    expect(current.messages['agent-2::session-1']?.['message-1'].text).toBe('Codex')
  })

  it('does not discard same-id replay events from a different agent scope', () => {
    const store = useAstrorderStore.getState()
    store.applyEvent({
      id: 'native-event-1',
      cursor: 1,
      type: 'message.upsert',
      agent_id: 'agent-1',
      session_id: 'session-1',
      data: { ...message('message-1', 'Hermes', 'agent-1') },
    })
    store.applyEvent({
      id: 'native-event-1',
      cursor: 2,
      type: 'message.upsert',
      agent_id: 'agent-2',
      session_id: 'session-1',
      data: { ...message('message-1', 'Codex', 'agent-2') },
    })

    expect(useAstrorderStore.getState().messages['agent-1::session-1']?.['message-1'].text).toBe('Hermes')
    expect(useAstrorderStore.getState().messages['agent-2::session-1']?.['message-1'].text).toBe('Codex')
  })

  it('stamps liveActivityAt on websocket message upserts but not on history merges', () => {
    const store = useAstrorderStore.getState()
    store.mergeMessages(session.agent_id, session.id, [message('history-1', '历史')])
    expect(useAstrorderStore.getState().liveActivityAt['agent-1::session-1']).toBeUndefined()

    store.applyEvent({
      id: 'live-1',
      cursor: 3,
      type: 'message.upsert',
      agent_id: session.agent_id,
      session_id: session.id,
      data: { ...message('live-1', '流式正文', session.agent_id), role: 'assistant' },
    })
    expect(useAstrorderStore.getState().liveActivityAt['agent-1::session-1']).toBeGreaterThan(Date.now() - 1000)
  })

  it('applies a late command acknowledgement to the exact outbox id', () => {
    const store = useAstrorderStore.getState()
    store.addOutbox({ ...command('command-1'), state: 'unknown', error: '连接中断' })
    store.applyEvent({
      id: 'event-9',
      cursor: 9,
      type: 'command.upsert',
      agent_id: session.agent_id,
      session_id: session.id,
      data: { ...command('command-1'), state: 'accepted', error: null },
    })

    const outbox = selectOutbox(useAstrorderStore.getState(), session.agent_id, session.id)[0]
    expect(outbox.status).toBe('accepted')
    expect(outbox.command.id).toBe('command-1')
  })

  it('does not manufacture a canonical message for an accepted command', () => {
    useAstrorderStore.getState().addOutbox(command('command-1'))
    useAstrorderStore.getState().applyEvent({
      id: 'event-1',
      cursor: 1,
      type: 'command.upsert',
      agent_id: session.agent_id,
      session_id: session.id,
      data: { ...command('command-1'), state: 'accepted' },
    })

    expect(useAstrorderStore.getState().messages['agent-1::session-1']).toBeUndefined()
  })

  it('keeps queue acknowledgement distinct from an accepted send', () => {
    const store = useAstrorderStore.getState()
    store.addOutbox(command('command-queue', '排队', 'enqueue'), 'submitting')
    store.addOutbox(command('command-send', '立即发送'), 'submitting')
    store.applyEvent({
      id: 'event-queue',
      cursor: 10,
      type: 'command.upsert',
      agent_id: session.agent_id,
      session_id: session.id,
      data: { ...command('command-queue', '排队', 'enqueue'), state: 'queued' },
    })
    store.applyEvent({
      id: 'event-send',
      cursor: 11,
      type: 'command.upsert',
      agent_id: session.agent_id,
      session_id: session.id,
      data: { ...command('command-send', '立即发送'), state: 'accepted' },
    })

    const current = selectOutbox(useAstrorderStore.getState(), session.agent_id, session.id)
    expect(current.map((entry) => [entry.command.id, entry.status, entry.command.action])).toEqual([
      ['command-queue', 'queued', 'enqueue'],
      ['command-send', 'accepted', 'send'],
    ])
  })

  it('keeps receipts with the same command id isolated by agent and session scope', () => {
    const store = useAstrorderStore.getState()
    const first = command('shared-command', 'Hermes send')
    const second: Command = {
      ...command('shared-command', 'Codex send'),
      agent_id: 'agent-2',
      session_id: 'session-2',
    }
    store.addOutbox(first)
    store.addOutbox(second)
    store.applyEvent({
      id: 'event-second-command',
      cursor: 12,
      type: 'command.upsert',
      agent_id: second.agent_id,
      session_id: second.session_id,
      data: { ...second, state: 'accepted' },
    })

    expect(selectOutbox(useAstrorderStore.getState(), first.agent_id, first.session_id)).toEqual([
      expect.objectContaining({ command: expect.objectContaining({ text: 'Hermes send' }), status: 'received' }),
    ])
    expect(selectOutbox(useAstrorderStore.getState(), second.agent_id, second.session_id)).toEqual([
      expect.objectContaining({ command: expect.objectContaining({ text: 'Codex send' }), status: 'accepted' }),
    ])
  })

  it('removes project and associated sessions on project.delete event', () => {
    const project: Project = {
      id: 'project-del-test',
      source_id: 'src-1',
      project_id: 'pid-1',
      project_name: 'ToDelete',
      session_count: 1,
      updated_at: '2026-09-09T00:00:00Z',
    }
    const session = {
      id: 'sess-to-del',
      agent_id: 'ag-1',
      title: 'Session To Delete',
      workspace: null,
      status: 'idle' as const,
      updated_at: '2026-09-09T00:00:00Z',
    }
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      agents: [],
      projects: [project],
      sessions: [session],
      cursor: 1,
    })

    expect(selectProjects(useAstrorderStore.getState())).toHaveLength(1)
    expect(Object.keys(useAstrorderStore.getState().sessions)).toHaveLength(1)

    useAstrorderStore.getState().applyEvent({
      id: 'ev-del-proj',
      cursor: 2,
      type: 'project.delete',
      agent_id: null,
      session_id: null,
      data: {
        project_key: 'project-del-test',
        project_id: 'pid-1',
        deleted_sessions: [{ agent_id: 'ag-1', id: 'sess-to-del' }],
      },
    })

    expect(selectProjects(useAstrorderStore.getState())).toHaveLength(0)
    expect(Object.keys(useAstrorderStore.getState().sessions)).toHaveLength(0)
  })
})
