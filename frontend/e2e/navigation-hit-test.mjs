import { chromium } from '@playwright/test'
import assert from 'node:assert/strict'

// Isolated browser fixtures: never authenticate or send to real Agent sessions.
const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  await page.route('**/api/v1/**', async route => {
    const url = route.request().url()
    const data = url.endsWith('/auth/session') ? { authenticated: true }
      : url.endsWith('/bootstrap') ? { protocol_version: 1, agents: [], sessions: [], cursor: 0 }
      : { items: [] }
    await route.fulfill({ json: data })
  })
  await page.routeWebSocket('**/ws/**', () => {})
  await page.goto('http://127.0.0.1:30001/chat')
  const nav = page.getByRole('link', { name: '监控室' })
  await nav.waitFor()
  const hit = await nav.evaluate(el => {
    const r = el.getBoundingClientRect()
    const top = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)
    return { clickable: el.contains(top), hit: top?.className }
  })
  console.log('Navigation hit test:', hit)
  assert.equal(hit.clickable, true, 'Navigation is covered by another layout element')
  await nav.click({ timeout: 3000 })
  await page.waitForURL('**/monitor')
  const heading = page.getByRole('heading', { name: '监控室', exact: true })
  assert(await heading.isVisible())
  const box = await heading.boundingBox()
  assert(box.y >= 64 && box.y < 900, 'Main content must be inside the viewport')
  console.log('Desktop navigation and main viewport passed')
} finally {
  await browser.close()
}
