import { IconMicrophone, IconPaperclip, IconPlus, IconPlayerStop, IconArrowUp, IconX } from '@tabler/icons-react'
import { Alert, Button, Group, Paper, Stack, Text, Textarea } from '@mantine/core'
import { useQueryClient } from '@tanstack/react-query'
import { type ChangeEvent, type KeyboardEvent, useRef, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { ApiError, api } from '../../api/client'
import { isDraftSendable, newCommandId, scopeKey } from '../../domain/semantics'
import type { Agent, Command, CommandAction, DraftAttachment, DraftState, Message, Session } from '../../domain/types'
import { selectCommands, useAstrorderStore } from '../../state/store'
import { submitBrowserCommand } from './commandActions'
import { VoiceInputSheet } from './VoiceInputSheet'
import { SessionModelControl } from './SessionModelControl'
import { notifySessionSubmitted } from '../../hooks/useSessionOrder'
import '../agents/agentsLayout.css'

const EMPTY_DRAFT: DraftState = { text: '', attachments: [] }
const allowedFiles = 'image/*,audio/*,.pdf,.txt,.md,.json,.csv,.log,.webp'

function hasCapability(agent: Agent | undefined, capability: string): boolean {
  if (!agent) return capability === 'chat' // 默认允许聊天，不设无谓门槛
  return Boolean(agent.capabilities?.includes(capability) || capability === 'chat')
}

function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.detail
  if (error instanceof Error) return error.message
  return '提交失败，请检查服务连接。'
}

function localCommand(session: Session, id: string, action: CommandAction, text: string): Command {
  return {
    id,
    session_id: session.id,
    agent_id: session.agent_id,
    action,
    state: 'received',
    text,
    attachments: [],
    created_at: new Date().toISOString(),
    error: null,
    target_id: null,
  }
}

