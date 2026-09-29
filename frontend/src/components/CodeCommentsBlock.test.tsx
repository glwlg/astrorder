import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { CodeCommentsBlock, extractCodeComments } from './CodeCommentsBlock'

it('copies every comment with its description and full file location', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.assign(navigator, { clipboard: { writeText } })
  render(
    <MantineProvider>
      <CodeCommentsBlock comments={[
        { title: '[P1] 修复竞态', body: '第一步会丢失状态。\n需要保留原值。', file: 'P:\\workspace\\astrorder\\src\\core\\state.ts', start: 12, end: 18, priority: 1 },
        { title: '处理错误', body: '异常应显示给用户。', file: 'src/ui/error.ts', start: 6 },
      ]} />
    </MantineProvider>,
  )

  fireEvent.click(screen.getByRole('button', { name: '复制注释' }))
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(
    '### [P1] 修复竞态\n\n位置：`P:\\workspace\\astrorder\\src\\core\\state.ts:12-18`\n\n问题描述：第一步会丢失状态。\n需要保留原值。\n\n' +
    '### [P2] 处理错误\n\n位置：`src/ui/error.ts:6`\n\n问题描述：异常应显示给用户。',
  ))
  expect(screen.getByRole('button', { name: '已复制注释' })).toBeInTheDocument()
})

it('preserves escaped quotes and braces in a code-comment description', async () => {
  const raw = String.raw`发现问题：
::code-comment{title="[P1] 不完整的模型返回仍能自动关闭问题" body="模型只返回 {\"status\":\"FIXED\"}、没有任何判断依据时，代码会自行补上“已得到确凿修复”并关闭问题；空对象或 FAILED 状态也会被当成正常的 STILL_OPEN。临时脚本已复现。应严格校验状态和非空依据，不符合契约的结果必须返回 FAILED，保留问题状态。" file="/home/luwei/workspace/OpsCore/app/services/code_review_ai_service.py" start=105 end=110 priority=1}`
  const { comments, cleanText } = extractCodeComments(raw)
  expect(cleanText).toBe('发现问题：')
  expect(comments).toEqual([{
    title: '[P1] 不完整的模型返回仍能自动关闭问题',
    body: '模型只返回 {"status":"FIXED"}、没有任何判断依据时，代码会自行补上“已得到确凿修复”并关闭问题；空对象或 FAILED 状态也会被当成正常的 STILL_OPEN。临时脚本已复现。应严格校验状态和非空依据，不符合契约的结果必须返回 FAILED，保留问题状态。',
    file: '/home/luwei/workspace/OpsCore/app/services/code_review_ai_service.py',
    start: 105,
    end: 110,
    priority: 1,
  }])

  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.assign(navigator, { clipboard: { writeText } })
  render(<MantineProvider><CodeCommentsBlock comments={comments} /></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: '复制注释' }))
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(
    '### [P1] 不完整的模型返回仍能自动关闭问题\n\n' +
    '位置：`/home/luwei/workspace/OpsCore/app/services/code_review_ai_service.py:105-110`\n\n' +
    `问题描述：${comments[0].body}`,
  ))
})
