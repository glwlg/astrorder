import { chromium, expect } from '@playwright/test'
let input=''; for await (const chunk of process.stdin) input+=chunk
const {token}=JSON.parse(input)
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
try {
 const context=await browser.newContext({viewport:{width:1500,height:1000}})
 const auth=await context.request.post('http://127.0.0.1:30002/api/v1/auth/session',{headers:{Origin:'http://127.0.0.1:30002'},data:{token}})
 expect(auth.ok()).toBe(true)
 const page=await context.newPage()
 const errors=[];page.on('pageerror',e=>errors.push(e.message))
 await page.goto('http://127.0.0.1:30001/chat/20260909_095323_ba4bde?agent_id=local-hermes-default')
 const transcript=page.locator('.transcript')
 await expect(transcript.getByText('你好',{exact:true})).toHaveCount(1)
 await expect(transcript.getByText('你好！需要我帮你做什么？',{exact:true})).toHaveCount(1)
 const text=await transcript.innerText()
 expect(text.indexOf('你好')).toBeLessThan(text.indexOf('你好！需要我帮你做什么？'))
 await page.locator('.history-button').click()
 await expect(transcript.getByText('已连接。',{exact:true})).toHaveCount(1)
 await expect(transcript.getByText('你好',{exact:true})).toHaveCount(1)
 expect(errors).toEqual([])
 console.log(JSON.stringify({scope:'reported native session read-only',user_hello_count:1,reply_count:1,user_before_reply:true,older_page_keeps_single_hello:true,errors}))
} finally {await browser.close()}
