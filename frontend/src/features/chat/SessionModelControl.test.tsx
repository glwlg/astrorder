import { MantineProvider } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { SessionModelControl, summarizeQuota } from './SessionModelControl'
import { REASONING_EFFORTS } from './composerMedia'

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

it('offers exactly the five enabled thinking levels', () => {
  expect(REASONING_EFFORTS).toEqual([
    { value: 'low', label: '低' },
    { value: 'medium', label: '中' },
    { value: 'high', label: '高' },
    { value: 'xhigh', label: '超高' },
    { value: 'max', label: '最高' },
  ])
})

it('summarizes model quota health accurately and defensively', () => {
  expect(summarizeQuota(null)).toBeNull()
  expect(summarizeQuota({ model: 'm', providers: [], reports: [], accounts: [] })).toBeNull()

  // Provider report with high remaining
  expect(summarizeQuota({
    model: 'gemini-3.8-flash',
    providers: ['google-antigravity'],
    reports: [{ provider: 'google-antigravity', label: 'Google', quota: { weeklyPercent: 25 } }],
    accounts: [],
  })).toEqual({
    status: 'healthy',
    label: '余75%',
    detail: 'Google 配额充裕 (余 75%)',
  })

  // Accounts with tight quota
  expect(summarizeQuota({
    model: 'gpt-5.6-sol',
    providers: ['openai'],
    reports: [],
    accounts: [{ id: 'acc1', paused: false, quota: { weeklyPercent: 88 } }],
  })).toEqual({
    status: 'tight',
    label: '余12%',
    detail: '最佳账号剩余 12%',
  })

  // Accounts all exhausted/paused
  expect(summarizeQuota({
    model: 'gpt-5.6-sol',
    providers: ['openai'],
    reports: [],
    accounts: [{ id: 'acc1', paused: true, quota: { weeklyPercent: 50 } }],
  })).toEqual({
    status: 'exhausted',
    label: '不可用',
    detail: '所有账号已暂停或暂无额度',
  })
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
  expect(switchBtn).toHaveTextContent('p/bound')
  expect(switchBtn.querySelector('.codex-model-effort-highlight')).toBeInTheDocument()
  fireEvent.click(switchBtn.querySelector('.codex-model-name-label')!)
  
  const targetItem = await screen.findByText('Provider · next')
  fireEvent.click(targetItem)
  
  await waitFor(() => expect(change).toHaveBeenCalledWith('native', 'source', 'p', 'next'))
  await waitFor(() => expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveTextContent('p/next'))
  expect(notifications.show).not.toHaveBeenCalled()
})

it('shows the native default medium effort when the runtime has not reported one', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'bound', provider: 'p', effort: null })
  vi.spyOn(api, 'getSessionModels').mockResolvedValue({ items: [] })
  renderControl()

  await waitFor(() => expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveTextContent('中'))
  const slider = await openEffortSlider()
  expect(slider).toHaveAttribute('aria-valuenow', '1')
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

it.each(['#93c5fd', '#60a5fa', '#3b82f6', '#0066cc', '#1e40af'].map((color, index) => ({ color, index })))(
  'uses the color and fill endpoint for effort $index',
  async ({ color, index }) => {
    vi.spyOn(api, 'getSessionModel').mockResolvedValue({
      model: 'bound', provider: 'p', effort: REASONING_EFFORTS[index].value,
    })
    vi.spyOn(api, 'getSessionModels').mockResolvedValue({ items: [] })
    renderControl()
    await waitFor(() => expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveTextContent('p/bound'))
    const slider = await openEffortSlider()
    expect(slider).toHaveAttribute('aria-valuenow', String(index))
    const root = document.querySelector<HTMLElement>('.mantine-Slider-root')!
    expect(root.style.getPropertyValue('--slider-color')).toBe(color)
    const bar = root.querySelector<HTMLElement>('.mantine-Slider-bar')!
    expect(bar.style.width).toBe(index === 4 ? '' : `calc(${index * 25}% + var(--slider-size))`)
  },
)

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

