import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
const origin = process.env.ASTRORDER_UPGRADE_TEST_ORIGIN || 'http://127.0.0.1:30001'
const out = 'C:/Users/luwei/AppData/Local/hermes/cache/scratch/agent-upgrade-fix'
const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
try {
 const page = await browser.newPage({viewport:{width:1440,height:1000}})
 const errors=[];page.on('pageerror',e=>errors.push(e.message))
 let update = false, upgrades = 0
 let release
 const blocked = new Promise(resolve => { release = resolve })
 await page.routeWebSocket('**/ws/**',()=>{})
 await page.route('**/api/v1/**',async route=>{
  const req=route.request(),path=new URL(req.url()).pathname;let data={items:[]}
  if(path.endsWith('/auth/session'))data={authenticated:true}
  else if(path.endsWith('/bootstrap'))data={protocol_version:1,cursor:0,agents:[],sessions:[],projects:[],commands:[]}
  else if(path.endsWith('/preferences'))data={appearance:{},session_pins:{},pinned_projects:[],project_order:[]}
  else if(path.endsWith('/connections'))data={local:{state:'offline',available:false},ssh:{items:[]}}
  else if(path.endsWith('/environments'))data={items:[{id:'local',name:'本机',method:'local',discovered:true,agents:[]}]}
  else if(path.endsWith('/agents/upgrade/targets'))data={items:[{id:'codex',name:'本机 Codex',kind:'codex',connection_id:'local',current_version:'0.163.0',latest_version:update?'0.164.0':'0.163.0',has_update:update},{id:'grok',name:'本机 Grok',kind:'grok',connection_id:'local',current_version:'1.0.60',latest_version:'1.0.60',has_update:false}]}
  else if(path.endsWith('/upgrade')){
   upgrades++
   await blocked
   return route.fulfill({contentType:'application/x-ndjson',body:'{"type":"init","command":"fixture updater"}\n{"type":"chunk","data":"fixture log after settings closed\\n"}\n{"type":"done","ok":true,"exit_code":0}\n'})
  }
  await route.fulfill({json:data})
 })
 await page.goto(origin+'/chat')
 await page.getByRole('button',{name:'打开设置',exact:true}).click()
 await page.getByRole('button',{name:'全节点升级',exact:true}).click()
 await expect(page.getByRole('button',{name:'开始升级选中 Agent (0)'})).toBeDisabled()
 await expect(page.getByText('已是最新',{exact:true})).toHaveCount(2)
 await expect(page.getByText('latest',{exact:true})).toHaveCount(0)
 update=true
 await page.getByRole('button',{name:'重新检测',exact:true}).click()
 await page.getByRole('button',{name:'开始升级选中 Agent (1)'}).click()
 await expect.poll(()=>upgrades).toBe(1)
 await page.getByRole('button',{name:'放入后台运行',exact:true}).click()
 await expect(page.getByRole('dialog',{name:'Agent 批量升级中心 · 正在执行',exact:true})).toHaveCount(0)
 await page.keyboard.press('Escape')
 await expect(page.getByRole('dialog',{name:'设置',exact:true})).toHaveCount(0)
 await page.getByRole('button',{name:'后台任务',exact:true}).click()
 await page.getByText('批量升级 Agent (1)',{exact:true}).click()
 await expect(page.getByLabel('升级日志内容')).toContainText('[INIT]')
 await expect(page.getByRole('dialog',{name:'后台任务',exact:true})).toHaveCount(0)
 release()
 await expect(page.getByLabel('升级日志内容')).toContainText('fixture log after settings closed')
 await expect(page.getByText('全部 1 个 Agent 已顺利完成升级',{exact:true})).toBeVisible()
 await page.screenshot({path:out+'/logs-without-settings.png'})
 await page.getByRole('dialog',{name:/升级日志/}).getByRole('button',{name:'关闭',exact:true}).click()
 await page.getByRole('button',{name:'后台任务',exact:true}).click()
 await page.getByText('批量升级 Agent (1)',{exact:true}).click()
 await expect(page.getByLabel('升级日志内容')).toContainText('升级成功')
 expect(errors).toEqual([])
 const result={passed:true,origin,scope:'installed browser assets with isolated API fixtures; no native updates executed',no_update_default_selection:true,actual_version_labels:true,settings_unmounted_log_restored:true,completion_log_retained:true,upgrades,page_errors:errors}
 await fs.writeFile(out+'/browser-result.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result))
}finally{await browser.close()}
