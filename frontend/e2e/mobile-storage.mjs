import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
const context = await browser.newContext()
const page = await context.newPage()
const results = []
try {
  await page.route('**/api/**', route => route.fulfill({ contentType: 'application/json', body: '{"authenticated":false}' }))
  await page.goto('http://127.0.0.1:30001/')
  await page.evaluate(async () => {
    const { mobileOutboxStorage } = await import('/src/features/mobile/mobileOutboxStorage.ts')
    await mobileOutboxStorage.save([{ payload: { id: 'isolated-file', agent_id: 'inert', session_id: 'inert-native', action: 'send', text: '> 引用\n\n离线正文', attachment_ids: [], target_id: null }, files: [new File(['offline-content'], '离线.txt', { type: 'text/plain' })], attachments: [], state: 'queued' }])
  })
  await page.reload()
  const restored = await page.evaluate(async () => {
    const { mobileOutboxStorage } = await import('/src/features/mobile/mobileOutboxStorage.ts')
    const [row] = await mobileOutboxStorage.load()
    return { id: row.payload.id, text: row.payload.text, file: row.files[0].name, bytes: await row.files[0].text(), state: row.state }
  })
  expect(restored).toEqual({ id: 'isolated-file', text: '> 引用\n\n离线正文', file: '离线.txt', bytes: 'offline-content', state: 'queued' })
  results.push({ name: 'real Chromium IndexedDB reload preserves ID, quote, text and File bytes', status: 'passed' })
  await fs.mkdir('test-results/mobile-parity', { recursive: true })
  await fs.writeFile('test-results/mobile-parity/storage.json', JSON.stringify(results, null, 2))
  console.log(JSON.stringify(results))
} finally { await context.close(); await browser.close() }
