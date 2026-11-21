import { chromium, expect } from '@playwright/test'
let input='';for await(const chunk of process.stdin) input+=chunk
const {token, sessionId, agentId, sessionTitle}=JSON.parse(input)
if (!sessionId || !agentId || !sessionTitle) throw new Error("sessionId, agentId and sessionTitle are required")
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
try {
 const context=await browser.newContext({viewport:{width:1440,height:1000}})
 expect((await context.request.post('http://127.0.0.1:30002/api/v1/auth/session',{headers:{Origin:'http://127.0.0.1:30002'},data:{token}})).ok()).toBe(true)
 const native=await context.request.get('http://127.0.0.1:30002/api/v1/user-activity');expect(native.ok()).toBe(true)
 const activity=(await native.json()).items.find(r=>r.id===sessionId && r.agent_id===agentId);expect(activity).toBeTruthy()
 const page=await context.newPage();await page.goto(`http://127.0.0.1:30001/chat/${encodeURIComponent(sessionId)}?agent_id=${encodeURIComponent(agentId)}`)
 const row=page.locator('.session-row').filter({has:page.locator('.session-row-title').filter({hasText:sessionTitle})})
 await expect(row).toBeVisible({timeout:30000})
 await expect(row).not.toContainText('2天前',{timeout:30000})
 const result=await row.evaluate((el, title)=>{const group=el.closest('.session-project-group');const titles=[...group.querySelectorAll('.session-row-title')].map(e=>e.textContent);return {visible_index:titles.indexOf(title),visible_count:titles.length,time:el.querySelector('.session-row-time').textContent}}, sessionTitle)
 expect(result.visible_index).toBeLessThan(4)
 console.log(JSON.stringify({native_last_user_at:activity.last_user_at,...result,scope:'live read-only, no prompts'}))
} finally {await browser.close()}
