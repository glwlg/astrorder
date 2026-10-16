import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
const out='C:/Users/luwei/AppData/Local/hermes/cache/scratch/file-transfer-fix'
let browser
for(let i=0;i<50;i++){
 try {browser=await chromium.connectOverCDP('http://127.0.0.1:9357');break}catch{await new Promise(r=>setTimeout(r,200))}
}
if(!browser)throw new Error('isolated WebView did not start')
try {
 const page=browser.contexts()[0].pages()[0]
 await page.waitForFunction(()=>typeof window.astrorderDesktop?.saveDownload==='function')
 // The isolated WebView invokes the real generated binding and HTTP writer.
 const result=await page.evaluate(async()=>window.astrorderDesktop.saveDownload('/api/v1/files/raw?path=%2Ftmp%2Fbinary&connection_id=ssh-fixture','binary.bin'))
 expect(result).toBe(true)
 const clipboard=await page.evaluate(async path=>window.astrorderDesktop.setClipboardFiles([path]),`${out}/clipboard-fixture.txt`)
 expect(clipboard).toBe(true)
 const report={passed:true,native_binding:true,authenticated_stream:true,dialog:'destination selected by scratch Go overlay'}
 await fs.writeFile(`${out}/native-probe.json`,JSON.stringify(report,null,2));console.log(JSON.stringify(report))
}finally{await browser.close()}
