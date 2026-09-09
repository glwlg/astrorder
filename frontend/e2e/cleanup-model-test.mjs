import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
const origin = 'http://127.0.0.1:30001'
const context = await chromium.launchPersistentContext('C:/Users/luwei/AppData/Local/hermes/cache/astrorder-mobile-reference-browser', { executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
try {
 const bootstrap = async () => (await (await context.request.get(origin + '/api/v1/bootstrap')).json()).sessions
 const rows = (await bootstrap()).filter(s => s.title === '临时模型切换验收' && s.agent_id === 'ssh-hermes-ssh-c5f75ce2e7c847daabe75ea7')
 if (rows.length > 1) throw new Error('Ambiguous temporary session; no cleanup attempted')
 const results = []
 for (const s of rows) {
  const url = `${origin}/api/v1/sessions/${encodeURIComponent(s.id)}?agent_id=${encodeURIComponent(s.agent_id)}`
  let response = await context.request.delete(url, { headers: { Origin: origin } })
  if (response.status() === 409) {
   const stopped = await context.request.post(origin + '/api/v1/commands', { headers: { Origin: origin }, data: { id: crypto.randomUUID(), agent_id: s.agent_id, session_id: s.id, action: 'stop', text: '', attachment_ids: [], target_id: s.id } })
   results.push({ id: s.id, stop_status: stopped.status() })
   await expect.poll(async () => { response = await context.request.delete(url, { headers: { Origin: origin } }); return response.status() }, { timeout: 30000, intervals: [500, 1000, 2000] }).toBe(200)
  }
  expect(response.ok()).toBe(true)
  expect((await bootstrap()).some(row => row.id === s.id && row.agent_id === s.agent_id)).toBe(false)
  results.push({ id: s.id, deleted: true, absence_verified: true })
 }
 await fs.writeFile('test-results/mobile-parity/model-cleanup.json', JSON.stringify(results, null, 2))
 console.log(JSON.stringify(results))
} finally { await context.close() }
