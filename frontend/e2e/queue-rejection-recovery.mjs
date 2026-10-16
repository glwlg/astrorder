import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
const origin = process.env.ASTRORDER_QUEUE_TEST_ORIGIN || 'http://127.0.0.1:30019'
const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
try {
 const page = await browser.newPage({viewport:{width:1440,height:1000}})
 const errors=[];page.on('pageerror',e=>errors.push(e.message))
 const session={id:'queue-fixture',agent_id:'fixture-hermes',title:'Queue recovery fixture',status:'idle',workspace:null,updated_at:'2026-10-15T00:00:00Z'}
 const attachments=Array.from({length:4},(_,i)=>({id:`blob-${i}`,name:`photo-${i}.png`,media_type:'image/png',url:`/blob-${i}`}))
 let command={id:'rejected',agent_id:session.agent_id,session_id:session.id,action:'enqueue',state:'failed',text:'带四个附件的失败引导',attachments,created_at:session.updated_at,target_id:null,error:'attachments must be queued for the next Hermes turn'}
 let rows=[command],creates=[],sends=0,deletes=0
 await page.routeWebSocket('**/ws/**',()=>{})
 await page.route('**/api/v1/**',async route=>{
  const req=route.request(),path=new URL(req.url()).pathname;let data={items:[]}
  if(path.endsWith('/auth/session'))data={authenticated:true}
  else if(path.endsWith('/bootstrap'))data={protocol_version:1,cursor:0,agents:[{id:session.agent_id,name:'Hermes fixture',kind:'hermes',status:'ready',capabilities:['chat','attachments','queue'],limitation:null}],sessions:[session],projects:[],commands:rows}
  else if(path.endsWith('/preferences'))data={appearance:{},session_pins:{},pinned_projects:[],project_order:[]}
  else if(path.endsWith('/connections'))data={local:{state:'offline',available:false},ssh:{items:[]}}
  else if(path.endsWith('/sync'))data=session
  else if(path.endsWith('/model'))data={model:'fixture',provider:'fixture',read_only:false}
  else if(path.endsWith('/messages'))data={items:[],next_cursor:null}
  else if(path.endsWith('/commands')&&req.method()==='GET')data={items:rows}
  else if(path.endsWith('/commands')&&req.method()==='POST'){
   const payload=req.postDataJSON();creates.push(payload);command={...payload,attachments,created_at:session.updated_at,state:'queued',error:null};rows.push(command);data=command
  }else if(path.includes('/commands/')&&req.method()==='DELETE'){
   deletes++;const id=path.split('/').at(-1);data={...rows.find(r=>r.id===id),state:'cancelled'};rows=rows.map(r=>r.id===id?data:r)
  }else if(path.includes('/commands/')&&path.endsWith('/send')){sends++;data={...command,state:'accepted'};rows=rows.map(r=>r.id===command.id?data:r)}
  await route.fulfill({json:data})
 })
 const open=()=>page.goto(`${origin}/chat/${session.id}?agent_id=${session.agent_id}`)
 await open()
 await expect(page.getByLabel('排队消息')).toContainText('4 个附件')
 expect(errors).toEqual([])
 await page.getByRole('button',{name:'编辑',exact:true}).click({timeout:5000}).catch(async error => { console.log('UI',await page.locator('body').innerText(),'ERRORS',errors); throw error })
 await expect(page.getByLabel('消息内容')).toHaveValue('带四个附件的失败引导')
 for(const a of attachments)await expect(page.getByText(a.name,{exact:true})).toBeVisible()
 await expect(page.getByLabel('排队消息')).toHaveCount(0)
 // Reset only isolated API fixture, then exercise cancellation and explicit retry.
 rows=[{...command,id:'rejected',state:'failed'}];await open()
 await page.getByRole('button',{name:'删除',exact:true}).click()
 await expect(page.getByLabel('排队消息')).toHaveCount(0)
 rows=[{...command,id:'rejected',state:'failed'}];await open()
 await page.getByRole('button',{name:'重试',exact:true}).click()
 await expect.poll(()=>sends).toBe(1)
 expect(creates).toHaveLength(1);expect(creates[0].attachment_ids).toEqual(attachments.map(a=>a.id));expect(creates[0].id).not.toBe('rejected')
 await expect(page.getByLabel('排队消息')).toHaveCount(0)
 expect(errors).toEqual([])
 const result={passed:true,scope:'real browser UI with isolated API fixtures; no user messages sent',attachments:4,edit_restored:true,delete_removed:true,retry_sent:true,sends,deletes,page_errors:errors}
 await fs.writeFile(process.env.ASTRORDER_QUEUE_TEST_OUTPUT||'C:/Users/luwei/AppData/Local/hermes/cache/scratch/queue-recovery/browser.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result))
}finally{await browser.close()}
