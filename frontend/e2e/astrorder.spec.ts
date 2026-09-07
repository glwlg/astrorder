import { expect, test, type Page } from '@playwright/test'

const agentId = 'inert-browser-fixture'
const sessionId = 'inert-browser-session'
const sessionTitle = '隔离联调会话'
const tinyPng = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLQ+QAAAABJRU5ErkJggg==',
  'base64',
)

function scopedText(prefix: string): string {
  return `${prefix}-${test.info().project.name}`
}

async function openIsolatedSession(page: Page): Promise<void> {
  const token = process.env.ASTRORDER_E2E_TOKEN
  test.skip(!token, 'Set ASTRORDER_E2E_TOKEN only for an isolated FastAPI fixture run')

  await page.goto(`/chat/${sessionId}?agent_id=${agentId}`)
  await expect(page.getByLabel('访问令牌')).toBeVisible()
  await page.getByLabel('访问令牌').fill(token!)
  await page.getByRole('button', { name: '建立会话' }).click()
  await expect(page).toHaveURL(new RegExp(`/chat/${sessionId}\\?agent_id=${agentId}`))
  expect(new URL(page.url()).search).not.toContain(token!)
  expect(new URL(page.url()).hash).not.toContain(token!)
  await expect(page.getByRole('heading', { name: sessionTitle, level: 2 })).toBeVisible()
  await expect(page.getByTestId('message-inert-initial-message-59')).toBeVisible()
}

async function sendText(page: Page, text: string): Promise<void> {
  await page.getByLabel('消息内容').fill(text)
  const send = page.getByRole('button', { name: '发送', exact: true })
  await expect(send).toBeEnabled()
  await send.click()
}

