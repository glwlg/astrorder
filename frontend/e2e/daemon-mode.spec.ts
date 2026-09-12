import { expect, test } from '@playwright/test'

test('shows explicit daemon ownership in the real Agents page', async ({ page, baseURL }) => {
  test.skip(baseURL !== 'http://127.0.0.1:30013', 'Requires isolated fixture server')
  const token = process.env.ASTRORDER_E2E_TOKEN
  test.skip(!token, 'Requires fixture token')
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))

  const auth = await page.request.post('/api/v1/auth/session', { data: { token } })
  expect(auth.ok()).toBeTruthy()
  if (test.info().project.name === 'mobile') {
    await page.goto('/mobile/chat/fixture-pages?agent_id=mobile-protocol-fixture')
    await page.getByRole('button', { name: '应用设置' }).click()
    await page.getByRole('menuitem', { name: '连接管理' }).click()
  } else {
    await page.goto('/agents')
  }

  const hermesDaemonMode = page.getByTestId('environment-daemon-mode-local-hermes')
  await expect(hermesDaemonMode).toBeVisible()
  await expect(hermesDaemonMode).toHaveText('守护进程托管')
  const codexDaemonMode = page.getByTestId('environment-daemon-mode-local-codex')
  await expect(codexDaemonMode).toBeVisible()
  await expect(codexDaemonMode).toHaveText('守护进程托管')
  await page.screenshot({ path: test.info().outputPath(`daemon-mode-${test.info().project.name}.png`), fullPage: true })
  expect(errors).toEqual([])
})
