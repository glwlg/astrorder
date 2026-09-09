import { chromium } from '@playwright/test'
import fs from 'node:fs/promises'
const origin = 'http://127.0.0.1:30001'
const context = await chromium.launchPersistentContext('C:/Users/luwei/AppData/Local/hermes/cache/astrorder-mobile-reference-browser', { executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true, viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })
const results = []
try {
 const data = await (await context.request.get(origin + '/api/v1/bootstrap')).json()
 const agents = data.agents.filter(a => a.kind === 'hermes')
 for (const agent of agents) {
  let session
  const row = { agent_id: agent.id }
  try {
   const created = await context.request.post(origin + '/api/v1/sessions', { headers: { Origin: origin }, data: { agent_id: agent.id, title: '临时模型切换验收', workspace: null } })
   row.create_status = created.status()
   if (!created.ok()) { row.error = (await created.json()).detail; continue }
   session = await created.json()
   row.session_id = session.id
   const page = await context.newPage()
   page.on('dialog', dialog => dialog.accept())
   await page.goto(`${origin}/mobile/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(agent.id)}`)
   await page.getByRole('button', { name: '选择会话模型', exact: true }).click({ timeout: 35000 })
   const choice = page.locator('.m-model-choice').filter({ hasText: /gpt-6-astra|gpt-5.6-luna/ }).first()
   await choice.waitFor({ timeout: 45000 })
   row.choice = await choice.textContent()
   const response = page.waitForResponse(r => r.url().endsWith('/model') && r.request().method() === 'POST', { timeout: 45000 })
   await choice.click()
   await page.getByRole('button', { name: '确认切换模型', exact: true }).click()
   const result = await response
   row.switch_status = result.status()
   row.result = await result.json()
   await page.close()
  } catch (error) { row.error = error.message }
  finally {
   if (session) {
    const deleted = await context.request.delete(`${origin}/api/v1/sessions/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(agent.id)}`, { headers: { Origin: origin } })
    row.cleanup_status = deleted.status()
    const refreshed = await (await context.request.get(origin + '/api/v1/bootstrap')).json()
    row.cleanup_verified = !refreshed.sessions.some(s => s.id === session.id && s.agent_id === agent.id)
   }
   results.push(row)
   await fs.writeFile('test-results/mobile-parity/model-diagnostic.json', JSON.stringify(results, null, 2))
  }
 }
 console.log(JSON.stringify(results))
} finally { await context.close() }
