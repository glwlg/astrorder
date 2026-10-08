import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AgentCommandMenu, type CommandMenuItem } from './AgentCommandMenu'

const commands: CommandMenuItem[] = [
  { kind: 'command', command: { name: 'compact', description: '压缩上下文', input_hint: null } },
  { kind: 'command', command: { name: 'review', description: '审查代码', input_hint: null } },
]

const originalScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView')
beforeEach(() => Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() }))
afterEach(() => {
  cleanup()
  if (originalScroll) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScroll)
  else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView')
})

describe('animated composer command menu', () => {
  it('adds lightweight list animation without stale filtered options or stealing input focus', () => {
    const onSelect = vi.fn()
    const view = (items: CommandMenuItem[], activeIndex: number) => (
      <MantineProvider>
        <textarea aria-label="测试输入" />
        <AgentCommandMenu items={items} activeIndex={activeIndex} onSelect={onSelect} />
      </MantineProvider>
    )
    const { rerender } = render(view(commands, 0))
    const input = screen.getByLabelText('测试输入')
    input.focus()
    expect(screen.getByRole('listbox')).toHaveClass('astr-animated-list')
    const first = screen.getAllByRole('option')[0]
    expect(first).toHaveClass('astr-animated-list-item')
    expect(first).toHaveAttribute('aria-selected', 'true')
    expect(fireEvent.mouseDown(first)).toBe(false)
    fireEvent.click(first)
    expect(onSelect).toHaveBeenCalledWith(commands[0])
    expect(input).toHaveFocus()
    expect(fireEvent.keyDown(input, { key: 'ArrowDown' })).toBe(true)
    expect(fireEvent.keyDown(input, { key: 'Tab' })).toBe(true)

    rerender(view([commands[1]], 0))
    expect(screen.getAllByRole('option')).toHaveLength(1)
    expect(screen.queryByText('/compact')).not.toBeInTheDocument()
    expect(screen.getByRole('option')).toHaveAttribute('aria-selected', 'true')
    expect(input).toHaveFocus()
    rerender(view([], 0))
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })
})
