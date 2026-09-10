import { chromium, expect } from '@playwright/test'
let input='';for await(const chunk of process.stdin) input+=chunk
const {token,host}=JSON.parse(input)
const origin='http://127.0.0.1:30001'
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
try {
 const context=await browser.newContext({viewport:{width:1440,height:1000}})
 expect((await context.request.get(origin+'/api/v1/bootstrap')).status()).toBe(401)
 const auth=await context.request.post(origin+'/api/v1/auth/session',{headers:{Origin:'https://'+host,Host:host},data:{token}})
 expect(auth.ok()).toBe(true)
 expect((await context.request.get(origin+'/api/v1/bootstrap',{headers:{Origin:'https://untrusted.invalid'}})).status()).toBe(403)
 const page=await context.newPage();const errors=[];const dev=[]
 page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.url().includes('/@vite/')||r.url().includes(':30002'))dev.push(r.url())})
 for(const route of ['/chat','/agents','/mobile']) {
  await page.goto(origin+route)
  await expect(page.locator('body')).not.toContainText('Blocked request')
  expect(await page.locator('script[src]').evaluateAll(elements=>elements.every(el=>el.src.includes('/assets/')))).toBe(true)
 }
 const websocket=await page.evaluate(()=>new Promise(resolve=>{const ws=new WebSocket(`ws://${location.host}/ws/v1/events?after=0`);const timer=setTimeout(()=>{ws.close();resolve(false)},10000);ws.onopen=()=>{clearTimeout(timer);ws.close();resolve(true)};ws.onerror=()=>{clearTimeout(timer);resolve(false)}}))
 expect(websocket).toBe(true)
 const env=(await (await context.request.get(origin+'/api/v1/environments')).json()).items
 expect(env.find(e=>e.id==='local').agents.every(a=>a.state==='connected')).toBe(true)
 expect(env.find(e=>e.name==='WSL').agents.find(a=>a.kind==='hermes').state).toBe('connected')
 expect(errors).toEqual([]);expect(dev).toEqual([])
 console.log(JSON.stringify({port:30001,compiled_assets:true,auth:true,private_domain_origin:true,foreign_origin_rejected:true,websocket:true,connectors_restored:true,dev_requests:dev.length,page_errors:errors.length}))
} finally {await browser.close()}
