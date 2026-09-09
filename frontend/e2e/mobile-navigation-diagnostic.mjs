import { chromium } from '@playwright/test'
import fs from 'node:fs/promises'
const output = 'test-results/mobile-parity/navigation-diagnostic.json'
const records = []
const save = async row => { records.push(row); await fs.writeFile(output, JSON.stringify(records, null, 2)) }
const context = await chromium.launchPersistentContext('C:/Users/luwei/AppData/Local/hermes/cache/astrorder-mobile-reference-browser', { executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true, viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })
try {
  const page = await context.newPage()
  page.setDefaultTimeout(8000)
  page.on('pageerror', error => void save({ error: error.message }))
  page.on('response', response => { if (response.url().includes('/api/')) void save({ status: response.status(), path: new URL(response.url()).pathname }) })
  // Do not use request routing: Playwright routing disables Vite's module cache.
  await page.addInitScript(() => {
    const original = window.fetch.bind(window)
    window.fetch = (input, init) => {
      const method = init?.method || (input instanceof Request ? input.method : 'GET')
      if (!['GET', 'HEAD'].includes(method.toUpperCase())) return Promise.reject(new Error('Read-only diagnostic'))
      return original(input, init)
    }
  })
  await page.goto('http://127.0.0.1:30001/mobile')
  await page.locator('.mobile-workspace').waitFor({ timeout: 35000 })
  for (let i = 0; i < 12; i++) {
    await page.getByRole('button', { name: '打开会话列表' }).click()
    const rows = page.locator('.m-session-row > button:first-child')
    const count = await rows.count()
    if (!count) throw new Error('No visible sessions')
    const started = Date.now()
    await rows.nth(i % count).click()
    await page.getByRole('button', { name: '会话信息' }).click()
    await page.getByRole('button', { name: '关闭面板' }).click()
    await save({ step: i + 1, milliseconds: Date.now() - started, path: new URL(page.url()).pathname })
  }
  console.log(JSON.stringify({ steps: records.filter(r => r.step).length, errors: records.filter(r => r.error), output }))
} catch (error) { await save({ failure: error.message }); console.log(JSON.stringify({ failure: error.message, output })); process.exitCode = 1 }
finally { await context.close() }
