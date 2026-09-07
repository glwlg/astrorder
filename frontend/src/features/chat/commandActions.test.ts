import { describe, expect, it } from 'vitest'
import type { Attachment, Command, Session } from '../../domain/types'
import { submitBrowserCommand } from './commandActions'

const session: Session = {
  id: 'session-1',
  agent_id: 'agent-1',
  title: '隔离测试会话',
  workspace: 'P:/workspace/isolated',
  status: 'idle',
  updated_at: '2026-01-01T00:00:00Z',
}

function attachment(id: string, name: string): Attachment {
  return { id, name, media_type: 'image/png', url: `/api/v1/attachments/${id}` }
}

describe('browser command submission', () => {
  it('preserves image-only input and uploads same file selections separately', async () => {
    const file = new File(['image'], 'same.png', { type: 'image/png' })
    const differentFile = new File(['other'], 'different.png', { type: 'image/png' })
    const uploaded: string[] = []
    let sent: Record<string, unknown> | undefined
    const result = await submitBrowserCommand({
      commandId: 'command-image-only',
      session,
      text: '',
      files: [file, differentFile, file],
      action: 'send',
      uploadAttachment: async (current) => {
        uploaded.push(current.name)
        return attachment(`attachment-${uploaded.length}`, current.name)
      },
      createCommand: async (payload) => {
        sent = payload as unknown as Record<string, unknown>
        return { id: 'command-image-only', state: 'accepted' } as Command
      },
    })

    expect(result.command.id).toBe('command-image-only')
    expect(uploaded).toEqual(['same.png', 'different.png', 'same.png'])
    expect(sent?.text).toBe('')
    expect(sent?.attachment_ids).toEqual(['attachment-1', 'attachment-2', 'attachment-3'])
  })

  it('does not retry an ambiguous HTTP submission automatically', async () => {
    let attempts = 0
    await expect(submitBrowserCommand({
      commandId: 'command-unknown',
      session,
      text: '一次',
      files: [],
      action: 'send',
      uploadAttachment: async () => attachment('unused', 'unused.png'),
      createCommand: async () => {
        attempts += 1
        throw new Error('网络中断')
      },
    })).rejects.toThrow('网络中断')

    expect(attempts).toBe(1)
  })
})
