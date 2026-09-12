import { expect, test } from '@playwright/test'

// Only the inert mobile_fixture_server on 30013, never a real Agent.
for (const theme of ['light', 'dark']) {
  test(`side chat shares the real composer and isolated replies (${theme})`, async ({ page, baseURL }) => {
    test.skip(baseURL !== 'http://127.0.0.1:30013', 'Requires isolated fixture server')
    const token = process.env.ASTRORDER_E2E_TOKEN
    test.skip(!token, 'Requires fixture token')
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    let creations = 0
    page.on('request', request => {
      if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/sessions') creations += 1
    })
    const auth = await page.request.post('/api/v1/auth/session', { data: { token } })
    expect(auth.ok()).toBeTruthy()
    // Legacy wire payload: the generated marker exists but its flag was lost.
    const legacy = await page.request.post('/api/v1/sessions', { data: {
      agent_id: 'mobile-protocol-fixture', title: '[侧边聊天]', ephemeral: false,
    } })
    expect(legacy.ok()).toBeTruthy()
    expect((await legacy.json()).ephemeral).toBe(false)
    creations = 0
    await page.addInitScript(scheme => localStorage.setItem('mantine-color-scheme-value', scheme), theme)
    await page.goto('/chat/fixture-pages?agent_id=mobile-protocol-fixture')
    const main = page.locator('.chat-layout > .chat-column')
    await expect(main.getByRole('textbox', { name: '消息内容' })).toBeVisible()
    const rail = page.locator('.session-rail')
    const heading = page.locator('.rail-heading')
    const initialCount = (await heading.textContent()) || ''
    await expect(heading).toHaveText('项目会话5')
    await expect(rail.getByText('[侧边聊天]', { exact: true })).toHaveCount(0)
    await main.getByRole('textbox', { name: '消息内容' }).fill('主会话未发送草稿')
    const createdResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/sessions')
    await page.keyboard.press('Control+Alt+s')
    const created = await (await createdResponse).json()
    expect(created.ephemeral).toBe(true)
    const side = page.locator('.sidechat-viewer')
    const input = side.getByRole('textbox', { name: '消息内容' })
    await expect(input).toBeVisible({ timeout: 15000 })
    await expect(rail.getByText('[侧边聊天]', { exact: true })).toHaveCount(0)
    await expect(heading).toHaveText(initialCount)
    await expect(side.locator('.message-row')).toHaveCount(0)
    await expect(side.locator('.sc-chat-input')).toHaveCount(0)
    for (const name of ['语音输入', '选择会话模型', '发送']) {
      await expect(side.getByRole('button', { name, exact: true })).toBeVisible()
    }
    await expect(side.getByLabel('添加附件')).toBeVisible()
    await expect(side.getByRole('button', { name: '选择会话模型' })).not.toContainText('读取模型', { timeout: 10000 })
    const metrics = await page.evaluate(() => {
      const mainCard = document.querySelector('.chat-layout > .chat-column > .composer-card')!
      const sideCard = document.querySelector('.sidechat-viewer > .composer-card')!
      const panel = document.querySelector('.sidechat-viewer')!
      const a = getComputedStyle(mainCard), b = getComputedStyle(sideCard)
      return {
        main: { background: a.backgroundColor, radius: a.borderRadius, position: a.position },
        side: { background: b.backgroundColor, radius: b.borderRadius, position: b.position },
        panel: panel.getBoundingClientRect().toJSON(), card: sideCard.getBoundingClientRect().toJSON(),
        send: sideCard.querySelector('.send-button')!.getBoundingClientRect().toJSON(),
      }
    })
    expect(metrics.side).toEqual(metrics.main)
    expect(metrics.card.bottom).toBeLessThanOrEqual(metrics.panel.bottom)
    expect(metrics.send.right).toBeLessThanOrEqual(metrics.card.right)
    expect(metrics.send.left).toBeGreaterThanOrEqual(metrics.card.left)
    await page.screenshot({ path: `test-results/sidechat-empty-${theme}.png`, fullPage: true })
    await input.fill('侧边隔离协议测试')
    await side.getByRole('button', { name: '发送', exact: true }).click()
    await expect(side.getByText('协议测试回显：侧边隔离协议测试', { exact: true })).toBeVisible({ timeout: 15000 })
    await expect(main.getByRole('textbox', { name: '消息内容' })).toHaveValue('主会话未发送草稿')
    await expect(main.getByText('协议测试回显：侧边隔离协议测试', { exact: true })).toHaveCount(0)
    await expect(rail.getByText('临时协议回显', { exact: true })).toHaveCount(0)
    await expect(heading).toHaveText(initialCount)
    const bootstrap = await (await page.request.get('/api/v1/bootstrap')).json()
    expect(bootstrap.sessions.find((s: { id: string }) => s.id === created.id)).toMatchObject({ ephemeral: true, title: '临时协议回显' })
    await page.locator('.sidecar-tabs-bar').getByTitle('关闭标签页').last().click()
    await expect(side).toHaveCount(0)
    await page.reload()
    await expect(main.getByRole('textbox', { name: '消息内容' })).toBeVisible()
    await expect(heading).toHaveText(initialCount)
    await expect(rail.getByText('临时协议回显', { exact: true })).toHaveCount(0)
    await expect(rail.getByText('[侧边聊天]', { exact: true })).toHaveCount(0)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/mobile/chat/fixture-pages?agent_id=mobile-protocol-fixture')
    await page.getByRole('button', { name: '打开会话列表' }).click()
    await expect(page.locator('.m-session-row').getByText('fixture-pages', { exact: true })).toBeVisible()
    await expect(page.locator('.m-session-row').getByText('临时协议回显', { exact: true })).toHaveCount(0)
    await page.screenshot({ path: `test-results/sidechat-hidden-mobile-${theme}.png`, fullPage: true })
    expect(creations).toBe(1)
    expect(errors).toEqual([])
  })
}
