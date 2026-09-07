import { beforeEach, describe, expect, it } from 'vitest'
import { mergeHistoryPages } from './useAstrorderData'
import type { Message } from '../domain/types'
import { selectMessages, useAstrorderStore } from '../state/store'

const agentId = 'agent-history'
const sessionId = 'session-history'

function message(id: string, text: string): Message {
  return {
    id,
    agent_id: agentId,
    session_id: sessionId,
    role: 'assistant',
    kind: 'message',
    text,
    attachments: [],
    created_at: '2026-09-06T00:00:00Z',
    command_id: null,
    tool: null,
  }
}

beforeEach(() => useAstrorderStore.getState().resetRuntime())

describe('history page reconciliation', () => {
  it('keeps older pages before the latest tail across a tail refetch', () => {
    const older = message('older', 'older')
    const tail = message('tail', 'tail')
    const live = message('live', 'live')

    mergeHistoryPages(agentId, sessionId, [{ items: [tail] }, { items: [older] }])
    useAstrorderStore.getState().applyEvent({
      id: 'live-event',
      cursor: 1,
      type: 'message.upsert',
      agent_id: agentId,
      session_id: sessionId,
      data: { ...live },
    })
    mergeHistoryPages(agentId, sessionId, [{ items: [tail, live] }, { items: [older] }])

    expect(selectMessages(useAstrorderStore.getState(), agentId, sessionId).map((item) => item.id)).toEqual([
      'older',
      'tail',
      'live',
    ])
  })
})
