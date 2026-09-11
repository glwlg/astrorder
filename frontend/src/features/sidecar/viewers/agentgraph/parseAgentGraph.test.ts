import { describe, expect, it } from 'vitest'
import type { Message, Task } from '../../../../domain/types'
import { parseNodesFromSession } from './parseAgentGraph'

function message(partial: Partial<Message> & Pick<Message, 'id' | 'role'>): Message {
  return {
    session_id: 's',
    agent_id: 'a',
    kind: 'message',
    text: '',
    attachments: [],
    created_at: '2026-01-01T00:00:00Z',
    command_id: null,
    tool: null,
    ...partial,
  }
}

describe('parseNodesFromSession', () => {
  it('maps a user turn, thinking, tools and the assistant reply into a linear decision flow', () => {
    const nodes = parseNodesFromSession([
      message({ id: 'u1', role: 'user', text: '修一下构建' }),
      message({ id: 't1', role: 'assistant', kind: 'thinking', text: '先看测试失败原因' }),
      message({
        id: 'c1',
        role: 'tool',
        kind: 'tool',
        text: '',
        tool: { name: 'commandExecution', arguments: { command: 'npm test' }, status: 'failed' },
      }),
      message({ id: 'a1', role: 'assistant', text: '测试挂了，接着改' }),
    ], [])

    expect(nodes.map((node) => node.kind)).toEqual(['user_prompt', 'thinking', 'tool_call', 'completed'])
    expect(nodes[2]?.status).toBe('failed')
    expect(nodes[2]?.title).toContain('npm test')
  })

  it('does not treat system chatter as thinking, and surfaces subagent tasks', () => {
    const task: Task = {
      id: 'sub-1',
      session_id: 's',
      agent_id: 'a',
      kind: 'subagent',
      title: '审查 diff',
      status: 'running',
      progress: null,
      command: null,
      logs: [],
      target_id: 'sub-1',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:01:00Z',
    }
    const nodes = parseNodesFromSession([
      message({ id: 'sys', role: 'system', text: 'runtime ready' }),
    ], [task])

    expect(nodes.some((node) => node.kind === 'thinking')).toBe(false)
    expect(nodes.some((node) => node.kind === 'subagent' && node.status === 'running')).toBe(true)
  })

  it('returns a ready placeholder when the session has no transcript yet', () => {
    const nodes = parseNodesFromSession([], [])
    expect(nodes).toHaveLength(1)
    expect(nodes[0]?.kind).toBe('planning')
    expect(nodes[0]?.status).toBe('pending')
  })
})
