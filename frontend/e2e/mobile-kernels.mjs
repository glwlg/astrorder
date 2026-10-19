import {chromium,expect} from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'
let input='';for await(const chunk of process.stdin)input+=chunk
const {origin,credential,root}=JSON.parse(input)
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
const results=[]
try{
 const context=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true})
 const login=await context.request.post(origin+'/api/v1/auth/session',{data:{token:credential},headers:{Origin:origin}});expect(login.ok()).toBe(true)
 const page=await context.newPage();let nativeDialogs=0;page.on('dialog',async d=>{nativeDialogs++;await d.dismiss()})
 await page.goto(origin+'/mobile');await page.getByRole('button',{name:'应用设置',exact:true}).click();await page.getByRole('menuitem',{name:'内核管理',exact:true}).click()
 await expect(page.getByRole('heading',{name:'内核管理',exact:true})).toBeVisible()
 await expect(page.getByText('有可用更新',{exact:true})).toBeVisible({timeout:20000})
 await page.screenshot({path:path.join(root,'mobile-kernels.png'),fullPage:true})
 let posts=0;page.on('request',r=>{if(r.method()==='POST'&&r.url().endsWith('/services/kernels/operations'))posts++})
 await page.getByRole('button',{name:'重启小内核',exact:true}).click();await expect(page.getByRole('dialog',{name:'重启小内核？',exact:true})).toBeVisible()
 await page.getByRole('dialog',{name:'重启小内核？',exact:true}).evaluate(node=>Promise.all(node.getAnimations({subtree:true}).map(a=>a.finished.catch(()=>{}))))
 await page.screenshot({path:path.join(root,'mobile-kernels-confirm.png'),fullPage:true})
 await page.getByRole('button',{name:'取消',exact:true}).click();expect(posts).toBe(0)
 for(const [label,target,action] of [['重启大内核','app','restart'],['重启小内核','daemon','restart'],['更新并重启小内核','daemon','update']]){
  const before=await (await context.request.get(origin+'/api/v1/services/kernels/status')).json()
  await page.getByRole('button',{name:label,exact:true}).click()
  const responsePromise=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/services/kernels/operations'))
  await page.getByRole('button',{name:action==='update'?'确认更新并重启':'确认重启',exact:true}).click()
  const response=await responsePromise;expect(response.status()).toBe(202);const op=await response.json();expect(op.target).toBe(target);expect(op.action).toBe(action)
  expect(response.request().postDataJSON()).toEqual({target,action,confirm_active:true})
  await expect(page.getByText('操作已成功完成。',{exact:true})).toBeVisible({timeout:100000})
  const receipt=await (await context.request.get(origin+'/api/v1/services/kernels/operations/'+op.id)).json();expect(receipt.status).toBe('success')
  const after=await (await context.request.get(origin+'/api/v1/services/kernels/status')).json();expect(after[target].process).not.toEqual(before[target].process)
  if(target==='app')expect(after.daemon.process).toEqual(before.daemon.process)
  results.push({target,action,status:receipt.status,new_identity:true,daemon_preserved:target==='app'?true:undefined})
 }
 expect(posts).toBe(3);expect(nativeDialogs).toBe(0)
 const result={passed:true,viewport:'390x844',real_chrome:true,real_http_controller:true,cancel_posts:0,native_dialogs:nativeDialogs,operations:results}
 await fs.writeFile(path.join(root,'mobile-browser-acceptance.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result))
}finally{await browser.close()}
