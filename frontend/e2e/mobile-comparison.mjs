import { chromium } from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'

const output = path.resolve('test-results/mobile-parity')
await fs.mkdir(output, { recursive: true })
const results = []
const context = await chromium.launchPersistentContext('C:/Users/luwei/AppData/Local/hermes/cache/astrorder-mobile-reference-browser', {
  executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true,
  viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true,
})
async function check(surface, name, run) {
  try { await run(); results.push({ surface, name, status: 'passed' }) }
  catch (error) { results.push({ surface, name, status: 'failed', error: String(error.message).slice(0, 350) }) }
  await fs.writeFile(path.join(output, 'comparison.json'), JSON.stringify(results, null, 2))
}
try {
  const reference = await context.newPage()
  reference.on('dialog', dialog => dialog.dismiss())
  await reference.goto('http://127.0.0.1:9999/', { waitUntil: 'domcontentloaded' })
  await reference.waitForFunction(() => /[1-9]/.test(document.querySelector('#liveCount')?.textContent || ''), { timeout: 30000 })
  await check('overlook', 'theme cycle', async () => { for (let i = 0; i < 3; i++) await reference.getByRole('button', { name: '切换主题', exact: true }).click() })
  await check('overlook', 'drawer, filters, search and close', async () => {
    await reference.getByRole('button', { name: '打开会话列表' }).click()
    for (const filter of ['all', 'unread', 'open', 'pinned', 'recent', 'all']) await reference.locator(`[data-filter="${filter}"]`).click()
    await reference.locator('#drawerSearch').fill('not-a-session-parity-test')
    await reference.locator('#drawerSearch').fill('')
    await reference.locator('#drawerPanel button[aria-label="关闭"]').click()
    await reference.waitForFunction(() => document.querySelector('#drawerPanel').getBoundingClientRect().top >= innerHeight - 1)
  })
  await reference.screenshot({ path: path.join(output, 'overlook-390.png') })
  const auth = await context.request.get('http://127.0.0.1:30001/api/v1/auth/session')
  if (!auth.ok() || !(await auth.json()).authenticated) throw new Error('Saved browser login expired; log in through the browser. Credential files are not read.')
  const page = await context.newPage()
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('http://127.0.0.1:30001/mobile', { waitUntil: 'domcontentloaded' })
  await page.locator('[data-mobile-shell="independent"]').waitFor({ timeout: 30000 })
  await check('astrorder', 'independent mobile shell, no desktop shell', async () => {
    if (await page.locator('.astrorder-shell').count()) throw new Error('desktop shell rendered')
    if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('horizontal overflow')
  })
  await check('astrorder', 'theme cycle', async () => { for (let i = 0; i < 3; i++) await page.getByRole('button', { name: '切换主题', exact: true }).click() })
  await check('astrorder', 'drawer, filters, search and close', async () => {
    await page.getByRole('button', { name: '打开会话列表' }).click()
    for (const label of ['全部', '未读', '开放中', '置顶', '24小时', '全部']) await page.locator('.m-filters').getByRole('button', { name: label, exact: true }).click()
    await page.getByRole('textbox', { name: '搜索会话' }).fill('not-a-session-parity-test')
    if (await page.locator('.m-session-row').count()) throw new Error('search failed')
    await page.getByRole('textbox', { name: '搜索会话' }).fill('')
    await page.getByRole('button', { name: '关闭面板' }).click()
  })
  await check('astrorder', 'draft, multiline, clear (no send)', async () => {
    await page.getByRole('textbox', { name: '消息内容' }).fill('界面对照草稿\n不发送')
    await page.getByRole('button', { name: '清空输入内容' }).click()
    if (await page.getByRole('textbox', { name: '消息内容' }).inputValue()) throw new Error('clear failed')
  })
  await check('astrorder', 'status sheet and close', async () => {
    await page.getByRole('button', { name: '运行状态', exact: true }).click()
    await page.getByRole('button', { name: '复制会话 ID' }).waitFor()
    await page.getByRole('button', { name: '关闭面板' }).click()
  })
  await check('astrorder', 'attachment add/remove (no upload/send)', async () => {
    await page.locator('input[type=file]').setInputFiles({ name: 'parity.txt', mimeType: 'text/plain', buffer: Buffer.from('fixture only') })
    await page.getByRole('button', { name: '移除 parity.txt' }).click()
  })
  await check('astrorder', 'voice sheet open/cancel (no microphone request)', async () => {
    await page.getByRole('button', { name: '语音消息' }).click()
    await page.getByRole('button', { name: '开始录音' }).waitFor()
    await page.getByRole('button', { name: '关闭语音输入' }).click()
    await page.getByRole('dialog', { name: '语音输入' }).waitFor({ state: 'hidden' })
  })
  await check('astrorder', 'message select/quote/cancel and activity folds', async () => {
    const message = page.locator('.m-msg').first()
    await message.waitFor({ timeout: 30000 })
    await message.dispatchEvent('contextmenu')
    await page.getByRole('button', { name: '选择', exact: true }).click()
    await page.locator('.m-select-text').waitFor()
    await page.getByRole('button', { name: '关闭面板' }).click()
    await message.dispatchEvent('contextmenu')
    await page.getByRole('button', { name: '引用', exact: true }).click()
    await page.getByRole('button', { name: '取消引用' }).click()
    const pack = page.locator('.m-think-pack').first()
    if (await pack.count()) { await pack.locator(':scope > summary').click(); await pack.locator(':scope > summary').click() }
  })
  await check('astrorder', 'native model catalog/search (no model change)', async () => {
    await page.getByRole('button', { name: '选择会话模型', exact: true }).click()
    await page.locator('.m-model-choice').first().waitFor({ timeout: 45000 })
    await page.getByRole('textbox', { name: '搜索模型' }).fill('not-a-model-parity-test')
    if (await page.locator('.m-model-choice').count()) throw new Error('model search failed')
    await page.getByRole('button', { name: '关闭面板' }).click()
  })
  await page.screenshot({ path: path.join(output, 'astrorder-390.png') })
  results.push({ surface: 'astrorder', name: 'runtime page errors', status: errors.length ? 'failed' : 'passed', errors })
  await fs.writeFile(path.join(output, 'comparison.json'), JSON.stringify(results, null, 2))
  console.log(JSON.stringify({ passed: results.filter(r => r.status === 'passed').length, failed: results.filter(r => r.status === 'failed'), evidence: output }))
} finally { await context.close() }
