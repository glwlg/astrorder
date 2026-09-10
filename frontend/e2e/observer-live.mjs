import {chromium,expect} from '@playwright/test'
import fs from 'node:fs/promises'
let input='';for await(const chunk of process.stdin) input+=chunk
const {token}=JSON.parse(input)
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
try {
 const context=await browser.newContext({viewport:{width:1440,height:1000}})
 const origin='http://127.0.0.1:30001'
 expect((await context.request.post(origin+'/api/v1/auth/session',{headers:{Origin:origin},data:{token}})).ok()).toBe(true)
 const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message))
 await page.goto(origin+'/agents')
 const panels=page.getByLabel('原生观察钩子')
 await expect(panels).toHaveCount(2)
 await expect(panels.filter({hasText:'待原生信任'})).toHaveCount(2,{timeout:30000})
 await expect(page.getByText('TASK_EVENTS',{exact:true})).toHaveCount(0)
 const agents=(await (await context.request.get(origin+'/api/v1/agents')).json()).items
 for(const agent of agents.filter(a=>a.kind==='hermes'&&a.status==='ready')) {expect(agent.capabilities).toContain('history');expect(agent.capabilities).toContain('stop')}
 expect(errors).toEqual([])
 await fs.mkdir('test-results/observer-live',{recursive:true});await page.screenshot({path:'test-results/observer-live/agents.png',fullPage:true})
 console.log(JSON.stringify({observer_panels:2,native_hook_review_required:2,effective_hermes_capabilities:true,page_errors:0,scope:'real read-only UI; no native prompts or approval changes'}))
} catch(e){console.error(e.message);process.exitCode=1} finally {await browser.close()}
