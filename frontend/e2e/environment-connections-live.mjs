import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
let input='';for await(const chunk of process.stdin) input+=chunk
const {token}=JSON.parse(input)
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
const output='test-results/environment-connections';await fs.mkdir(output,{recursive:true})
let context, remoteId, connectedByTest=false
try {
 context=await browser.newContext({viewport:{width:1440,height:1000}})
 const auth=await context.request.post('http://127.0.0.1:30002/api/v1/auth/session',{headers:{Origin:'http://127.0.0.1:30002'},data:{token}});expect(auth.ok()).toBe(true)
 const page=await context.newPage(); const errors=[];page.on('pageerror',e=>errors.push(e.message))
 await page.goto('http://127.0.0.1:30001/agents')
 await expect(page.getByRole('heading',{name:'连接管理',exact:true})).toHaveCount(1)
 const local=page.locator('.environment-row').filter({has:page.getByRole('heading',{name:'本机',exact:true})})
 await expect(local.locator('.environment-agent')).toHaveCount(2)
 const remote=page.locator('.environment-row').filter({has:page.getByRole('heading',{name:'WSL',exact:true})})
 await remote.getByRole('button',{name:/发现/}).click()
 await expect(remote.getByText('已发现',{exact:true})).toHaveCount(1)
 const snapshot=await (await context.request.get('http://127.0.0.1:30002/api/v1/environments')).json()
 const environment=snapshot.items.find(x=>x.name==='WSL');remoteId=environment.id
 expect(environment.agents.every(x=>x.available)).toBe(true)
 const codex=remote.locator('.environment-agent').filter({has:page.getByText('Codex',{exact:true})})
 await codex.getByRole('button',{name:'接入',exact:true}).click();connectedByTest=true
 await expect(codex.getByText('已接入',{exact:true})).toBeVisible({timeout:60000})
 const bootstrap=await (await context.request.get('http://127.0.0.1:30002/api/v1/bootstrap')).json()
 const agent='ssh-codex-'+remoteId
 const sessions=bootstrap.sessions.filter(s=>s.agent_id===agent)
 expect(sessions.length).toBeGreaterThan(0);expect(sessions.every(s=>s.connection_id===remoteId)).toBe(true)
 const session=sessions.find(s=>s.native_kind!=='subagent')
 const query=`?agent_id=${encodeURIComponent(agent)}`
 const messages=await context.request.get(`http://127.0.0.1:30002/api/v1/sessions/${session.id}/messages${query}&limit=2`);expect(messages.ok()).toBe(true)
 const model=await context.request.get(`http://127.0.0.1:30002/api/v1/sessions/${session.id}/model${query}`);expect(model.ok()).toBe(true);expect((await model.json()).model).toBeTruthy()
 await page.screenshot({path:`${output}/desktop-connected.png`})
 await codex.getByRole('button',{name:'断开',exact:true}).click();await expect(codex.getByText('已发现',{exact:true})).toBeVisible();connectedByTest=false
 await page.setViewportSize({width:390,height:844});await page.goto('http://127.0.0.1:30001/mobile');await page.getByRole('button',{name:'连接管理',exact:true}).click()
 await expect(page.locator('.environment-row')).toHaveCount(snapshot.items.length)
 expect(await page.locator('.environment-connections').evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
 await page.screenshot({path:`${output}/mobile-discovered.png`})
 expect(errors).toEqual([])
 console.log(JSON.stringify({scope:'live SSH discover/connect/read/disconnect; no prompts',remote_sessions:sessions.length,model_read:true,original_remote_disconnected_restored:true,errors}))
} finally {
 if(connectedByTest && remoteId) await context.request.post(`http://127.0.0.1:30002/api/v1/environments/${remoteId}/agents/codex/disconnect`,{headers:{Origin:'http://127.0.0.1:30002'}})
 await browser.close()
}
