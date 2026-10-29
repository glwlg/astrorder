import { test } from 'node:test'
import assert from 'node:assert/strict'
import { installExternalLinks } from './desktop-links.js'

test('external clicks and window.open use the native browser; internal and handled actions remain intact', async () => {
  const handlers={}, opened=[], native=[]
  const win={location:{href:'http://wails.localhost/chat/one',origin:'http://wails.localhost'},document:{addEventListener:(type,fn)=>handlers[type]=fn},open:(...args)=>native.push(args),console,alert:()=>assert.fail('unexpected failure')}
  installExternalLinks(win,url=>opened.push(url))
  const click=(href, options={})=>{
    const event={type:'click',button:0,defaultPrevented:false,target:{closest:()=>({href,hasAttribute:()=>false})},preventDefault(){this.defaultPrevented=true},...options}
    handlers[event.type](event)
    return event.defaultPrevented
  }
  assert.equal(click('https://grafana.com/docs/'),true)
  assert.equal(click('https://grafana.com/middle',{type:'auxclick',button:1}),true)
  assert.equal(click('http://wails.localhost/api/v1/files/raw'),false)
  click('https://grafana.com/handled',{defaultPrevented:true})
  win.open('https://grafana.com/script','_blank')
  win.open('/api/v1/attachments/image','_blank')
  await Promise.resolve()
  assert.deepEqual(opened,['https://grafana.com/docs/','https://grafana.com/middle','https://grafana.com/script'])
  assert.deepEqual(native,[['/api/v1/attachments/image','_blank']])
})
