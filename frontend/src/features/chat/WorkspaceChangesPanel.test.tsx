import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { Message, Session } from '../../domain/types'
import { WorkspaceChangesPanel } from './WorkspaceChangesPanel'
import { useSidecarStore } from '../sidecar/sidecarStore'

vi.mock('liquid-gl', () => ({ default: () => ({ destroy: vi.fn() }) }))

const session: Session = {
  id: 'shared-id', agent_id: 'remote-agent', connection_id: 'remote-connection',
  title: '会话', workspace: '/home/user/project', status: 'idle', updated_at: '2026-11-16T00:00:00Z',
}
const clients: QueryClient[] = []
function show(messages: Message[] = [], value = session, onClose = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  clients.push(client)
  return render(<MantineProvider><QueryClientProvider client={client}>
    <div className="chat-workbench"><WorkspaceChangesPanel session={value} messages={messages} onClose={onClose} /></div>
  </QueryClientProvider></MantineProvider>)
}
afterEach(() => {
  cleanup()
  clients.splice(0).forEach(client => client.clear())
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('uses the real workspace diff and opens the exact remote file only on request', async () => {
  const onClose = vi.fn()
  const open = vi.spyOn(useSidecarStore.getState(), 'openArtifact')
  vi.stubGlobal('fetch', vi.fn(async (input: unknown) => new Response(
    String(input).includes('diff-raw')
      ? '@@ -1 +1 @@\n-old\n+new'
      : JSON.stringify({ files: [{ path: 'src/app.ts', status: 'modified', staged: false }], insertions: 1, deletions: 1 }),
    { status: 200 },
  )))
  show([], session, onClose)
  expect(await screen.findByText('+new')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /app.ts修改/ })).toHaveAttribute('aria-pressed', 'true')
  expect(open).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: '完整 Diff ↗' }))
  expect(open).toHaveBeenCalledWith(expect.objectContaining({
    sessionId: 'shared-id', agentId: 'remote-agent', connectionId: 'remote-connection',
    path: 'src/app.ts', mediaType: 'text/x-diff',
    readUrl: expect.stringContaining('connection_id=remote-connection'),
  }), 'diff-viewer')
  expect(onClose).toHaveBeenCalledOnce()
})

it('includes only explicit Agent output attachments, not user uploads or paths in prose', () => {
  const fetcher = vi.fn()
  vi.stubGlobal('fetch', fetcher)
  const message: Message = {
    id: 'm1', session_id: session.id, agent_id: session.agent_id, role: 'assistant', kind: 'message',
    text: 'See /home/user/skills/SKILL.md', attachments: [{ id: 'report', name: 'report.png', media_type: 'image/png', url: '/report.png' }],
    command_id: null, tool: null, created_at: session.updated_at,
  }
  show([message, { ...message, id: 'm2' }, { ...message, id: 'user', role: 'user',
    attachments: [{ id: 'ref', name: 'reference.png', media_type: 'image/png', url: '/ref.png' }] }], { ...session, workspace: null })
  fireEvent.click(screen.getByRole('radio', { name: '产物 1' }))
  expect(screen.getByText('report.png')).toBeInTheDocument()
  expect(screen.queryByText('reference.png')).not.toBeInTheDocument()
  expect(screen.queryByText('SKILL.md')).not.toBeInTheDocument()
  expect(fetcher).not.toHaveBeenCalled()
})

it('reports a status failure without claiming the workspace is clean, and closes on Escape', async () => {
  const onClose = vi.fn()
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 502 })))
  show([], session, onClose)
  expect(await screen.findByRole('alert')).toHaveTextContent('无法读取工作区变更')
  expect(screen.queryByText('工作区没有未提交的变更')).not.toBeInTheDocument()
  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
})
