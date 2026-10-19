import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { Agent, Session } from '../domain/types'
import { AgentSessionFilter } from './AgentSessionFilter'

afterEach(cleanup)
const agent = (id: string, kind: string, connection_id?: string, status: Agent['status'] = 'ready'): Agent => ({ id, kind, connection_id, name: connection_id ? `开发机 · ${kind}` : kind, status, capabilities: [], limitation: null })
it('keeps bounded icon selection available with many kinds and scopes it to the selected connection', async () => {
  const agents = Object.fromEntries(Array.from({ length: 20 }, (_, i) => [`a${i}`, agent(`a${i}`, `kind-${i}`)]))
  agents.remote = agent('remote', 'codex', 'remote')
  const onChange = vi.fn()
  const view = render(<MantineProvider env="test"><AgentSessionFilter agents={agents} value="all" onChange={onChange} /></MantineProvider>)
  expect(screen.getByRole('combobox', { name: '按连接筛选' })).toBeInTheDocument()
  const trigger = screen.getByRole('button', { name: '按 Agent 类型筛选' })
  expect(trigger.querySelector('svg')).toBeInTheDocument()
  expect(trigger.textContent).toBe('')
  fireEvent.click(trigger)
  expect(await screen.findAllByRole('menuitem')).toHaveLength(22)
  fireEvent.click(trigger)
  fireEvent.change(screen.getByLabelText('按连接筛选'), { target: { value: 'remote' } })
  expect(onChange).toHaveBeenLastCalledWith('connection:remote|kind:all')
  view.unmount()
  render(<MantineProvider env="test"><AgentSessionFilter agents={agents} value="connection:remote|kind:all" onChange={onChange} /></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: '按 Agent 类型筛选' }))
  expect(await screen.findByRole('menuitem', { name: 'Codex' })).toBeInTheDocument()
  expect(screen.getAllByRole('menuitem')).toHaveLength(2)
  fireEvent.click(screen.getByRole('menuitem', { name: 'Codex' }))
  expect(onChange).toHaveBeenLastCalledWith('connection:remote|kind:codex')
})
it('omits Grok on all three disconnected servers even when each has historical sessions', async () => {
  const agents = { live: agent('live', 'codex'), ...Object.fromEntries(['one', 'two', 'three'].map(id => [id, agent(id, 'grok', id, 'disconnected')])) }
  const sessions = ['one', 'two', 'three'].map(agent_id => ({ agent_id })) as Session[]
  render(<MantineProvider env="test"><AgentSessionFilter agents={agents} sessions={sessions} value="all" onChange={vi.fn()} /></MantineProvider>)
  expect(within(screen.getByLabelText('按连接筛选')).getAllByRole('option').map(o => o.getAttribute('value'))).toEqual(['all', 'local'])
  fireEvent.click(screen.getByRole('button', { name: '按 Agent 类型筛选' }))
  expect(await screen.findByRole('menuitem', { name: 'Codex' })).toBeInTheDocument()
  expect(screen.queryByRole('menuitem', { name: /Grok/ })).not.toBeInTheDocument()
})
it('clears a selected kind after disconnection', () => {
  const onChange = vi.fn()
  render(<MantineProvider env="test"><AgentSessionFilter agents={{ c: agent('c', 'codex'), g: agent('g', 'grok', undefined, 'disconnected') }} value="connection:local|kind:grok" onChange={onChange} /></MantineProvider>)
  expect(onChange).toHaveBeenCalledWith('connection:local|kind:all')
})
it('preserves the saved selection while the initial Agent catalog is loading', () => {
  const onChange = vi.fn()
  const view = render(<MantineProvider env="test"><AgentSessionFilter agents={{}} value="connection:local|kind:codex" onChange={onChange} /></MantineProvider>)
  expect(onChange).not.toHaveBeenCalled()
  view.rerender(<MantineProvider env="test"><AgentSessionFilter agents={{ c: agent('c', 'codex') }} value="connection:local|kind:codex" onChange={onChange} /></MantineProvider>)
  expect(onChange).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: '按 Agent 类型筛选' })).toHaveAttribute('title', 'Codex')
})
it('resets an unavailable kind when switching connections', () => {
  const onChange = vi.fn()
  render(<MantineProvider env="test"><AgentSessionFilter agents={{ local: agent('local', 'hermes'), remote: agent('remote', 'codex', 'remote') }} value="connection:local|kind:hermes" onChange={onChange} /></MantineProvider>)
  fireEvent.change(screen.getByLabelText('按连接筛选'), { target: { value: 'remote' } })
  expect(onChange).toHaveBeenCalledWith('connection:remote|kind:all')
})
