import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
let input = ''; for await (const chunk of process.stdin) input += chunk
const { token } = JSON.parse(input)
const origin = 'http://127.0.0.1:30001'
const output = 'test-results/ui-refinement-live'
await fs.mkdir(output, { recursive: true })
const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
const results = []
try {
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  expect((await context.request.post(origin + '/api/v1/auth/session', { headers: { Origin: origin }, data: { token } })).ok()).toBe(true)
  const data = await (await context.request.get(origin + '/api/v1/bootstrap')).json()
  const selected = data.sessions.find(s => s.id === '20260906_133220_28fc67')
  expect(selected).toBeTruthy()
  const route = `/chat/${encodeURIComponent(selected.id)}?agent_id=${encodeURIComponent(selected.agent_id)}`
  const page = await context.newPage()
  const errors = []; page.on('pageerror', e => errors.push(e.message))
  await page.goto(origin + route)
  await expect(page.locator('.chat-heading h2')).toHaveText(selected.title)
  await expect(page.locator('.transcript .message-row, .transcript .activity-pack').first()).toBeVisible({ timeout: 30000 })
  for (let i = 0; i < 30 && await page.locator('.transcript .message-row').count() === 0; i++) {
    const response = page.waitForResponse(r => r.url().includes('/messages?') && r.status() === 200)
    await page.locator('.history-button').click(); await response
  }
  await expect(page.locator('.transcript .message-row').first()).toBeVisible({ timeout: 30000 })
  await expect(page.locator('.desktop-details')).toHaveCount(0)
  expect((await page.locator('.transcript').boundingBox()).width).toBeLessThanOrEqual(860)
  await page.getByRole('button', { name: '打开会话详情' }).click()
  await expect(page.getByRole('dialog').getByLabel('运行信息')).toContainText(selected.id)
  await page.getByRole('button', { name: '固定详情侧栏' }).click()
  await expect(page.locator('.desktop-details')).toBeVisible()
  await page.getByRole('button', { name: '收起详情侧栏' }).click()
  await expect(page.locator('.desktop-details')).toHaveCount(0)
  await page.screenshot({ path: output + '/desktop.png' })
  results.push({ check: 'desktop-native-details-and-reading-column', passed: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(origin + '/mobile' + route)
  await expect(page.locator('.m-card-head h1')).toHaveText(selected.title)
  await expect(page.locator('.m-msg, .m-think-pack').first()).toBeVisible({ timeout: 30000 })
  for (let i = 0; i < 30 && await page.locator('.m-msg').count() === 0; i++) {
    const response = page.waitForResponse(r => r.url().includes('/messages?') && r.status() === 200)
    await page.locator('.m-earlier').click(); await response
  }
  await expect(page.locator('.m-msg').first()).toBeVisible({ timeout: 30000 })
  await expect(page.locator('.m-composer').getByRole('button', { name: '选择会话模型' })).toBeVisible()
  await page.getByRole('button', { name: '会话操作' }).click()
  await expect(page.getByRole('menuitem', { name: '终止全部任务' })).toBeVisible()
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: '应用设置' }).click()
  await expect(page.getByRole('menuitem', { name: '连接管理' })).toBeVisible()
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: '打开会话列表' }).click()
  await expect(page.getByRole('dialog', { name: '会话列表' })).toBeVisible()
  await page.getByRole('button', { name: '关闭面板' }).click()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: output + '/mobile.png' })
  await page.getByRole('button', { name: '新建会话', exact: true }).click()
  await expect(page.getByRole('button', { name: '创建会话', exact: true })).toBeDisabled()
  await expect(page.getByLabel('选择 Agent')).toBeVisible()
  results.push({ check: 'mobile-menus-navigation-composer-and-create-dialog', passed: true })
  expect(errors).toEqual([])
  results.push({ check: 'no-page-exceptions', passed: true })
  await fs.writeFile(output + '/results.json', JSON.stringify(results, null, 2))
  console.log(JSON.stringify({ passed: results.length, scope: 'native read-only UI; no prompts, model changes or native session creation', output }))
} catch (error) { console.error(error.message); process.exitCode = 1 } finally { await browser.close() }
