import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Message } from '../../domain/types'
import { TurnMinimap, buildMinimapNodes } from './TurnMinimap'

function msg(id: string, role: Message['role'], kind: Message['kind'], text: string): Message {
  return {
    id,
    session_id: 's1',
    agent_id: 'codex',
    role,
    kind,
    text,
    attachments: [],
    created_at: '2026-11-21T08:00:00Z',
    command_id: null,
    tool: null,
  }
}

describe('TurnMinimap', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('classifies user, activity pack, code reply, and pending approvals accurately', () => {
    const messages = [
      msg('m1', 'user', 'message', '分析系统架构'),
      msg('m2', 'tool', 'tool', 'read_file'),
      msg('m3', 'assistant', 'message', '这是修复：\n```ts\nconst x = 1\n```'),
    ]
    const packs = new Map<number, Message[]>([[1, [messages[1]]]])
    const nodes = buildMinimapNodes(messages, packs, [
      { id: 'a1', agent_id: 'codex', session_id: 's1', title: '执行测试', detail: '', state: 'pending', target_id: null, data: {} },
    ])
    expect(nodes.map((n) => n.kind)).toEqual(['user', 'activity', 'code', 'approval'])
  })

  it('renders ticks for 3+ turns, shows preview on hover, and scrolls on click', () => {
    const messages = [
      msg('u1', 'user', 'message', '第一轮需求'),
      msg('a1', 'assistant', 'message', '第一轮回答'),
      msg('u2', 'user', 'message', '第二轮追问'),
    ]
    const scroller = document.createElement('div')
    const target = document.createElement('div')
    target.dataset.turnId = 'u2'
    target.scrollIntoView = vi.fn()
    scroller.appendChild(target)

    render(
      <MantineProvider>
        <TurnMinimap visibleMessages={messages} activityPacks={new Map()} approvals={[]} scroller={scroller} />
      </MantineProvider>,
    )

    const tick = screen.getByRole('button', { name: /第二轮追问/ })
    fireEvent.mouseEnter(tick)
    expect(screen.getByRole('tooltip')).toHaveTextContent('第二轮追问')

    fireEvent.click(tick)
    expect(target.scrollIntoView).toHaveBeenCalledOnce()
    expect(target).toHaveClass('turn-jump-highlight')
  })
})
