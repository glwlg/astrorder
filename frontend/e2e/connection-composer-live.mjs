import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
let input = ''; for await (const chunk of process.stdin) input += chunk
const { token } = JSON.parse(input)
const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
const output = 'test-results/connection-composer-polish'
await fs.mkdir(output, { recursive: true })
try {
  const context = await browser.newContext({ viewport: { width: 1500, height: 1000 } })
  const auth = await context.request.post('http://127.0.0.1:30002/api/v1/auth/session', { headers: { Origin: 'http://127.0.0.1:30002' }, data: { token } })
  expect(auth.ok()).toBe(true)
  const page = await context.newPage()
  const errors = []; page.on('pageerror', e => errors.push(e.message))
  await page.goto('http://127.0.0.1:30001/agents')
  await expect(page.getByRole('heading', { name: '连接管理', exact: true })).toHaveCount(1)
  const local = page.locator('.connection-row').filter({ hasText: '本机 Hermes' })
  const codex = page.locator('.connection-row').filter({ hasText: '本机 Codex' })
  await expect(local).toBeVisible(); await expect(codex).toBeVisible()
  for (const row of [local, codex]) {
    await row.click(); await expect(page.getByRole('dialog')).toBeVisible()
    await page.getByRole('button', { name: '关闭详情' }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
  }
  expect(await page.locator('.agents-page').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
  const widths = await page.locator('.connection-table, .agent-status-list').evaluateAll(elements => elements.map(el => el.getBoundingClientRect().width))
  expect(Math.abs(widths[0] - widths[1])).toBeLessThan(2)
  await expect(page.locator('.agent-status-list')).not.toContainText('Astrorder-owned')
  await page.screenshot({ path: `${output}/connections.png` })
  await page.locator('.session-row').first().click()
  const inputBox = page.getByLabel('消息内容')
  await expect(inputBox).toBeVisible()
  const box = await inputBox.boundingBox(); const model = await page.getByRole('button', { name: '选择会话模型' }).boundingBox()
  expect(model.y).toBeGreaterThan(box.y + box.height - 2)
  const controls = await page.locator('.composer-row .mantine-Button-root').evaluateAll(elements => elements.map(el => {
    const rect = el.getBoundingClientRect()
    const label = el.querySelector('.mantine-Button-label')?.getBoundingClientRect()
    return { name: el.getAttribute('aria-label'), height: rect.height, center: rect.y + rect.height / 2, labelCenter: label ? label.y + label.height / 2 : null }
  }))
  console.log(JSON.stringify({ controls }))
  for (const control of controls) {
    expect(control.height).toBe(36)
    expect(Math.abs(control.center - controls[0].center)).toBeLessThan(1)
    if (control.labelCenter !== null) expect(Math.abs(control.labelCenter - control.center)).toBeLessThan(1)
  }
  await page.screenshot({ path: `${output}/composer.png` })
  const more = page.getByText(/展开更多/).first()
  if (await more.count()) {
    await more.click()
    await expect(page.getByText('收起', { exact: true }).first()).toBeVisible()
    await page.reload()
    await expect(page.getByLabel('消息内容')).toBeVisible()
    await expect(page.getByText('收起', { exact: true })).toHaveCount(0)
  }
  expect(errors).toEqual([])
  console.log(JSON.stringify({ scope: 'live UI, no messages sent or connection mutations', checks: ['one heading', 'peer connection rows and drawers', 'aligned widths', 'Chinese notices', 'composer layout', 'expansion reset'], errors }))
} finally { await browser.close() }
