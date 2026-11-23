import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { clearHandoffSummaryCache, HandoffSummaryCard } from './HandoffSummaryCard'

const baseSession: Session = {
  id: 'target-agy-1',
  agent_id: 'agy-1',
  title: '监控Agent',
  workspace: '/home/luwei/workspace/OpsCore',
  status: 'idle',
  updated_at: '2026-11-23T09:15:00Z',
  handoff_from_agent_id: 'codex-1',
  handoff_from_session_id: 'codex-src-1',
  handoff_context: 'native-handoff-v1:{"agent_id":"codex-1","session_id":"fork-1","connection_id":"","message_id":"msg-1"}',
  handoff_context_consumed: false,
}

beforeEach(() => {
  clearHandoffSummaryCache()
  useAstrorderStore.setState({
    agents: {
      'codex-1': { id: 'codex-1', kind: 'codex', name: 'Codex', status: 'ready', capabilities: [], limitation: null },
      'agy-1': { id: 'agy-1', kind: 'antigravity', name: 'Antigravity', status: 'ready', capabilities: [], limitation: null },
    },
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('HandoffSummaryCard', () => {
  it('renders nothing when session has no handoff_context', () => {
    const spy = vi.spyOn(api, 'getSessionHandoffContext')
    const { container } = render(
      <MantineProvider>
        <HandoffSummaryCard
          session={{ ...baseSession, handoff_context: null }}
          renderMarkdown={(text) => <div>{text}</div>}
        />
      </MantineProvider>,
    )
    expect(container.querySelector('[data-testid="handoff-summary-card"]')).toBeNull()
    expect(spy).not.toHaveBeenCalled()
  })

  it('stays collapsed by default with zero API calls and loads lazily on expand', async () => {
    const spy = vi.spyOn(api, 'getSessionHandoffContext').mockResolvedValue({
      available: true,
      mode: 'native_summary',
      summary: '## 当前目标\n继续验收 API 授权复核',
      consumed: false,
      source_agent_id: 'codex-1',
      source_agent_kind: 'codex',
      source_session_id: 'codex-src-1',
      source_session_title: '监控Agent',
    })
    const copy = vi.fn().mockResolvedValue(undefined)
    const previousClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: copy } })

    try {
      render(
        <MantineProvider>
          <HandoffSummaryCard
            session={baseSession}
            renderMarkdown={(text) => <div data-testid="summary-md">{text}</div>}
          />
        </MantineProvider>,
      )

      const toggle = screen.getByRole('button', { name: /交接摘要/ })
      expect(toggle).toHaveAttribute('aria-expanded', 'false')
      expect(toggle).toHaveTextContent('来自 Codex')
      expect(toggle).toHaveTextContent('待随首条消息注入')
      expect(spy).not.toHaveBeenCalled()

      fireEvent.click(toggle)
      await waitFor(() => {
        expect(screen.getByTestId('summary-md')).toHaveTextContent('继续验收 API 授权复核')
      })
      expect(spy).toHaveBeenCalledTimes(1)
      expect(spy).toHaveBeenCalledWith('target-agy-1', 'agy-1')
      expect(screen.getByText('源 Agent 原生交接摘要')).toBeInTheDocument()
      expect(screen.getByText('原会话：监控Agent')).toBeInTheDocument()

      fireEvent.click(screen.getByRole('button', { name: '复制交接摘要' }))
      await waitFor(() => {
        expect(copy).toHaveBeenCalledWith('## 当前目标\n继续验收 API 授权复核')
      })

      // Collapsing and re-expanding serves from cache without refetching.
      fireEvent.click(toggle)
      expect(screen.queryByTestId('summary-md')).toBeNull()
      fireEvent.click(toggle)
      expect(screen.getByTestId('summary-md')).toHaveTextContent('继续验收 API 授权复核')
      expect(spy).toHaveBeenCalledTimes(1)
    } finally {
      if (previousClipboard) Object.defineProperty(navigator, 'clipboard', previousClipboard)
      else Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

  it('shows consumed badge and supports retrying after a fetch error', async () => {
    const spy = vi.spyOn(api, 'getSessionHandoffContext')
      .mockRejectedValueOnce(new Error('Native handoff history unavailable'))
      .mockResolvedValueOnce({
        available: true,
        mode: 'transcript_excerpt',
        summary: '[User]\n修复下拉框',
        consumed: true,
        source_agent_kind: 'antigravity',
      })

    render(
      <MantineProvider>
        <HandoffSummaryCard
          session={{ ...baseSession, handoff_context_consumed: true }}
          renderMarkdown={(text) => <div data-testid="summary-md">{text}</div>}
        />
      </MantineProvider>,
    )

    const toggle = screen.getByRole('button', { name: /交接摘要/ })
    expect(toggle).toHaveTextContent('已注入上下文')

    fireEvent.click(toggle)
    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Native handoff history unavailable')
    })

    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => {
      expect(screen.getByTestId('summary-md')).toHaveTextContent('修复下拉框')
    })
    expect(screen.getByText('源会话历史摘录')).toBeInTheDocument()
    expect(spy).toHaveBeenCalledTimes(2)
  })
})
