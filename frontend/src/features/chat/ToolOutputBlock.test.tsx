import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ToolOutputBlock } from './ToolOutputBlock'

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
})
