import { MantineProvider } from '@mantine/core'
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ConfirmPopover } from './ConfirmPopover'

describe('ConfirmPopover', () => {
  it('anchors a confirmation opened at the viewport edge instead of centering it', async () => {
    render(
      <MantineProvider>
        <ConfirmPopover
          opened
          coords={{ x: 0, y: 100 }}
          title="删除会话？"
          message="确定删除会话“测试会话”吗？"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />
      </MantineProvider>,
    )

    const dialog = await screen.findByRole('dialog', { name: '删除会话？' })
    await waitFor(() => expect(dialog).toHaveStyle({ left: '8px', top: '108px' }))
  })
})
