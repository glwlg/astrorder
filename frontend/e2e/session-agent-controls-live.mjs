import { chromium, expect } from '@playwright/test'
let input='';for await(const chunk of process.stdin) input+=chunk
const {token}=JSON.parse(input)
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
try {
 const context=await browser.newContext({viewport:{width:1440,height:1000}})
 expect((await context.request.post('http://127.0.0.1:30002/api/v1/auth/session',{headers:{Origin:'http://127.0.0.1:30002'},data:{token}})).ok()).toBe(true)
 const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message))
 for(const mobile of [false,true]) {
  await page.setViewportSize(mobile?{width:390,height:844}:{width:1440,height:1000})
  await page.goto('http://127.0.0.1:30001/'+(mobile?'mobile':'chat'))
  if(mobile) await page.getByRole('button',{name:'打开会话列表'}).click()
  await page.getByLabel('按 Agent 筛选').selectOption('codex')
  if(!mobile) {
   await expect(page.locator('.session-row').first()).toBeVisible()
   for(const text of await page.locator('.session-row').allTextContents()) expect(text).toContain('Codex')
  }
  await page.getByRole('button',{name:'新建会话',exact:true}).first().click()
  await expect(page.getByRole('button',{name:'创建会话',exact:true})).toBeDisabled()
  await expect(page.getByLabel('选择 Agent')).toHaveValue('')
  const options=await page.getByLabel('选择 Agent').locator('option').evaluateAll(rows=>rows.map(row=>row.value).filter(Boolean))
  expect(options.length).toBeGreaterThan(0)
  await page.getByLabel('选择 Agent').selectOption(options[0])
  await expect(page.getByRole('button',{name:'创建会话',exact:true})).toBeEnabled()
  await page.getByRole('button',{name:'取消',exact:true}).click()
 }
 expect(errors).toEqual([])
 console.log(JSON.stringify({desktop_filter:true,desktop_agent_choice:true,mobile_agent_choice:true,no_sessions_created:true,errors}))
} finally {await browser.close()}
