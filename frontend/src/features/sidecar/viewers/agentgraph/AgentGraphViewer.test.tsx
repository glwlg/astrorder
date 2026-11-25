import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { scopeKey } from '../../../../domain/semantics'
import type { Message, Session } from '../../../../domain/types'
import { useAstrorderStore } from '../../../../state/store'
import { useSidecarStore } from '../../sidecarStore'
import { AgentGraphViewer } from './AgentGraphViewer'

const testSession: Session = {
  id: 'sess-hub',
  agent_id: 'ag-1',
  title: '重构任务',
  workspace: '/workspace/proj',
  status: 'idle',
  updated_at: '2026-01-01T10:05:00Z',
}

const sampleMessages: Message[] = [
  {
    id: 'm-u1',
    session_id: 'sess-hub',
    agent_id: 'ag-1',
    role: 'user',
    kind: 'message',
    text: '第一步：整理配置模块',
    attachments: [],
    created_at: '2026-01-01T10:00:00Z',
    command_id: null,
    tool: null,
  },
  {
    id: 'm-e1',
    session_id: 'sess-hub',
    agent_id: 'ag-1',
    role: 'tool',
    kind: 'tool',
    text: 'Made 1 replacement in /workspace/proj/src/config.ts (+8/-1 lines).',
    attachments: [],
    created_at: '2026-01-01T10:00:10Z',
    command_id: null,
    tool: { name: 'edit_file', arguments: { Path: '/workspace/proj/src/config.ts' }, status: 'completed' },
  },
  {
    id: 'm-u2',
    session_id: 'sess-hub',
    agent_id: 'ag-1',
    role: 'user',
    kind: 'message',
    text: '第二步：运行构建验证',
    attachments: [],
    created_at: '2026-01-01T10:02:00Z',
    command_id: null,
    tool: null,
  },
  {
    id: 'm-v1',
    session_id: 'sess-hub',
    agent_id: 'ag-1',
    role: 'tool',
    kind: 'tool',
    text: 'Command exited with code 0.\nbuild ok',
    attachments: [],
    created_at: '2026-01-01T10:02:05Z',
    completed_at: '2026-01-01T10:02:08Z',
    command_id: null,
    tool: { name: 'run_command', arguments: { CommandLine: 'npm run build' }, status: 'completed' },
  },
]

beforeEach(() => {
  const key = scopeKey(testSession.agent_id, testSession.id)
  const messagesById = Object.fromEntries(sampleMessages.map((m) => [m.id, m]))
  useAstrorderStore.setState({
    sessions: { [key]: testSession },
    messages: { [key]: messagesById },
    tasks: {},
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('AgentGraphViewer (Execution & Decision Hub)', () => {
  it('renders turn cards newest-first, switches between all 3 views, filters by search, and dispatches jump-to-message', () => {
    const focusSpy = vi.fn()
    window.addEventListener('astrorder:focus-message', focusSpy)
    const openArtifactSpy = vi.spyOn(useSidecarStore.getState(), 'openArtifact').mockImplementation(() => {})

    render(
      <MantineProvider>
        <AgentGraphViewer
          artifact={{
            id: 'agentgraph:sess-hub',
            name: '决策与轨迹',
            kind: 'workspace_file',
            mediaType: 'application/x-agent-graph',
            readUrl: '',
            writable: false,
            sessionId: testSession.id,
            agentId: testSession.agent_id,
          }}
        />
      </MantineProvider>,
    )

    expect(screen.getByText('任务轨迹与决策中枢')).toBeInTheDocument()
    const titles = screen.getAllByRole('heading', { level: 3 }).map((el) => el.textContent)
    expect(titles).toEqual(['第二步：运行构建验证', '第一步：整理配置模块'])

    // Click 「定位对话」 on the latest turn
    fireEvent.click(screen.getAllByRole('button', { name: '定位对话' })[0])
    expect(focusSpy).toHaveBeenCalledTimes(1)
    expect((focusSpy.mock.calls[0][0] as CustomEvent).detail).toMatchObject({
      agentId: 'ag-1',
      sessionId: 'sess-hub',
      messageId: 'm-u2',
    })

    // Switch to 文件影响 tab and open config.ts
    fireEvent.click(screen.getByRole('tab', { name: /文件影响/ }))
    expect(screen.getByText('config.ts')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '打开' }))
    expect(openArtifactSpy).toHaveBeenCalledTimes(1)

    // Switch to 验证与报错 tab
    fireEvent.click(screen.getByRole('tab', { name: /验证与报错/ }))
    expect(screen.getByText('npm run build')).toBeInTheDocument()

    // Search filters out non-matching items
    fireEvent.change(screen.getByLabelText('搜索文件、命令或报错'), { target: { value: 'does-not-exist' } })
    expect(screen.getByText('暂无命令验证或报错记录')).toBeInTheDocument()

    window.removeEventListener('astrorder:focus-message', focusSpy)
  })
})
