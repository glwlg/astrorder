import { chromium } from '@playwright/test'

const baseUrl = process.env.ASTRORDER_NATIVE_URL
const browserToken = process.env.ASTRORDER_NATIVE_BROWSER_TOKEN
const agentId = process.env.ASTRORDER_NATIVE_AGENT_ID
const sessionId = process.env.ASTRORDER_NATIVE_SESSION_ID
const artifactDir = process.env.ASTRORDER_NATIVE_ARTIFACT_DIR
const commandText = process.env.ASTRORDER_NATIVE_COMMAND_TEXT
const executablePath = process.env.ASTRORDER_NATIVE_CHROME || 'C:/Program Files/Google/Chrome/Application/chrome.exe'

if (!baseUrl || !browserToken || !agentId || !sessionId || !artifactDir || !commandText) {
  throw new Error('Native Hermes browser verification requires its controlled test environment')
}

async function assertVisible(locator, description) {
  try {
    await locator.waitFor({ state: 'visible', timeout: 20_000 })
  } catch {
    throw new Error(`Expected ${description} to be visible`)
  }
}

async function verifyViewport(browser, name, viewport, sendCommand) {
  const context = await browser.newContext({ viewport })
  const page = await context.newPage()
  try {
    await page.goto(`${baseUrl}/agents`, { waitUntil: 'networkidle' })
    await assertVisible(page.getByLabel('访问令牌'), 'the local authentication form')
    const authenticated = await page.evaluate(async (token) => {
      const response = await fetch('/api/v1/auth/session', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token }),
      })
      return response.ok
    }, browserToken)
    if (!authenticated) throw new Error('Controlled local authentication failed')
    await page.reload({ waitUntil: 'domcontentloaded' })
    await assertVisible(page.getByRole('heading', { name: '本机 Hermes' }), 'the local Hermes settings card')
    await assertVisible(page.getByText('本机 Hermes 已通过原生插件连接。'), 'the native connected state')
    await assertVisible(page.locator('.agent-card').filter({ hasText: '本机 Hermes' }), 'the real Hermes agent card')

    const agentDimensions = await page.evaluate(() => ({
      bodyClient: document.body.clientWidth,
      bodyScroll: document.body.scrollWidth,
      rootClient: document.documentElement.clientWidth,
      rootScroll: document.documentElement.scrollWidth,
    }))
    if (agentDimensions.bodyScroll > agentDimensions.bodyClient || agentDimensions.rootScroll > agentDimensions.rootClient) {
      throw new Error(`${name} Agents page has horizontal overflow`)
    }
    await page.screenshot({ path: `${artifactDir}/native-hermes-agents-${name}.png`, fullPage: true })

    await page.goto(`${baseUrl}/chat/${encodeURIComponent(sessionId)}?agent_id=${encodeURIComponent(agentId)}`, { waitUntil: 'networkidle' })
    await assertVisible(page.getByRole('heading', { name: sessionId, level: 2 }), 'the native Hermes session')
    const initialMessages = await page.locator('[data-testid^="message-"]').count()
    if (initialMessages < 2) {
      throw new Error('Expected the isolated native session to contain the captured smoke messages')
    }
    if (sendCommand) {
      await page.getByLabel('消息内容').fill(commandText)
      await page.getByRole('button', { name: '发送', exact: true }).click()
      await assertVisible(
        page.locator('.outbox-receipt').filter({ hasText: commandText }).getByText('已接受'),
        'the native injected command acceptance',
      )
      await page.waitForFunction(
        (count) => document.querySelectorAll('[data-testid^="message-"]').length >= count + 2,
        initialMessages,
        { timeout: 90_000 },
      )
    }
    await page.screenshot({ path: `${artifactDir}/native-hermes-session-${name}.png`, fullPage: true })
  } finally {
    await context.close()
  }
}

const browser = await chromium.launch({ executablePath, headless: true })
try {
  await verifyViewport(browser, 'desktop', { width: 1440, height: 1000 }, true)
  await verifyViewport(browser, 'mobile', { width: 390, height: 844 }, false)
  console.log('native Hermes browser verification passed')
} finally {
  await browser.close()
}
