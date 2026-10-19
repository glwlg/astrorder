import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'
const root=process.env.QR_HARNESS_ROOT
if(!root)throw new Error('QR_HARNESS_ROOT is required')
const origin=(await fs.readFile(path.join(root,'origin.txt'),'utf8')).trim()
const credential=await fs.readFile(path.join(root,'fixture-credential.txt'),'utf8')
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
const results=[]
const auth=async context=>(await (await context.request.get(origin+'/api/v1/auth/session')).json()).authenticated
const makePC=async()=>{
 const context=await browser.newContext({viewport:{width:1280,height:900}}),page=await context.newPage()
 await page.goto(origin)
 const svg=page.locator('.auth-card svg').filter({has:page.locator('path[fill="#FFFFFF"]')})
 await expect(page.getByText('确认码：',{exact:true})).toBeVisible()
 await page.addScriptTag({path:path.resolve('node_modules/jsqr/dist/jsQR.js')})
 const actual=svg
 const xml=await actual.evaluate(node=>new XMLSerializer().serializeToString(node))
 const url=await page.evaluate(async xml=>{
  const image=new Image();image.src='data:image/svg+xml;base64,'+btoa(xml);await image.decode()
  const canvas=document.createElement('canvas');canvas.width=canvas.height=1000
  const ctx=canvas.getContext('2d');ctx.fillStyle='#fff';ctx.fillRect(0,0,1000,1000);ctx.drawImage(image,0,0,1000,1000)
  const pixels=ctx.getImageData(0,0,1000,1000);return window.jsQR(pixels.data,1000,1000)?.data
 },xml)
 expect(url).toMatch(new RegExp('^'+origin.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+'/auth/qr#'))
 const file=path.join(root,`qr-${results.length}.png`);await actual.screenshot({path:file})
 return {context,page,url,file}
}
try{
 const pc=await makePC()
 const stranger=await browser.newContext({viewport:{width:390,height:844}}),strangerPage=await stranger.newPage()
 await strangerPage.goto(pc.url)
 await expect(strangerPage.getByText(/先登录/).first()).toBeVisible()
 expect(await auth(pc.context)).toBe(false)
 expect(await auth(stranger)).toBe(false)
 const phone=await browser.newContext({viewport:{width:390,height:844}})
 const login=await phone.request.post(origin+'/api/v1/auth/session',{data:{token:credential},headers:{Origin:origin}});expect(login.ok()).toBe(true)
 const mobile=await phone.newPage()
 await mobile.goto(origin+'/auth/qr/scan')
 await mobile.locator('input[type="file"]').setInputFiles(pc.file)
 await expect(mobile.getByRole('button',{name:/确认授权/})).toBeVisible()
 expect(new URL(mobile.url()).hash).toBe('')
 const code=await pc.page.getByText(/^\d{6}$/).textContent();await expect(mobile.getByText(code,{exact:true})).toBeVisible()
 expect(await auth(pc.context)).toBe(false)
 await mobile.screenshot({path:path.join(root,'phone-confirm.png')})
 await mobile.getByRole('button',{name:/确认授权/}).click()
 await expect(mobile.getByText(/已授权/).first()).toBeVisible()
 await expect.poll(()=>auth(pc.context)).toBe(true)
 expect(await auth(stranger)).toBe(false)
 const params=new URLSearchParams(new URL(pc.url).hash.slice(1))
 const replay=await phone.request.post(origin+'/api/v1/auth/qr/approve',{data:{id:params.get('id'),scan_secret:params.get('scan_secret'),decision:'approve'},headers:{Origin:origin}})
 expect(replay.status()).toBe(410)
 results.push({case:'approve',actual_qr_image_decoded:true,unauthenticated_phone_blocked:true,fragment_cleared:true,explicit_confirmation:true,original_pc_authenticated:true,stranger_unauthenticated:true,replay_status:410})
 const rejected=await makePC();await mobile.goto(rejected.url)
 await expect(mobile.getByRole('button',{name:/确认授权/})).toBeVisible()
 await mobile.getByRole('button',{name:/取消|拒绝/}).click()
 await expect(mobile.getByText(/已取消|已拒绝/).first()).toBeVisible()
 await expect(rejected.page.getByText(/取消/).first()).toBeVisible()
 expect(await auth(rejected.context)).toBe(false)
 results.push({case:'reject',pc_remains_unauthenticated:true})
 const cameraPC=await makePC()
 const cameraPage=await phone.newPage()
 await cameraPage.addInitScript(data=>{
  navigator.mediaDevices.getUserMedia=async()=>{
   const image=new Image();image.src=data;await image.decode()
   const canvas=document.createElement('canvas');canvas.width=canvas.height=1000
   const ctx=canvas.getContext('2d');ctx.fillStyle='#fff';ctx.fillRect(0,0,1000,1000);ctx.drawImage(image,0,0,1000,1000)
   const stream=canvas.captureStream(10);window.__qrCameraStream=stream;window.__qrCameraCanvas=canvas
   return stream
  }
 },'data:image/png;base64,'+(await fs.readFile(cameraPC.file)).toString('base64'))
 await cameraPage.goto(origin+'/auth/qr/scan')
 await expect(cameraPage.getByRole('button',{name:/确认授权/})).toBeVisible()
 await expect.poll(()=>cameraPage.evaluate(()=>window.__qrCameraStream.getTracks().every(track=>track.readyState==='ended'))).toBe(true)
 expect(await auth(cameraPC.context)).toBe(false)
 results.push({case:'camera_frame',real_media_stream_frame_decoded:true,tracks_stopped_after_scan:true,confirmation_required:true,physical_camera:false})
 const result={passed:true,scope:'real Chrome, independent cookie contexts, real Go HTTP auth handler and real QR image decoder; no physical camera',results}
 await fs.writeFile(path.join(root,'browser-acceptance.json'),JSON.stringify(result,null,2));console.log(JSON.stringify(result))
}finally{await browser.close()}
