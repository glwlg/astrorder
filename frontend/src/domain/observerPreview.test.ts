import { expect,it } from 'vitest'
import { notificationForEvent } from './notifications'
it('shows the native reply and session title in completion notifications',()=>{
 const result=notificationForEvent({id:'preview',cursor:1,type:'native.observation',agent_id:'codex',session_id:'session',data:{event:'Stop',id:'event',notification:true,observed_at:Date.now()/1000,session_title:'修复登录问题',preview:'已修复登录失败，并通过回归测试。'}})
 expect(result?.title).toBe('修复登录问题')
 expect(result?.message).toBe('已修复登录失败，并通过回归测试。')
})
