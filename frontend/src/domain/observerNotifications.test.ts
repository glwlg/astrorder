import { expect, it } from 'vitest'
import { notificationForEvent } from './notifications'
const event = { id:'observer-1',cursor:3,type:'native.observation',agent_id:'codex',session_id:'native-id',data:{ event:'PermissionRequest',id:'hook-1',notification:true,observed_at:Date.now()/1000 } }
it('raw observer events cannot duplicate canonical approval notices', () => {
 const result=notificationForEvent(event)
 expect(result).toBeNull()
})
it('observer tool activity and delayed replay do not spam notifications', () => {
 expect(notificationForEvent({...event,data:{...event.data,event:'PostToolUse'}})).toBeNull()
 expect(notificationForEvent({...event,data:{...event.data,notification:false}})).toBeNull()
 expect(notificationForEvent({...event,data:{...event.data,observed_at:1}})).toBeNull()
})
