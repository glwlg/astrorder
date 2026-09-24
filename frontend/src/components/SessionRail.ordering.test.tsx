import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { notifySessionSubmitted } from '../hooks/useSessionOrder'
import { afterEach, expect, it } from 'vitest'
import { SessionRail } from './SessionRail'
import type { Session } from '../domain/types'
const sessions: Session[] = Array.from({ length: 6 }, (_, i) => ({ id: String(i), agent_id: 'a', title: `session-${i}`, workspace: '/work', status: 'idle', updated_at: `2026-01-0${i+1}T00:00:00Z` }))
afterEach(cleanup)
it('expands sessions 4 at a time and allows collapse', async () => {
  const tenSessions: Session[] = Array.from({ length: 11 }, (_, i) => ({
    id: String(i),
    agent_id: 'a',
    title: `session-${i}`,
    workspace: '/work',
    status: 'idle',
    updated_at: `2026-01-0${i + 1}T00:00:00Z`,
  }))
  const wrap = (rows: Session[]) => <MantineProvider><SessionRail sessions={rows} agents={{}} onSelect={() => {}} /></MantineProvider>
  const view = render(wrap(tenSessions))
  const titles = () => [...view.container.querySelectorAll('.session-row-title')].map(e => e.textContent)

  // 初始默认展示 4 个，还有 7 个
  expect(titles()).toHaveLength(4)
  expect(screen.getByText('展开更多（还有 7 个会话）')).toBeInTheDocument()

  // 第一次点击展开更多：展示 8 个，还有 3 个，且出现收起按钮
  fireEvent.click(screen.getByText(/展开更多/))
  expect(titles()).toHaveLength(8)
  expect(screen.getByText('展开更多（还有 3 个会话）')).toBeInTheDocument()
  expect(screen.getByText('收起')).toBeInTheDocument()

  // 第二次点击展开更多：展示全部 11 个，没有更多展开按钮，只有收起按钮
  fireEvent.click(screen.getByText(/展开更多/))
  expect(titles()).toHaveLength(11)
  expect(screen.queryByText(/展开更多/)).not.toBeInTheDocument()
  expect(screen.getByText('收起')).toBeInTheDocument()

  // 点击收起：重置回 4 个
  fireEvent.click(screen.getByRole('button', { name: /收起/ }))
  expect(screen.getByText('展开更多（还有 7 个会话）')).toBeInTheDocument()
  expect(screen.queryByText('收起')).not.toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: /session-/ })).toHaveLength(4)
})

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
