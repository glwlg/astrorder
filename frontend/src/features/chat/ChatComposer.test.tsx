import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { ChatComposer } from './ChatComposer'

const session: Session = { id: 'native-one', agent_id: 'hermes', title: 'one', workspace: null, status: 'idle', updated_at: '2026-01-01T00:00:00Z' }
const agent: Agent = { id: 'hermes', name: 'Hermes', kind: 'hermes', status: 'ready', capabilities: ['chat', 'stop'], limitation: null }
afterEach(() => { cleanup(); vi.restoreAllMocks(); useAstrorderStore.getState().resetRuntime() })
it('embeds the model picker and swaps the primary send button for native stop until completion', async () => {
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'native-model', provider: 'provider' })
  const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: input.action === 'stop' ? 'completed' : 'running', attachments: [], created_at: session.updated_at, error: null, target_id: input.target_id ?? null }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrap = (value: Session) => <MantineProvider><QueryClientProvider client={client}><ChatComposer session={value} agent={agent} /></QueryClientProvider></MantineProvider>
  const view = render(wrap(session))
  const model = await screen.findByRole('button', { name: '选择会话模型' })
  expect(model.closest('.composer-card')).not.toBeNull()
  expect(screen.queryByText('模型', { exact: true })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '排队发送' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '停止' })).not.toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: 'hello' } })
  fireEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(screen.getByRole('button', { name: '停止' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: '停止' }))
  await waitFor(() => expect(create).toHaveBeenCalledTimes(2))
  expect(create.mock.calls[1][0]).toMatchObject({ action: 'stop', text: '', target_id: create.mock.calls[0][0].id, session_id: session.id })
  useAstrorderStore.getState().resetRuntime()
  view.rerender(wrap({ ...session, status: 'running' }))
  expect(screen.getByRole('button', { name: '停止' })).toBeInTheDocument()
  view.rerender(wrap(session))
  expect(screen.getByRole('button', { name: '发送' })).toBeInTheDocument()
  client.clear()
})
