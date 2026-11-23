import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { LazyDetails } from './LazyDetails'

describe('LazyDetails autoOpen', () => {
  it('follows autoOpen transitions without remounting and respects manual user override', () => {
    const { container, rerender } = render(
      <LazyDetails autoOpen={true} loading={true} summary={<span>执行条</span>}>
        <div data-testid="pack-body">内容</div>
      </LazyDetails>,
    )

    const detailsEl = container.querySelector('details')!
    expect(detailsEl.open).toBe(true)
    expect(screen.getByTestId('pack-body')).toBeInTheDocument()

    // Auto-collapse when autoOpen transitions to false
    rerender(
      <LazyDetails autoOpen={false} loading={false} summary={<span>执行条</span>}>
        <div data-testid="pack-body">内容</div>
      </LazyDetails>,
    )
    expect(container.querySelector('details')).toBe(detailsEl)
    expect(detailsEl.open).toBe(false)
    expect(screen.queryByTestId('pack-body')).toBeNull()

    // Manual user open must survive subsequent autoOpen=false -> true -> false transitions
    fireEvent.click(screen.getByText('执行条'))
    expect(detailsEl.open).toBe(true)

    rerender(
      <LazyDetails autoOpen={true} loading={true} summary={<span>执行条</span>}>
        <div data-testid="pack-body">内容</div>
      </LazyDetails>,
    )
    rerender(
      <LazyDetails autoOpen={false} loading={false} summary={<span>执行条</span>}>
        <div data-testid="pack-body">内容</div>
      </LazyDetails>,
    )
    expect(detailsEl.open).toBe(true)

    // Manual user collapse while running must not be forced open by autoOpen=true
    fireEvent.click(screen.getByText('执行条'))
    expect(detailsEl.open).toBe(false)
    rerender(
      <LazyDetails autoOpen={true} loading={true} summary={<span>执行条</span>}>
        <div data-testid="pack-body">内容</div>
      </LazyDetails>,
    )
    expect(detailsEl.open).toBe(false)
  })
})
