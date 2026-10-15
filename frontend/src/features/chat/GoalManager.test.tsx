import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Agent, Session, Task } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { GoalManager } from './GoalManager'

const session: Session = { id: 's', agent_id: 'a', title: 'test', workspace: null, status: 'idle', updated_at: '2026-10-13T00:00:00Z' }
const agent: Agent = { id: 'a', name: 'Codex', kind: 'codex', status: 'ready', capabilities: ['chat'], limitation: null }
const task: Task = { id: 'codex:goal:current', agent_id: 'a', session_id: 's', kind: 'todo', title: '目标: 原目标', status: 'completed', progress: { type: 'goal', source: 'codex', native_status: 'complete' }, updated_at: session.updated_at, created_at: session.updated_at, command: null, target_id: null, logs: [] }
const clients: QueryClient[] = []
afterEach(() => { cleanup(); vi.restoreAllMocks(); useAstrorderStore.getState().resetRuntime(); clients.splice(0).forEach(c => c.clear()) })
function mount(currentSession = session, currentAgent = agent, tasks = [task]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  clients.push(client)
  return render(<MantineProvider><QueryClientProvider client={client}><GoalManager session={currentSession} agent={currentAgent} tasks={tasks} /></QueryClientProvider></MantineProvider>)
}
it.each(['codex', 'hermes'] as const)('keeps a %s session without a goal free of goal controls', kind => {
  mount(session, { ...agent, kind }, [])
  expect(screen.queryByRole('button', { name: '设置目标' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '修改目标' })).not.toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})
it('edits completed native goals and merges confirmed native state', async () => {
  const save = vi.spyOn(api, 'setSessionGoal').mockResolvedValue({ source: 'codex', goal: { threadId: 's', objective: '新目标', status: 'complete', tokensUsed: 123 } })
  mount()
  fireEvent.click(screen.getByRole('button', { name: '修改目标' }))
  expect(await screen.findByLabelText('目标内容')).toHaveValue('原目标')
  fireEvent.change(screen.getByLabelText('目标内容'), { target: { value: '新目标' } })
  fireEvent.click(screen.getByRole('button', { name: '保存目标' }))
  await waitFor(() => expect(save).toHaveBeenCalledWith('s', 'a', '新目标'))
  await waitFor(() => expect(Object.values(useAstrorderStore.getState().tasks)[0]).toMatchObject({ title: '目标: 新目标', status: 'completed' }))
})
it('confirms deletion, allows cancellation, and keeps the goal on native failure', async () => {
  const clear = vi.spyOn(api, 'clearSessionGoal').mockRejectedValue(new Error('原生接口拒绝删除'))
  mount()
  fireEvent.click(screen.getByRole('button', { name: '删除目标' }))
  expect(clear).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: '取消' }))
  expect(clear).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: '删除目标' }))
  const dialog = screen.getByRole('dialog')
  fireEvent.click(within(dialog).getByRole('button', { name: '删除目标' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('原生接口拒绝删除')
  expect(screen.getByText('原目标')).toBeInTheDocument()
  expect(Object.values(useAstrorderStore.getState().tasks)).toHaveLength(0)
})
it('clears the projected goal only after successful native readback', async () => {
  vi.spyOn(api, 'clearSessionGoal').mockResolvedValue({ goal: null, source: 'codex' })
  mount()
  fireEvent.click(screen.getByRole('button', { name: '删除目标' }))
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: '删除目标' }))
  await waitFor(() => expect(Object.values(useAstrorderStore.getState().tasks)[0]).toMatchObject({ status: 'cancelled' }))
})
it('prevents resetting active Hermes goals while allowing clear', () => {
  mount({ ...session, status: 'running' }, { ...agent, kind: 'hermes' })
  expect(screen.getByRole('button', { name: '修改目标' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '删除目标' })).toBeEnabled()
})
it('explains Hermes reset and kickoff before saving', async () => {
  mount(session, { ...agent, kind: 'hermes' })
  fireEvent.click(screen.getByRole('button', { name: '修改目标' }))
  expect(await screen.findByText(/重置轮次预算并立即开始执行/)).toBeInTheDocument()
})
it('hides cleared goals and unsupported native management', () => {
  const view = mount(session, { ...agent, kind: 'grok' })
  expect(screen.queryByRole('button', { name: '修改目标' })).not.toBeInTheDocument()
  view.unmount()
  mount(session, agent, [{ ...task, status: 'cancelled' }])
  expect(screen.queryByText('原目标')).not.toBeInTheDocument()
})
it('pauses and resumes through confirmed native lifecycle without resetting the objective', async () => {
  const change = vi.spyOn(api, 'setSessionGoalStatus').mockResolvedValue({ source: 'codex', goal: { objective: '原目标', status: 'paused', tokensUsed: 123 } })
  const current = { ...task, status: 'running' as const, progress: { ...task.progress, native_status: 'active' } }
  const view = mount(session, agent, [current])
  fireEvent.click(screen.getByRole('button', { name: '暂停目标' }))
  await waitFor(() => expect(change).toHaveBeenCalledWith('s', 'a', 'paused'))
  await waitFor(() => expect(Object.values(useAstrorderStore.getState().tasks)[0]).toMatchObject({ status: 'pending', progress: { native_status: 'paused', tokensUsed: 123 } }))
  view.unmount()
  change.mockResolvedValue({ source: 'codex', goal: { objective: '原目标', status: 'active', tokensUsed: 123 } })
  mount(session, agent, [{ ...current, status: 'pending', progress: { ...current.progress, native_status: 'paused' } }])
  fireEvent.click(screen.getByRole('button', { name: '恢复目标' }))
  await waitFor(() => expect(change).toHaveBeenLastCalledWith('s', 'a', 'active'))
  await waitFor(() => expect(Object.values(useAstrorderStore.getState().tasks)[0]).toMatchObject({ status: 'running' }))
})
