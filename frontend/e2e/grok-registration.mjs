import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
const origin='http://127.0.0.1:30001'
const token = process.env.ASTRORDER_E2E_TOKEN
if (!token) throw new Error("ASTRORDER_E2E_TOKEN is required")
const expectedProjects = JSON.parse(process.env.ASTRORDER_E2E_PROJECTS || "[]")
if (!expectedProjects.length) throw new Error("ASTRORDER_E2E_PROJECTS must list the expected projects")
await fs.mkdir("test-results", { recursive: true })
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
try {
 const context=await browser.newContext({viewport:{width:1440,height:1000}})
 await context.route('**/*',async route=>{
  const request=route.request(),url=new URL(request.url())
  if(url.origin!==origin){await route.abort();return}
  await route.continue({headers:{...request.headers(),Authorization:'Bearer '+token}})
 })
 const page=await context.newPage()
 await page.goto(origin)
 await expect(page.locator('.session-project-group').first()).toBeVisible({timeout:15000})
 const groups=page.locator('.session-project-group')
 const results=[]
 for(const name of expectedProjects){
  const selected=groups.filter({has:page.locator('.session-project-header')}).filter({hasText:name}).first()
  await selected.getByRole('button',{name:'新建会话',exact:true}).click()
  const select=page.getByLabel('选择 Agent',{exact:true})
  await expect(select).toBeVisible()
  const options=await select.locator('option').evaluateAll(nodes=>nodes.filter(n=>n.value).map(n=>({id:n.value,label:n.textContent})))
  const groks=options.filter(option=>option.label.includes('Grok'))
  results.push({project:name,agent_count:options.length,grok_count:groks.length,grok_labels:groks.map(x=>x.label)})
  expect(groks).toHaveLength(1)
  if(name==='astrorder')expect(groks[0].id).toBe('local-grok')
  else expect(groks[0].id.startsWith('ssh-grok-')).toBe(true)
  await page.getByRole('button',{name:'取消',exact:true}).click()
 }
 const output={ok:true,scope:'installed frontend and live API; opened dialogs only, no session creation or model request',results}
 await fs.writeFile('test-results/grok-registration.json',JSON.stringify(output,null,2))
 console.log(JSON.stringify(output))
}finally{await browser.close()}
