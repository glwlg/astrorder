import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Agent } from '../domain/types'
import { NewSessionDialog } from './NewSessionDialog'
const agents: Record<string, Agent> = Object.fromEntries(['hermes', 'codex'].map(kind => [kind, { id: kind, kind, name: kind, source_id: 'local-' + kind, status: 'ready', capabilities: ['chat'], limitation: null }])) as Record<string, Agent>
afterEach(() => { cleanup(); vi.restoreAllMocks() })
it('requires explicit Agent selection and uses the returned native ID', async () => {
 const created = { id: 'native-id', agent_id: 'codex', title: 'new', workspace: '/work', status: 'idle' as const, updated_at: '2026-09-09T00:00:00Z' }
 const create = vi.spyOn(api, 'createSession').mockResolvedValue(created)
 const onCreated = vi.fn()
 render(<MantineProvider><NewSessionDialog agents={agents} project={null} onClose={() => {}} onCreated={onCreated} /></MantineProvider>)
 expect(await screen.findByRole('button', { name: '创建会话' })).toBeDisabled()
 fireEvent.change(screen.getByLabelText('选择 Agent'), { target: { value: 'codex' } })
 fireEvent.change(screen.getByLabelText('工作区'), { target: { value: '/work' } })
 fireEvent.click(screen.getByRole('button', { name: '创建会话' }))
 await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ agent_id: 'codex', workspace: '/work' })))
 expect(onCreated).toHaveBeenCalledWith(created)
})
