import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { MobileApprovals } from './MobileApprovals'
import { MobileTranscript } from './MobileTranscript'
import type { Approval, Session } from '../../domain/types'

afterEach(() => { cleanup(); vi.restoreAllMocks() })
const session: Session = { id: 'thread', agent_id: 'codex', title: 'test', workspace: null, status: 'waiting_approval', updated_at: '' }
export const question: Approval = { id: 'codex:91', agent_id: 'codex', session_id: 'thread', title: 'Codex 等待你的回答', detail: '', state: 'pending', target_id: 'codex:91', data: { method: 'item/tool/requestUserInput', questions: [{ id: 'name', header: '姓名', question: '你叫什么名字？', options: null }, { id: 'hobby', header: '休闲', question: '你更喜欢哪种休闲方式？', isOther: true, options: [{ label: '看电影', description: '在影院或家里' }, { label: '听音乐', description: '放松' }] }] } }
it('renders native questions in the mobile conversation', () => {
 render(<MobileTranscript messages={[]} approvals={[question]} busy={false} hasOlder={false} loadingOlder={false} loadOlder={vi.fn()} onMessageAction={vi.fn()} onImage={vi.fn()} onSwipe={vi.fn()} onApproval={vi.fn()} />)
 expect(screen.getByRole('region', { name: '会话内询问' })).toBeInTheDocument()
 expect(screen.queryByText('允许本次')).not.toBeInTheDocument()
})
it('retries the same answer and operation identity after a lost receipt', async () => {
 const send = vi.spyOn(api, 'respondUserInput').mockRejectedValueOnce(new Error('connection lost')).mockResolvedValue({ accepted: true, request_id: question.id })
 render(<MobileApprovals session={session} approvals={[question]} />)
 fireEvent.change(screen.getByLabelText('你叫什么名字？'), { target: { value: '张三' } })
 fireEvent.click(screen.getByRole('radio', { name: /看电影/ }))
 fireEvent.click(screen.getByRole('button', { name: '发送回答' }))
 await screen.findByRole('alert')
 fireEvent.click(screen.getByRole('button', { name: '重试相同回答' }))
 await screen.findByText('回答已发送，等待 Codex 继续。')
 expect(send.mock.calls).toHaveLength(2)
 expect(send.mock.calls[1]).toEqual(send.mock.calls[0])
})
it('renders native questions instead of permission buttons', async () => {
 const send = vi.spyOn(api, 'respondUserInput').mockResolvedValue({ accepted: true, request_id: question.id })
 render(<MobileApprovals session={session} approvals={[question]} />)
 expect(screen.queryByText('允许本次')).not.toBeInTheDocument()
 fireEvent.change(screen.getByLabelText('你叫什么名字？'), { target: { value: '张三' } })
 fireEvent.click(screen.getByRole('radio', { name: /看电影/ }))
 fireEvent.click(screen.getByRole('button', { name: '发送回答' }))
 await waitFor(() => expect(send).toHaveBeenCalledWith('thread', expect.objectContaining({ agent_id: 'codex', request_id: 'codex:91', answers: { name: { answers: ['张三'] }, hobby: { answers: ['看电影'] } } })))
 expect(screen.getByText('回答已发送，等待 Codex 继续。')).toBeInTheDocument()
})