test.describe.serial('real isolated FastAPI and built SPA integration', () => {
  test('keeps an unauthenticated browser on the authentication screen', async ({ page }) => {
    const response = await page.request.get('/api/v1/auth/session')
    expect(response.ok()).toBeTruthy()
    await expect(response.json()).resolves.toEqual({ authenticated: false })

    await page.goto('/chat')
    await expect(page.getByRole('heading', { name: '登录本地工作台' })).toBeVisible()
    await expect(page.getByLabel('访问令牌')).toBeVisible()
    await expect(page.getByRole('link', { name: '监控室' })).toHaveCount(0)
  })

  test('synchronizes the isolated chat and monitor routes from one server state', async ({ page }) => {
    await openIsolatedSession(page)

    await page.goto('/monitor')
    const card = page.getByTestId(`monitor-card-${agentId}-${sessionId}`)
    await expect(card).toBeVisible()
    await expect(card).toContainText(sessionTitle)
    await card.getByRole('button', { name: '查看会话' }).click()
    await expect(page).toHaveURL(new RegExp(`/chat/${sessionId}\\?agent_id=${agentId}`))
    await expect(page.getByRole('heading', { name: sessionTitle, level: 2 })).toBeVisible()
  })

  test('keeps repeated identical sends distinct through reload replay', async ({ page }) => {
    await openIsolatedSession(page)
    const text = scopedText('重复指令')
    const reply = `回显：${text}`

    await sendText(page, text)
    await expect(page.getByText(reply, { exact: true })).toHaveCount(1)
    await sendText(page, text)
    await expect(page.getByText(reply, { exact: true })).toHaveCount(2)
    const receipts = page.locator('.outbox-receipt').filter({ hasText: text })
    await expect(receipts).toHaveCount(2)
    await expect(receipts.first().getByText('已完成')).toBeVisible()

    await page.reload()
    await expect(page.getByRole('heading', { name: sessionTitle, level: 2 })).toBeVisible()
    await expect(page.getByText(reply, { exact: true })).toHaveCount(2)
    await expect(page.locator('.outbox-receipt').filter({ hasText: text })).toHaveCount(2)
  })

  test('preserves an image-only command and its canonical attachment after replay', async ({ page }) => {
    await openIsolatedSession(page)
    const filename = `tiny-${test.info().project.name}.png`
    await page.locator('input[type="file"]').setInputFiles({
      name: filename,
      mimeType: 'image/png',
      buffer: tinyPng,
    })
    await expect(page.getByText(filename, { exact: true })).toBeVisible()
    await page.getByRole('button', { name: '发送', exact: true }).click()

    const image = page.locator(`img.attachment-image[alt="${filename}"]`)
    await expect(image).toHaveCount(1)
    await expect(image).toBeVisible()
    await expect.poll(() => image.evaluate((element: HTMLImageElement) => element.naturalWidth)).toBeGreaterThan(0)

    await page.reload()
    await expect(page.getByRole('heading', { name: sessionTitle, level: 2 })).toBeVisible()
    await expect(page.locator(`img.attachment-image[alt="${filename}"]`)).toHaveCount(1)
  })

  test('keeps a disconnected delivery unknown without duplicate replay', async ({ page }) => {
    await openIsolatedSession(page)
    const text = scopedText('disconnect-once')
    await sendText(page, text)
    const receipt = page.locator('.outbox-receipt').filter({ hasText: text })
    await expect(receipt).toHaveCount(1)
    await expect(receipt.getByText('结果未知')).toBeVisible()
    await expect(receipt).toContainText('未自动重发')

    await page.reload()
    await expect(page.getByRole('heading', { name: sessionTitle, level: 2 })).toBeVisible()
    await expect(page.locator('.outbox-receipt').filter({ hasText: text })).toHaveCount(1)
  })

  test('keeps a genuine upward browse paused, exposes unsupported controls, and stays in bounds', async ({ page }) => {
    await openIsolatedSession(page)
    const transcript = page.locator('.transcript')
    const initial = await transcript.evaluate((element) => ({
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
      scrollTop: element.scrollTop,
    }))
    expect(initial.scrollHeight).toBeGreaterThan(initial.clientHeight)
    expect(initial.scrollHeight - (initial.scrollTop + initial.clientHeight)).toBeLessThanOrEqual(28)

    await transcript.evaluate((element) => {
      element.scrollTop = 0
      element.dispatchEvent(new Event('scroll', { bubbles: true }))
    })
    await expect(page.getByTestId('return-bottom')).toBeVisible()
    const text = scopedText('after-scroll')
    await sendText(page, text)
    await expect(page.getByText(`回显：${text}`, { exact: true })).toBeVisible()
    expect(await transcript.evaluate((element) => element.scrollTop)).toBeLessThanOrEqual(1)
    await page.getByTestId('return-bottom').click()
    await expect.poll(() => transcript.evaluate((element) => element.scrollHeight - (element.scrollTop + element.clientHeight))).toBeLessThanOrEqual(28)

    await expect(page.getByRole('button', { name: '停止' })).toBeDisabled()
    const failure = scopedText('fixture-fail')
    await sendText(page, failure)
    const failedReceipt = page.locator('.outbox-receipt').filter({ hasText: failure })
    await expect(failedReceipt.getByText('失败')).toBeVisible()
    await expect(failedReceipt).toContainText('Inert connector intentionally rejected this command')

    const dimensions = await page.evaluate(() => ({
      bodyClient: document.body.clientWidth,
      bodyScroll: document.body.scrollWidth,
      rootClient: document.documentElement.clientWidth,
      rootScroll: document.documentElement.scrollWidth,
    }))
    expect(dimensions.bodyScroll).toBeLessThanOrEqual(dimensions.bodyClient)
    expect(dimensions.rootScroll).toBeLessThanOrEqual(dimensions.rootClient)
    await page.screenshot({
      path: test.info().outputPath(`isolated-chat-${test.info().project.name}.png`),
      fullPage: true,
    })

    await page.goto('/agents')
    await expect(page.getByText('Managed launch is disabled')).toHaveCount(2)
    await expect(page.getByRole('button', { name: '请求启动' }).first()).toBeDisabled()
  })
})
