import { MantineProvider } from '@mantine/core'
import { act, render } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Agent, Session } from '../domain/types'
import { useAstrorderStore } from '../state/store'
import { SessionRail } from './SessionRail'

it('clears live fallback on turn completion and expires it without another event', async () => {
  const preferences = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }
  vi.spyOn(api, 'getPreferences').mockResolvedValue(preferences)
  useAstrorderStore.getState().resetRuntime()
  vi.useFakeTimers()
  const session: Session = { id: 'activity-test', agent_id: 'local', title: '活动状态测试', workspace: '/repo', status: 'idle', updated_at: '2026-09-30T00:00:00Z' }
  const agent: Agent = { id: 'local', kind: 'hermes', name: '本机', status: 'ready', capabilities: [], limitation: null }
  const view = render(<MantineProvider><SessionRail sessions={[session]} agents={{ local: agent }} onSelect={() => {}} /></MantineProvider>)
  let cursor = 0
  const emit = (type: 'message.upsert' | 'session.upsert', data: Record<string, unknown>) => act(() => {
    cursor++
    useAstrorderStore.getState().applyEvent({ id: `activity-${cursor}`, cursor, type, agent_id: session.agent_id, session_id: session.id, data })
  })
  const live = { id: 'stream', agent_id: session.agent_id, session_id: session.id, role: 'assistant', kind: 'message', text: 'partial', attachments: [], created_at: session.updated_at, command_id: null, tool: null }
  try {
    emit('message.upsert', live)
    expect(view.container.querySelector('.session-row')).toHaveClass('is-running')
    emit('session.upsert', { ...session, status: 'idle' })
    expect(view.container.querySelector('.session-row')).not.toHaveClass('is-running')
    emit('message.upsert', live)
    expect(view.container.querySelector('.session-row')).toHaveClass('is-running')
    await act(async () => vi.advanceTimersByTime(15_001))
    expect(view.container.querySelector('.session-row')).not.toHaveClass('is-running')
  } finally {
    view.unmount()
    useAstrorderStore.getState().resetRuntime()
    vi.useRealTimers()
    vi.restoreAllMocks()
  }
})
