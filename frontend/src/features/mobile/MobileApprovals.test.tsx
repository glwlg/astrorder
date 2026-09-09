import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { MobileApprovals } from './MobileApprovals'

afterEach(() => { cleanup(); vi.restoreAllMocks() })
it.each([['允许本次', 'approve'], ['拒绝', 'cancel']] as const)('submits %s only after the user acts, with native approval scope', async (label, action) => {
  const session = { id: 'native', agent_id: 'codex', title: 'test', workspace: null, status: 'waiting_approval' as const, updated_at: '2026-01-01T00:00:00Z' }
  const send = vi.spyOn(api, 'createCommand').mockResolvedValue({ id: 'receipt', session_id: 'native', agent_id: 'codex', action, state: 'accepted', text: '', attachments: [], created_at: session.updated_at, error: null })
  render(<MobileApprovals session={session} approvals={[{ id: 'approval', agent_id: 'codex', session_id: 'native', title: 'Native approval', detail: 'Read workspace', state: 'pending', target_id: 'native-target', data: {} }]} />)
  expect(send).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: label }))
  await waitFor(() => expect(send).toHaveBeenCalledWith(expect.objectContaining({ action, agent_id: 'codex', session_id: 'native', target_id: 'native-target' })))
})
