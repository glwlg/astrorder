import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { ConnectionHistoryEntry } from '../../domain/types'
import { ConnectionHistoryTimeline } from './ConnectionHistoryTimeline'

const items: ConnectionHistoryEntry[] = [
  { id: 'history-1', connection_id: 'ssh-1', stage: 'deploy', state: 'completed', detail: '插件部署完成。', details: { phase: 'deploy' }, created_at: '2026-09-07T00:00:00Z' },
  { id: 'history-2', connection_id: 'ssh-1', stage: 'handshake', state: 'failed', detail: '握手失败。', details: { code: 'timeout' }, created_at: '2026-09-07T00:01:00Z' },
]

describe('ConnectionHistoryTimeline', () => {
  it('renders real stages and requests an older page', () => {
    const onLoadMore = vi.fn()
    render(<MantineProvider><ConnectionHistoryTimeline items={items} isLoading={false} error={null} hasMore onLoadMore={onLoadMore} /></MantineProvider>)
    expect(screen.getByText('插件部署完成。')).toBeInTheDocument()
    expect(screen.getByText('握手失败。')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '加载更早记录' }))
    expect(onLoadMore).toHaveBeenCalledOnce()
  })

  it('keeps empty and unavailable states explicit', () => {
    const { rerender } = render(<MantineProvider><ConnectionHistoryTimeline items={[]} isLoading={false} error={new Error('history unavailable')} hasMore={false} onLoadMore={vi.fn()} /></MantineProvider>)
    expect(screen.getByText(/运行历史不可用/)).toBeInTheDocument()
    rerender(<MantineProvider><ConnectionHistoryTimeline items={[]} isLoading={false} error={null} hasMore={false} onLoadMore={vi.fn()} /></MantineProvider>)
    expect(screen.getByText('尚无连接运行历史。')).toBeInTheDocument()
  })
})
