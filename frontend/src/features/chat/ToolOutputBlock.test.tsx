import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { ToolOutputBlock, dedupeToolPayload } from './ToolOutputBlock'
import type { Session } from '../../domain/types'

const sampleSession: Session = {
  id: 'sess-1',
  agent_id: 'agy-1',
  title: 'AGY',
  workspace: '/workspace/demo',
  status: 'idle',
  updated_at: '2026-11-23T00:00:00Z',
}

describe('ToolOutputBlock', () => {
  it('renders Hermes search_files formatted text', () => {
    const json = JSON.stringify({
      total_count: 11,
      matches_format: 'path-grouped...',
      matches_text: 'backend/src/bot_groups.py\n 101: request.app.state',
      truncated: true,
      _hint: 'Results truncated. Use offset=10 to see more.',
    })

    render(<ToolOutputBlock text={json} toolName="search_files" />)

    expect(screen.getByText('搜索匹配结果')).toBeInTheDocument()
    expect(screen.getByText('共 11 项')).toBeInTheDocument()
    expect(screen.getByText('已截断')).toBeInTheDocument()
    expect(screen.getByText(/backend\/src\/bot_groups\.py/)).toBeInTheDocument()
    expect(screen.getByText(/Results truncated/)).toBeInTheDocument()
  })

  it('renders Hermes read_file formatted content', () => {
    const json = JSON.stringify({
      content: '100| def _append_agent_message():\n101|   return True',
      total_lines: 861,
      truncated: true,
      hint: 'Use offset=160 to continue reading',
    })

    render(<ToolOutputBlock text={json} toolName="read_file" />)

    expect(screen.getByText('文件读取内容')).toBeInTheDocument()
    expect(screen.getByText('共 861 行')).toBeInTheDocument()
    expect(screen.getByText(/def _append_agent_message/)).toBeInTheDocument()
    expect(screen.getByText(/Use offset=160 to continue reading/)).toBeInTheDocument()
  })

  it('renders raw tool output when json parsing fails or plain text is returned', () => {
    render(<ToolOutputBlock text="plain execution output" />)
    expect(screen.getByText('plain execution output')).toBeInTheDocument()
  })

  it('renders image preview instead of duplicate Path/path JSON for Antigravity read_file on png', () => {
    const onImageClick = vi.fn()
    render(
      <ToolOutputBlock
        toolName="read_file"
        session={sampleSession}
        onImageClick={onImageClick}
        payload={{
          Path: '/workspace/demo/scratch/agent-detail-install-light.png',
          path: '/workspace/demo/scratch/agent-detail-install-light.png',
        }}
      />,
    )

    expect(screen.getByText('agent-detail-install-light.png')).toBeInTheDocument()
    const img = screen.getByRole('img', { name: 'agent-detail-install-light.png' })
    expect(img).toHaveAttribute('src', expect.stringContaining('/api/v1/files/raw?'))
    expect(screen.queryByText(/"Path"/)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '放大查看 agent-detail-install-light.png' }))
    expect(onImageClick).toHaveBeenCalledWith(expect.stringContaining('/api/v1/files/raw?'))

    // Raw JSON remains available on explicit click
    fireEvent.click(screen.getByRole('button', { name: 'JSON' }))
    expect(screen.getByText(/"Path"/)).toBeInTheDocument()
  })

  it('deduplicates Antigravity alias parameters into a clean key-value card', () => {
    const entries = dedupeToolPayload({
      Path: '/workspace/demo/src/app.ts',
      path: '/workspace/demo/src/app.ts',
      StartLine: 10,
      start_line: 10,
      WaitMsBeforeAsync: 5000,
    })
    expect(entries.map(e => e.key)).toEqual(['path', 'start_line'])

    render(
      <ToolOutputBlock
        toolName="invoke_subagent"
        payload={{ Role: 'Codebase Researcher', TypeName: 'research', Prompt: 'Inspect auth flow' }}
      />,
    )
    expect(screen.getByText('调用参数')).toBeInTheDocument()
    expect(screen.getByText('Codebase Researcher')).toBeInTheDocument()
    expect(screen.queryByText(/"TypeName"/)).not.toBeInTheDocument()
  })
})
