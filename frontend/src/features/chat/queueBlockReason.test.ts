import { expect, it } from 'vitest'
import type { Command } from '../../domain/types'
import { queueBlockReason } from './queueBlockReason'

const command = (id: string, action: Command['action'], state: Command['state'], agent_id = 'a', session_id = 's'): Command => ({ id, action, state, agent_id, session_id, text: '', attachments: [], created_at: '2026-10-20T00:00:00Z', error: null })
it('explains an unresolved historical receipt without treating idle or later guidance as completion', () => {
 expect(queueBlockReason([command('old', 'send', 'unknown'), command('guidance', 'send', 'completed')], 'a', 's')).toContain('上一条消息的发送结果尚未确认')
})
it('explains missing completion and ignores accepted controls and other sessions', () => {
 expect(queueBlockReason([command('old', 'enqueue', 'accepted')], 'a', 's')).toContain('上一条消息的完成回执尚未收到')
 expect(queueBlockReason([command('stop', 'stop', 'accepted'), command('foreign', 'send', 'unknown', 'other')], 'a', 's')).toBeNull()
})
it('keeps a claimed request blocked while it crosses native admission', () => {
 expect(queueBlockReason([command('claim', 'enqueue', 'received')], 'a', 's')).toContain('上一条指令仍在提交')
})
