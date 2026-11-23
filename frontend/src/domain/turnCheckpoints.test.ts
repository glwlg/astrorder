import { describe, expect, it } from 'vitest'
import type { SessionCheckpoint } from '../api/client'
import type { Message } from './types'
import { mapCheckpointsToMessages } from './turnCheckpoints'

function msg(overrides: Partial<Message> & { id: string; role: Message['role'] }): Message {
  return {
    agent_id: 'agent-1',
    session_id: 'sess-1',
    kind: 'message',
    text: 'content',
    attachments: [],
    created_at: '2026-11-23T12:00:00Z',
    command_id: null,
    tool: null,
    ...overrides,
  }
}

function cp(overrides: Partial<SessionCheckpoint> & { id: string; turn_number: number }): SessionCheckpoint {
  return {
    agent_id: 'agent-1',
    session_id: 'sess-1',
    kind: 'auto_after',
    label: '快照',
    workspace: '/repo',
    repo_root: '/repo',
    branch: 'master',
    head_sha: '1'.repeat(40),
    tree_sha: 'a'.repeat(40),
    commit_sha: 'b'.repeat(40),
    ref_name: 'refs/astrorder/checkpoints/x/1',
    files_changed: 1,
    insertions: 10,
    deletions: 2,
    files: [],
    created_at: '2026-11-23T12:01:05Z',
    ...overrides,
  }
}

describe('mapCheckpointsToMessages', () => {
  it('pairs checkpoints by command_id and timestamp fallback while skipping empty auto_before baselines', () => {
    const messages: Message[] = [
      msg({ id: 'u1', role: 'user', command_id: 'cmd-1', created_at: '2026-11-23T12:00:00Z' }),
      msg({ id: 't1', role: 'assistant', kind: 'tool', created_at: '2026-11-23T12:00:30Z' }),
      msg({ id: 'a1', role: 'assistant', created_at: '2026-11-23T12:01:00Z' }),
      msg({ id: 'u2', role: 'user', created_at: '2026-11-23T12:05:00Z' }),
      msg({ id: 'a2', role: 'assistant', created_at: '2026-11-23T12:06:00Z' }),
    ]
    const checkpoints: SessionCheckpoint[] = [
      cp({ id: 'cp-base', turn_number: 1, kind: 'auto_before', files_changed: 0, created_at: '2026-11-23T12:00:01Z' }),
      cp({ id: 'cp-1', turn_number: 2, command_id: 'cmd-1', created_at: '2026-11-23T12:01:03Z' }),
      cp({ id: 'cp-2', turn_number: 3, kind: 'manual', created_at: '2026-11-23T12:06:20Z' }),
    ]

    const mapped = mapCheckpointsToMessages(messages, checkpoints)
    expect(mapped.size).toBe(2)
    expect(mapped.get('a1')?.id).toBe('cp-1')
    expect(mapped.get('a2')?.id).toBe('cp-2')
  })
})
