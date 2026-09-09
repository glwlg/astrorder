import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { notifySessionSubmitted } from '../hooks/useSessionOrder'
import { afterEach, expect, it } from 'vitest'
import { SessionRail } from './SessionRail'
import type { Session } from '../domain/types'
const sessions: Session[] = Array.from({ length: 6 }, (_, i) => ({ id: String(i), agent_id: 'a', title: `session-${i}`, workspace: '/work', status: 'idle', updated_at: `2026-01-0${i+1}T00:00:00Z` }))
afterEach(cleanup)
it('keeps event updates in place and resets expanded-more after remount', () => {
  const wrap = (rows: Session[]) => <MantineProvider><SessionRail sessions={rows} agents={{}} onSelect={() => {}} /></MantineProvider>
  const view = render(wrap(sessions))
  const titles = () => [...view.container.querySelectorAll('.session-row-title')].map(e => e.textContent)
  const initial = titles()
  fireEvent.click(screen.getByText(/展开更多/))
  expect(titles()).toHaveLength(6)
  const expanded = titles()
  view.rerender(wrap(sessions.map(s => ({ ...s, updated_at: s.id === '0' ? '2027-01-01T00:00:00Z' : s.updated_at }))))
  expect(titles()).toEqual(expanded)
  act(() => notifySessionSubmitted(sessions[0]))
  expect(titles()[0]).toBe('session-0')
  const submitted = titles()
  view.rerender(wrap(sessions.map(s => ({ ...s, updated_at: s.id === '1' ? '2028-01-01T00:00:00Z' : s.updated_at }))))
  expect(titles()).toEqual(submitted)
  view.unmount()
  const fresh = render(wrap(sessions))
  expect([...fresh.container.querySelectorAll('.session-row-title')].map(e => e.textContent)).toEqual(initial)
})
