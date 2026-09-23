import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CompactionDivider, isCompactionMessage } from './CompactionDivider'
import type { Message } from '../../domain/types'

describe('CompactionDivider', () => {
  it('identifies contextCompaction tool messages', () => {
    const msg: Message = {
      id: 'comp-1',
      session_id: 's-1',
      agent_id: 'a-1',
      command_id: 'c-1',
      role: 'tool',
      kind: 'tool',
      text: '',
      created_at: new Date().toISOString(),
      attachments: [],
      tool: { name: 'contextCompaction', arguments: {}, status: 'completed' },
    }
    expect(isCompactionMessage(msg)).toBe(true)
  })

  it('renders a distinct divider without crashing', () => {
    const msg: Message = {
      id: 'comp-2',
      session_id: 's-1',
      agent_id: 'a-1',
      command_id: 'c-1',
      role: 'tool',
      kind: 'tool',
      text: 'Summary of earlier discussion',
      created_at: new Date().toISOString(),
      attachments: [],
      tool: { name: 'contextCompaction', arguments: {}, status: 'completed' },
    }
    render(<CompactionDivider message={msg} />)
    expect(screen.getByText('上下文已压缩')).toBeInTheDocument()
  })
})
