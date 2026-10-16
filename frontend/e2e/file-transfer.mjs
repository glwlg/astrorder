import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
const origin=process.env.ASTRORDER_FILE_TEST_ORIGIN||'http://127.0.0.1:30001'
const output=process.env.ASTRORDER_FILE_TEST_OUTPUT||'C:/Users/luwei/AppData/Local/hermes/cache/scratch/file-transfer-fix/browser-regression.json'
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
const results=[]
try {
 for(const connection of ['local','ssh-fixture']) {
  const page=await browser.newPage({viewport:{width:1440,height:1000},acceptDownloads:true})
  const session={id:'file-fixture',agent_id:'file-agent',title:'File transfer fixture',status:'idle',control_state:'owned',workspace:connection==='local'?'C:/fixture':'/home/fixture',connection_id:connection,updated_at:'2026-10-16T07:00:00Z'}
  let staged,clipboard,raw=0
  await page.addInitScript(()=>{window.astrorderDesktop={setClipboardFiles:async paths=>{window.__clipboard=paths;return true}}})
  await page.routeWebSocket('**/ws/**',()=>{})
  await page.route('**/api/v1/**',async route=>{
   const req=route.request(),path=new URL(req.url()).pathname;let data={items:[]}
   if(path.endsWith('/auth/session'))data={authenticated:true}
   else if(path.endsWith('/bootstrap'))data={protocol_version:1,cursor:0,agents:[{id:session.agent_id,name:'Files fixture',kind:'codex',status:'ready',capabilities:['chat']}],sessions:[session],projects:[],commands:[]}
   else if(path.endsWith('/preferences'))data={appearance:{},session_pins:{},pinned_projects:[],project_order:[]}
   else if(path.endsWith('/connections'))data={local:{state:'offline',available:false},ssh:{items:[]}}
   else if(path.endsWith('/open-sessions'))data={known_agent_ids:[session.agent_id],items:[{agent_id:session.agent_id,id:session.id}]}
   else if(path.endsWith('/sync'))data=session
   else if(path.endsWith('/model'))data={model:'fixture',provider:'fixture',read_only:false}
   else if(path.endsWith('/messages'))data={items:[],next_cursor:null}
   else if(path.endsWith('/files/tree'))data={root:session.workspace,items:[{name:'文件.bin',path:session.workspace+'/文件.bin',is_dir:false,size:4}]}
   else if(path.endsWith('/files/stage')){staged=req.postDataJSON();data={local_paths:['C:/fixture/staged.bin'],clipboard_set:false}}
   else if(path.endsWith('/files/raw')){raw++;await route.fulfill({status:200,body:Buffer.from([0,1,255,10]),headers:{'Content-Type':'application/octet-stream'}});return}
   await route.fulfill({json:data})
  })
  await page.goto(`${origin}/chat/${session.id}?agent_id=${session.agent_id}`)
  await page.waitForTimeout(600)
  await page.keyboard.press('Control+Shift+E')
  const row=page.locator('.file-tree-row').filter({hasText:'文件.bin'})
  await expect(row).toBeVisible()
  await row.click({button:'right'});await page.getByText('复制文件 (Ctrl+C)',{exact:true}).click()
  await expect.poll(()=>page.evaluate(()=>window.__clipboard)).toEqual(['C:/fixture/staged.bin'])
  expect(staged).toMatchObject({connection_id:connection,session_id:session.id,copy_to_clipboard:false,paths:[session.workspace+'/文件.bin']})
  await row.click({button:'right'})
  const downloaded=page.waitForEvent('download')
  await page.getByText('下载 / 另存为',{exact:true}).click()
  const download=await downloaded
  expect(download.suggestedFilename()).toBe('文件.bin')
  const bytes=await fs.readFile(await download.path());expect([...bytes]).toEqual([0,1,255,10]);expect(raw).toBe(1)
  expect(page.context().pages()).toHaveLength(1)
  results.push({connection,copy:true,download_bytes:bytes.length,popup:false})
  await page.close()
 }
 const result={passed:true,scope:'real browser UI with isolated APIs and native clipboard fixture; downloaded bytes read back',results}
 await fs.writeFile(output,JSON.stringify(result,null,2));console.log(JSON.stringify(result))
}finally{await browser.close()}
