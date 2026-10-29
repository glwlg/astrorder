import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { runInNewContext } from 'node:vm'
import { expect, it, vi } from 'vitest'

function worker(windows: unknown[] = []) {
  const handlers: Record<string, (event: unknown) => void> = {}
  const showNotification = vi.fn().mockResolvedValue(undefined)
  const openWindow = vi.fn().mockResolvedValue(undefined)
  runInNewContext(readFileSync(resolve('public/sw-notification-events.js'), 'utf8'), {
    URL, self: {
      location: { origin: 'https://astrorder.test' },
      addEventListener: (name: string, callback: (event: unknown) => void) => { handlers[name] = callback },
      registration: { showNotification },
      clients: { matchAll: vi.fn().mockResolvedValue(windows), openWindow },
    },
  })
  return { handlers, showNotification, openWindow }
}

it('shows a pushed question without an open page, with a stable notification tag', async () => {
  const w = worker()
  let pending: Promise<void> | undefined
  w.handlers.push({ data: { json: () => ({ key: 'question-1', title: 'Codex 等待你的回答', message: '测试会话', url: '/mobile/chat/s?agent_id=a' }) }, waitUntil: (promise: Promise<void>) => { pending = promise } })
  await pending
  expect(w.showNotification).toHaveBeenCalledWith('Codex 等待你的回答', expect.objectContaining({ tag: 'question-1', data: { url: 'https://astrorder.test/mobile/chat/s?agent_id=a' } }))
})
it('opens the session from a notification with no window', async () => {
  const w = worker()
  let pending: Promise<void> | undefined
  w.handlers.notificationclick({ notification: { close: vi.fn(), data: { url: '/mobile/chat/s?agent_id=a' } }, waitUntil: (promise: Promise<void>) => { pending = promise } })
  await pending
  expect(w.openWindow).toHaveBeenCalledWith('https://astrorder.test/mobile/chat/s?agent_id=a')
})
it('focuses an existing page and rejects a cross-origin notification target', async () => {
  const client = { url: 'https://astrorder.test/mobile', navigate: vi.fn().mockResolvedValue(undefined), focus: vi.fn().mockResolvedValue(undefined) }
  const w = worker([client])
  let pending: Promise<void> | undefined
  const click = (url: string) => w.handlers.notificationclick({ notification: { close: vi.fn(), data: { url } }, waitUntil: (promise: Promise<void>) => { pending = promise } })
  click('/mobile/chat/s')
  await pending
  expect(client.navigate).toHaveBeenCalledWith('https://astrorder.test/mobile/chat/s')
  expect(client.focus).toHaveBeenCalledOnce()
  click('https://evil.test/')
  expect(client.navigate).toHaveBeenCalledOnce()
  expect(w.openWindow).not.toHaveBeenCalled()
})
