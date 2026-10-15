import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import { test } from 'node:test'

const source=fs.readFileSync(new URL('./service-worker.js',import.meta.url),'utf8')
test('same full session key shares a group and distinct agents do not',async()=>{
 const tabs=new Map([[1,{id:1,windowId:1,groupId:-1}],[2,{id:2,windowId:1,groupId:-1}],[3,{id:3,windowId:1,groupId:-1}]])
 const markers=new Map([[1,'a::same-session'],[2,'a::same-session'],[3,'b::same-session']])
 const listeners={};let next=10
 const chrome={
  scripting:{executeScript:async({target})=>[{result:'astrorder:'+Buffer.from(markers.get(target.tabId)).toString('base64url')}]},
  tabs:{get:async id=>tabs.get(id),query:async()=>Array.from(tabs.values()),group:async({tabIds,groupId})=>{const id=groupId??next++;for(const tab of tabIds)tabs.get(tab).groupId=id;return id},onUpdated:{addListener:f=>listeners.updated=f},onCreated:{addListener:f=>listeners.created=f},onRemoved:{addListener:()=>{}}},
  tabGroups:{TAB_GROUP_ID_NONE:-1,update:async()=>{},get:async id=>({id})},
  runtime:{onMessage:{addListener:f=>listeners.message=f},onStartup:{addListener:()=>{}},onInstalled:{addListener:()=>{}}},
  storage:{session:{get:async()=>({}),set:async()=>{}}},
 }
 vm.runInNewContext(source,{chrome,atob:s=>Buffer.from(s,'base64').toString('binary'),decodeURIComponent,escape,TextDecoder,Uint8Array,console,setTimeout,clearTimeout})
 await listeners.updated(1,{status:'complete'});await listeners.updated(2,{status:'complete'});await listeners.updated(3,{status:'complete'})
 assert.equal(tabs.get(1).groupId,tabs.get(2).groupId)
 assert.notEqual(tabs.get(1).groupId,tabs.get(3).groupId)
})

test('worker activation restores all existing session tabs',async()=>{
 const restored=[]
 const chrome={
  scripting:{executeScript:async({target})=>[{result:'astrorder:'+Buffer.from('agent::session-'+target.tabId).toString('base64url')}]},
  tabs:{get:async id=>({id,windowId:1,groupId:-1}),query:async()=>[{id:1,windowId:1,groupId:-1},{id:2,windowId:1,groupId:-1}],group:async({tabIds})=>{restored.push(...tabIds);return tabIds[0]},onUpdated:{addListener:()=>{}},onCreated:{addListener:()=>{}},onRemoved:{addListener:()=>{}}},
  tabGroups:{TAB_GROUP_ID_NONE:-1,update:async()=>{}},
  runtime:{onMessage:{addListener:()=>{}},onStartup:{addListener:()=>{}},onInstalled:{addListener:()=>{}}},
  storage:{session:{get:async()=>({}),set:async()=>{}}}
 }
 vm.runInNewContext(source,{chrome,atob:s=>Buffer.from(s,'base64').toString('binary'),TextDecoder,Uint8Array,console,setTimeout,clearTimeout})
 await new Promise(resolve=>setImmediate(resolve))
 assert.deepEqual(restored,[1,2])
})
