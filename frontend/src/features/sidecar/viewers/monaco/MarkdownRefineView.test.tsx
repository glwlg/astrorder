import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../../../api/client'
import {
  MarkdownRefineView,
  replaceMarkdownSection,
  splitMarkdownSections,
} from './MarkdownRefineView'

describe('MarkdownRefineView & section splitter', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('splits markdown along headings while keeping fenced code blocks intact', () => {
    const md = [
      '# 方案说明',
      '这是背景介绍。',
      '```bash',
      '# not a heading inside fence',
      'echo ok',
      '```',
      '## 实施方案',
      '- 第一步',
      '- 第二步',
    ].join('\n')

    const sections = splitMarkdownSections(md)
    expect(sections).toHaveLength(2)
    expect(sections[0].heading).toBe('方案说明')
    expect(sections[0].markdown).toContain('# not a heading inside fence')
    expect(sections[1].heading).toBe('实施方案')

    const updated = replaceMarkdownSection(md, sections[1], '## 实施方案\n- 已精简为单步上线')
    expect(updated).toContain('这是背景介绍。')
    expect(updated).toContain('- 已精简为单步上线')
    expect(updated).not.toContain('- 第二步')
  })

  it('refines a single section with diff preview and applies replacement', async () => {
    vi.spyOn(api, 'refineOfficeSelection').mockResolvedValue({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      model: 'mock-editor',
      rewritten_text: '## 执行计划\n下周完成核心模块交付并上线。',
      assembled_prompt: 'prompt',
    })

    const initialMd = '# 项目报告\n总体进展顺利。\n## 执行计划\n我们下周可能要花不少时间处理模块和上线。'
    const onApplyChange = vi.fn()

    render(
      <MantineProvider>
        <MarkdownRefineView
          markdown={initialMd}
          documentTitle="项目报告"
          session={null}
          writable
          onApplyChange={onApplyChange}
        />
      </MantineProvider>,
    )

    const refineButtons = screen.getAllByRole('button', { name: '精修这段' })
    expect(refineButtons).toHaveLength(2)

    fireEvent.click(refineButtons[1])
    fireEvent.click(screen.getByRole('button', { name: '更精炼' }))

    await waitFor(() => {
      expect(screen.getByLabelText('改写对比')).toBeTruthy()
    })

    fireEvent.click(screen.getByRole('button', { name: '应用到正文' }))
    expect(onApplyChange).toHaveBeenCalledTimes(1)
    const nextMd = onApplyChange.mock.calls[0][0] as string
    expect(nextMd).toContain('总体进展顺利。')
    expect(nextMd).toContain('下周完成核心模块交付并上线。')
    expect(nextMd).not.toContain('可能要花不少时间')
  })
})