export function ChatComposer({ session, agent }: { session: Session; agent?: Agent }) {
  const queryClient = useQueryClient()
  const draftKey = scopeKey(session.agent_id, session.id)
  const draft = useAstrorderStore((state) => state.drafts[draftKey] || EMPTY_DRAFT)
  const commands = useAstrorderStore(useShallow((state) => selectCommands(state, session.agent_id, session.id)))
  const [submitting, setSubmitting] = useState(false)
  const [voiceOpened, setVoiceOpened] = useState(false)
  const submittingRef = useRef(false)
  const [error, setError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const attachmentKeyRef = useRef(0)

  const canChat = hasCapability(agent, 'chat')
  const canAttach = hasCapability(agent, 'attachments')
  const runningCommand = commands.find((command) => (command.action === 'send' || command.action === 'enqueue') && (command.state === 'running' || command.state === 'accepted'))
  const busy = submitting || session.status === 'running' || session.status === 'waiting_approval' || Boolean(runningCommand)
  const canStop = hasCapability(agent, 'stop') && busy

  const updateDraft = (next: DraftState) => useAstrorderStore.getState().setDraft(session.agent_id, session.id, next)
  const setText = (text: string) => updateDraft({ ...draft, text })

  const addFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const files = [...(event.currentTarget.files || [])]
    if (files.length === 0) return
    const additions: DraftAttachment[] = files.map((file) => {
      attachmentKeyRef.current += 1
      return { key: `draft-attachment-${attachmentKeyRef.current}`, file }
    })
    updateDraft({ ...draft, attachments: [...draft.attachments, ...additions] })
    event.currentTarget.value = ''
  }

  const removeAttachment = (key: string) => {
    updateDraft({ ...draft, attachments: draft.attachments.filter((item) => item.key !== key) })
  }

  const submit = async (action: CommandAction, targetId: string | null = null) => {
    const files = draft.attachments.map((item) => item.file)
    if (action === 'send' || action === 'enqueue') {
      if (!isDraftSendable(draft.text, files)) return
      if (!canChat) {
        setError('当前 Agent 不支持聊天发送。')
        return
      }
    }
    if (submitting || submittingRef.current) return

    const commandId = newCommandId()
    const command = localCommand(session, commandId, action, action === 'send' || action === 'enqueue' ? draft.text : '')
    const store = useAstrorderStore.getState()
    store.addOutbox(command, 'submitting')

    // 乐观更新：在用户点击发送瞬间，立即在聊天框呈现用户消息，彻底消除等待迟滞
    if ((action === 'send' || action === 'enqueue') && command.text) {
      const optimisticMessage: Message = {
        id: `optimistic-${commandId}`,
        session_id: session.id,
        agent_id: session.agent_id,
        role: 'user',
        kind: 'message',
        text: command.text,
        attachments: [],
        created_at: new Date().toISOString(),
        command_id: commandId,
        tool: null,
      }
      store.mergeMessages(session.agent_id, session.id, [optimisticMessage])
    }

    submittingRef.current = true
    setSubmitting(true)
    setError(null)
    try {
      const result = await submitBrowserCommand({
        commandId,
        session,
        text: command.text,
        files: action === 'send' || action === 'enqueue' ? files : [],
        action,
        targetId,
        uploadAttachment: api.uploadAttachment,
        createCommand: api.createCommand,
      })
      store.updateOutboxAttachments(session.agent_id, session.id, commandId, result.attachments)
      store.mergeCommands([result.command])
      if (action === 'send' || action === 'enqueue') {
        if (result.command.state !== 'failed' && result.command.state !== 'unknown') {
          store.setDraft(session.agent_id, session.id, EMPTY_DRAFT)
          notifySessionSubmitted(session)
        }
      }
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', session.agent_id, session.id] })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', session.agent_id, session.id] })
    } catch (nextError) {
      const status = nextError instanceof ApiError && nextError.status >= 400 && nextError.status < 500 ? 'failed' : 'unknown'
      store.markOutboxError(session.agent_id, session.id, commandId, errorMessage(nextError), status)
      setError(errorMessage(nextError))
      // Draft and File objects intentionally remain in Zustand after a failed send.
    } finally {
      submittingRef.current = false
      setSubmitting(false)
    }
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault()
      if (!busy) void submit('send')
    }
  }

  const commitVoice = (file: File, transcript: string) => {
    attachmentKeyRef.current += 1
    const nextText = transcript ? `${draft.text.trim()}${draft.text.trim() ? '\n' : ''}${transcript}` : draft.text
    updateDraft({
      text: nextText,
      attachments: [...draft.attachments, { key: `draft-attachment-${attachmentKeyRef.current}`, file }],
    })
  }

  return (
    <>
      <Paper className="composer-card" withBorder radius="lg" p="sm">
        <Stack gap="xs">
        {error && <Alert color="red" variant="light" icon={<IconX size={17} />} aria-live="assertive">{error}</Alert>}
        {draft.attachments.length > 0 && (
          <div className="draft-attachments" aria-label="待发送附件">
            {draft.attachments.map((item) => (
              <span className="draft-attachment" key={item.key}>
                <IconPaperclip size={14} aria-hidden="true" />
                <span>{item.file.name}</span>
                <button type="button" className="draft-remove" onClick={() => removeAttachment(item.key)} aria-label={`移除 ${item.file.name}`}>
                  <IconX size={13} />
                </button>
              </span>
            ))}
          </div>
        )}
        {!canChat && <Text size="xs" c="dimmed">此 Agent 未报告 chat 能力，发送控件已禁用。</Text>}
        <Textarea className="composer-input" aria-label="消息内容" placeholder="随心输入" variant="unstyled" autosize minRows={2} maxRows={8} value={draft.text} onChange={(event) => setText(event.currentTarget.value)} onKeyDown={handleKeyDown} disabled={!canChat || submitting} />
        <Group className="composer-row" align="center" gap="xs" wrap="nowrap">
          <Button
            className="attachment-button"
            component="label"
            variant="subtle"
            color="gray"
            disabled={!canAttach || !canChat || submitting}
            aria-label={canAttach ? '添加附件' : '附件能力未提供'}
          >
            <IconPlus size={20} />
            <input ref={fileInputRef} hidden type="file" multiple accept={allowedFiles} onChange={addFiles} />
          </Button>
          <span className="composer-toolbar-spacer" />
          <SessionModelControl key={`model:${draftKey}`} session={session} />
          <Button
            className="attachment-button"
            variant="subtle"
            color="gray"
            disabled={!canChat || submitting}
            aria-label="语音输入"
            onClick={() => setVoiceOpened(true)}
          >
            <IconMicrophone size={19} />
          </Button>

          <Button
            className="send-button"
            color={busy ? 'red' : 'indigo'}
            radius="xl"
            disabled={submitting || (busy ? !canStop : !canChat || !isDraftSendable(draft.text, draft.attachments))}
            onClick={() => void submit(busy ? 'stop' : 'send', busy ? runningCommand?.id || null : null)}
            aria-label={busy ? '停止' : '发送'}
            aria-busy={submitting}
          >
            {busy ? <IconPlayerStop size={18} /> : <IconArrowUp size={18} />}
          </Button>
        </Group>

        </Stack>
      </Paper>
      <VoiceInputSheet opened={voiceOpened} onClose={() => setVoiceOpened(false)} onCommit={commitVoice} />
    </>
  )
}
