import { MantineProvider } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { SessionModelControl } from './SessionModelControl'

const session = {
  id: 'native',
  agent_id: 'source',
  title: 'Test',
  status: 'idle' as const,
  workspace: null,
  updated_at: '2026-09-08T00:00:00Z',
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('desktop shows the bound native model and switches the exact session', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'bound', provider: 'p' })
  vi.spyOn(api, 'getSessionModels').mockResolvedValue({
    items: [{ model: 'next', provider: 'p', label: 'Provider · next' }],
  })
  const change = vi.spyOn(api, 'setSessionModel').mockResolvedValue({ model: 'next', provider: 'p' })
  vi.spyOn(notifications, 'show')

  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient()}>
        <SessionModelControl session={session} />
      </QueryClientProvider>
    </MantineProvider>,
  )

  await waitFor(() => expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveTextContent('p/bound'))
  fireEvent.click(screen.getByRole('button', { name: '选择会话模型' }))
  
  const switchBtn = await screen.findByRole('button', { name: '切换到选择模型' })
  fireEvent.click(switchBtn)
  
  const targetItem = await screen.findByText('Provider · next')
  fireEvent.click(targetItem)
  
  await waitFor(() => expect(change).toHaveBeenCalledWith('native', 'source', 'p', 'next'))
  await waitFor(() => expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveTextContent('p/next'))
  expect(notifications.show).not.toHaveBeenCalled()
})

function renderControl() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient()}>
        <SessionModelControl session={session} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

async function openEffortSlider() {
  fireEvent.click(await screen.findByRole('button', { name: '选择会话模型' }))
  return screen.findByRole('slider', { name: '思考强度' })
}

it('commits thinking effort once when the slider is released, not while dragging', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'bound', provider: 'p', effort: 'medium' })
  const changeEffort = vi.spyOn(api, 'setSessionReasoning').mockResolvedValue({ effort: 'max' })
  vi.spyOn(notifications, 'show')
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    bottom: 20,
    right: 200,
    width: 200,
    height: 20,
    toJSON() {
      return {}
    },
  } as DOMRect)

  renderControl()
  const slider = await openEffortSlider()
  const track = slider.parentElement
  expect(track).toBeTruthy()

  fireEvent.mouseDown(track!, { clientX: 10, clientY: 10, button: 0 })
  await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)))
  expect(changeEffort).not.toHaveBeenCalled()

  fireEvent.mouseMove(document, { clientX: 190, clientY: 10 })
  await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)))
  expect(changeEffort).not.toHaveBeenCalled()

  fireEvent.mouseUp(document, { clientX: 190, clientY: 10 })
  await waitFor(() => expect(changeEffort).toHaveBeenCalledTimes(1))
  expect(changeEffort).toHaveBeenCalledWith('native', 'source', 'max')
  expect(notifications.show).not.toHaveBeenCalled()
})

it('toasts only when thinking effort confirmation fails', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'bound', provider: 'p', effort: 'medium' })
  vi.spyOn(api, 'setSessionReasoning').mockRejectedValue(new Error('Codex 思考强度尚未读回确认。'))
  const shown = vi.spyOn(notifications, 'show')

  renderControl()
  const slider = await openEffortSlider()
  fireEvent.keyDown(slider, { key: 'End' })

  await waitFor(() => expect(shown).toHaveBeenCalled())
  expect(shown.mock.calls.some((call) => call[0]?.message === 'Codex 思考强度尚未读回确认。' && call[0]?.color === 'red')).toBe(true)
  expect(shown.mock.calls.some((call) => String(call[0]?.message || '').includes('已由原生端确认'))).toBe(false)
})

it('toasts only when model switch confirmation fails', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'bound', provider: 'p' })
  vi.spyOn(api, 'getSessionModels').mockResolvedValue({
    items: [{ model: 'next', provider: 'p', label: 'Provider · next' }],
  })
  vi.spyOn(api, 'setSessionModel').mockRejectedValue(new Error('模型切换尚未通过原生状态读回确认。'))
  const shown = vi.spyOn(notifications, 'show')

  renderControl()
  fireEvent.click(await screen.findByRole('button', { name: '选择会话模型' }))
  fireEvent.click(await screen.findByRole('button', { name: '切换到选择模型' }))
  fireEvent.click(await screen.findByText('Provider · next'))

  await waitFor(() => expect(shown).toHaveBeenCalled())
  expect(shown.mock.calls.some((call) => call[0]?.message === '模型切换尚未通过原生状态读回确认。' && call[0]?.color === 'red')).toBe(true)
  expect(shown.mock.calls.some((call) => String(call[0]?.message || '').includes('已确认'))).toBe(false)
})

