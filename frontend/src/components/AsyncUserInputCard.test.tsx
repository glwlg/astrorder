import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Command, Message } from '../domain/types'
import { Transcript } from '../features/chat/Transcript'
import { MobileTranscript } from '../features/mobile/MobileTranscript'
import { AsyncUserInputCard, hasLaterNativeInput } from './AsyncUserInputCard'

const message: Message = { id: 'call-question', agent_id: 'codex', session_id: 'thread', role: 'assistant', kind: 'message', text: '', attachments: [], tool: null, command_id: null, created_at: '2026-10-16T05:53:24Z', delivery: 'async', questions: [{ title: '喜欢哪种题材？', options: ['星际探索', '时间旅行', '人工智能'] }] }
const command: Command = { id: 'async-answer:call-question', agent_id: 'codex', session_id: 'thread', action: 'send', target_id: message.id, text: '时间旅行', attachments: [], state: 'accepted', created_at: '', error: null }
afterEach(() => { cleanup(); vi.restoreAllMocks() })
it.each(['desktop', 'mobile'])('renders async choices in the real %s transcript and sends ordinary user input', async mode => {
 const send = vi.spyOn(api, 'createCommand').mockResolvedValue(command)
 const sync = vi.spyOn(api, 'respondUserInput')
 vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [message, { ...message, id: 'answer', delivery: null, questions: null, role: 'user', text: '时间旅行' }], next_cursor: null })
 render(mode === 'desktop' ? <MantineProvider><Transcript messages={[message]} outbox={[]} /></MantineProvider> : <MobileTranscript messages={[message]} busy={false} hasOlder={false} loadingOlder={false} loadOlder={vi.fn()} onMessageAction={vi.fn()} onImage={vi.fn()} onSwipe={vi.fn()} />)
 fireEvent.click(screen.getByRole('radio', { name: '时间旅行' }))
 fireEvent.click(screen.getByRole('button', { name: '发送回答' }))
 await screen.findByText('回答已接收。')
 expect(send).toHaveBeenCalledExactlyOnceWith({ id: command.id, agent_id: 'codex', session_id: 'thread', action: 'send', text: '时间旅行', attachment_ids: [], target_id: message.id })
 expect(sync).not.toHaveBeenCalled()
})
it('restores closure from native subsequent input, including manual answers, without sending', () => {
 const send = vi.spyOn(api, 'createCommand')
 render(<MantineProvider><Transcript messages={[message, { ...message, role: 'user', id: 'manual-answer', text: '时间旅行', delivery: null, questions: null }]} outbox={[]} /></MantineProvider>)
 expect(screen.getByText('已有后续输入，此问题已结束。')).toBeInTheDocument()
 expect(screen.queryByRole('button', { name: '发送回答' })).not.toBeInTheDocument()
 expect(send).not.toHaveBeenCalled()
})
it('reads a lost receipt before retrying and never dispatches a second answer', async () => {
 const send = vi.spyOn(api, 'createCommand').mockRejectedValue(new Error('lost response'))
 const read = vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [command] })
 render(<AsyncUserInputCard message={message} />)
 fireEvent.click(screen.getByRole('radio', { name: '时间旅行' }))
 fireEvent.click(screen.getByRole('button', { name: '发送回答' }))
 await screen.findByRole('alert')
 fireEvent.click(screen.getByRole('button', { name: '检查发送结果' }))
 await screen.findByText('回答已接收。')
 expect(read).toHaveBeenCalledWith('thread', 'codex')
 expect(send).toHaveBeenCalledTimes(1)
})
it('shows unconfirmed restored commands as read-only receipt checks after reload', async () => {
 const send = vi.spyOn(api, 'createCommand')
 vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [{ ...command, state: 'unknown' }] })
 render(<AsyncUserInputCard message={message} commands={[{ ...command, state: 'unknown' }]} />)
 fireEvent.click(screen.getByRole('button', { name: '检查发送结果' }))
 await screen.findByRole('alert')
 expect(send).not.toHaveBeenCalled()
})
it('allows correcting a confirmed failure using a new operation', async () => {
 const send = vi.spyOn(api, 'createCommand').mockResolvedValueOnce({ ...command, state: 'failed', error: 'not dispatched' }).mockResolvedValueOnce({ ...command, id: 'retry' })
 vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [], next_cursor: null })
 render(<AsyncUserInputCard message={message} />)
 fireEvent.click(screen.getByRole('radio', { name: '时间旅行' }))
 fireEvent.click(screen.getByRole('button', { name: '发送回答' }))
 await screen.findByRole('alert')
 fireEvent.click(screen.getByRole('radio', { name: '人工智能' }))
 fireEvent.click(screen.getByRole('button', { name: '发送回答' }))
 await waitFor(() => expect(send).toHaveBeenCalledTimes(2))
 expect(send.mock.calls[1][0].id).not.toBe(send.mock.calls[0][0].id)
 expect(send.mock.calls[1][0].text).toBe('人工智能')
})
it('does not redispatch when a lost receipt remains absent', async () => {
 const send = vi.spyOn(api, 'createCommand').mockRejectedValue(new Error('lost response'))
 vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
 render(<AsyncUserInputCard message={message} />)
 fireEvent.click(screen.getByRole('radio', { name: '时间旅行' }))
 fireEvent.click(screen.getByRole('button', { name: '发送回答' }))
 await screen.findByRole('alert')
 fireEvent.click(screen.getByRole('button', { name: '检查发送结果' }))
 await screen.findByText('回答回执暂不可读，请稍后检查。')
 expect(send).toHaveBeenCalledTimes(1)
})
it('disables answers in a read-only session', () => {
 render(<AsyncUserInputCard message={message} readOnly />)
 expect(screen.getByRole('radio', { name: '时间旅行' })).toBeDisabled()
 expect(screen.getByRole('button', { name: '发送回答' })).toBeDisabled()
})
it('requires native input from the same scope before closing a question', () => {
 const user: Message = { ...message, role: 'user', text: '时间旅行', id: 'native-answer' }
 expect(hasLaterNativeInput([message, { ...user, id: 'optimistic-answer' }], 0)).toBe(false)
 expect(hasLaterNativeInput([message, { ...user, session_id: 'other' }], 0)).toBe(false)
 expect(hasLaterNativeInput([message, user], 0)).toBe(true)
})
it('sends all free-form answers as ordinary user input', async () => {
 const send = vi.spyOn(api, 'createCommand').mockResolvedValue(command)
 vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [], next_cursor: null })
 render(<AsyncUserInputCard message={{ ...message, questions: [{ title: '题材？' }, { title: '原因？' }] }} />)
 fireEvent.change(screen.getByRole('textbox', { name: '题材？ 自行填写' }), { target: { value: '时间旅行' } })
 fireEvent.change(screen.getByRole('textbox', { name: '原因？ 自行填写' }), { target: { value: '喜欢因果谜题' } })
 fireEvent.click(screen.getByRole('button', { name: '发送回答' }))
 await screen.findByText('回答已接收。')
 expect(send.mock.calls[0][0].text).toBe('题材？\n时间旅行\n\n原因？\n喜欢因果谜题')
})
