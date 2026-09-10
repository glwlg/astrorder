import { expect, it } from 'vitest'
import { notificationForEvent } from './notifications'
const event = { id:'observer-1',cursor:3,type:'native.observation',agent_id:'codex',session_id:'native-id',data:{ event:'PermissionRequest',id:'hook-1',notification:true,observed_at:Date.now()/1000 } }
it('observer approval notification cannot be mistaken for an actionable approval', () => {
 const result=notificationForEvent(event)
 expect(result?.title).toBe('原生端等待审批')
 expect(result?.message).toContain('请在原生客户端处理')
})
it('observer tool activity and delayed replay do not spam notifications', () => {
 expect(notificationForEvent({...event,data:{...event.data,event:'PostToolUse'}})).toBeNull()
 expect(notificationForEvent({...event,data:{...event.data,notification:false}})).toBeNull()
 expect(notificationForEvent({...event,data:{...event.data,observed_at:1}})).toBeNull()
})
