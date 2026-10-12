import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi } from 'vitest'
import { SessionComposerGate } from './SessionComposerGate'
import type { Session } from '../../domain/types'
const mocks = vi.hoisted(() => ({ readOnly: true, refetch: vi.fn(), sync: vi.fn().mockResolvedValue({}) }))
vi.mock('../../hooks/useSessionModel', () => ({ useSessionModel: () => ({ data: { read_only: mocks.readOnly }, refetch: mocks.refetch }) }))
vi.mock('../../api/client', () => ({ api: { syncSession: mocks.sync } }))
function mount(onFork = vi.fn()) {
  return render(<MantineProvider><QueryClientProvider client={new QueryClient()}><SessionComposerGate session={{ id: 's', agent_id: 'a', control_state: 'owned' } as Session} onFork={onFork}><textarea aria-label="消息内容" /></SessionComposerGate></QueryClientProvider></MantineProvider>)
}
describe('occupied composer', () => {
  it('replaces input and connects retry and fork actions', async () => {
    mocks.readOnly = true
    const fork = vi.fn()
    mount(fork)
    expect(screen.queryByRole('textbox')).toBeNull()
    expect(screen.getByText('已在另一个应用中打开')).toBeTruthy()
    fireEvent.click(screen.getByText('创建聊天分叉'))
    expect(fork).toHaveBeenCalledOnce()
    fireEvent.click(screen.getByText('重试'))
    await waitFor(() => expect(mocks.sync).toHaveBeenCalledWith('s', 'a'))
    await waitFor(() => expect(mocks.refetch).toHaveBeenCalled())
  })
  it('keeps owned input available', () => {
    mocks.readOnly = false
    mount()
    expect(screen.getByRole('textbox')).toBeTruthy()
  })
})
